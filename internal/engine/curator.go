package engine

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

//go:embed prompts/curator.md
var curatorPrompt string

// curatorEvery is the number of ended sessions of a repository, with no Curator session, that start a Curator.
const curatorEvery = 10

const (
	// curatorMaxText is the maximum number of characters of the text of one item in the prompt of the Curator.
	curatorMaxText = 1000
	// curatorMaxItems is the maximum number of items of each kind in the prompt of the Curator.
	curatorMaxItems = 20
	// curatorCut ends the text of an item that was cut to curatorMaxText characters.
	curatorCut = " (cut)"
	// curatorRepeatedFixRounds is the number of fix rounds of a task from which the Curator gets the task.
	curatorRepeatedFixRounds = 2
	// curatorMaxRounds is the maximum number of rounds of one task in the prompt of the Curator.
	curatorMaxRounds = 6
)

// curatorKey is the key in e.stops of the Worker of the Curator with this session id.
type curatorKey int64

type tellCuratorInput struct {
	Repository string `json:"repository"`
	Text       string `json:"text"`
}

type editMemoryInput struct {
	Old    string `json:"old"`
	New    string `json:"new"`
	Reason string `json:"reason"`
}

// startCuratorAfterEnds starts a Curator for repository when curatorEvery sessions of the repository ended since the
// last Curator session.
func (e *Engine) startCuratorAfterEnds(ctx context.Context, repository string) error {
	e.curatorsMu.Lock()
	defer e.curatorsMu.Unlock()
	ended, err := e.queries.CountSessionsEndedSinceCurator(ctx, repository)
	if err != nil || ended < curatorEvery {
		return err
	}
	return e.startCuratorLocked(ctx, repository)
}

// startCurator starts a Curator for repository. While a Curator of repository runs, it makes one more Curator wait
// for the end of the running Curator.
func (e *Engine) startCurator(ctx context.Context, repository string) error {
	e.curatorsMu.Lock()
	defer e.curatorsMu.Unlock()
	return e.startCuratorLocked(ctx, repository)
}

// tellCurator saves the request of the Owner and gives it to a Curator of the repository of the input. While a Curator
// of the repository runs, the request waits for the next Curator. The result goes to the Triager chat.
func (e *Engine) tellCurator(ctx context.Context, c caller, _ github.Repository, input tellCuratorInput) (string, error) {
	if c.repository != "" {
		return "", refuse("Only the Triager chat tells the Curator, after the Owner approves it.")
	}
	if empty(input.Text) {
		return "", refuse("text must not be empty.")
	}
	repository, err := e.repository(input.Repository)
	if err != nil {
		return "", err
	}
	if repository.Owner() != c.organization {
		return "", refuse("%s is not in the organization %s.", input.Repository, c.organization)
	}
	e.curatorsMu.Lock()
	defer e.curatorsMu.Unlock()
	id, err := e.queries.AddCuratorRequest(ctx, store.AddCuratorRequestParams{Repository: input.Repository, Text: input.Text})
	if err != nil {
		return "", err
	}
	if err := e.startCuratorLocked(ctx, input.Repository); err != nil {
		return "", errors.Join(err, e.queries.DeleteCuratorRequest(ctx, id))
	}
	return "Sent the request to the Curator. The result arrives later.", nil
}

// startCuratorLocked is startCurator for a caller that holds curatorsMu.
func (e *Engine) startCuratorLocked(ctx context.Context, repository string) error {
	if _, running := e.curators[repository]; running {
		e.curators[repository] = true
		return nil
	}
	e.curators[repository] = false
	return e.runCurator(ctx, repository)
}

// runCurator adds a Curator session for repository and runs it in the background. It takes the requests of the Owner
// that wait. A request stays in the store until the Triager chat gets the result, so a restart of Mobius keeps it. At
// its end, the Curator that waits starts. The caller holds curatorsMu and has set e.curators[repository].
func (e *Engine) runCurator(ctx context.Context, repository string) error {
	if e.ended() {
		delete(e.curators, repository)
		return refuse("Mobius stops, so no Curator starts now.")
	}
	organization, _, _ := strings.Cut(repository, "/")
	a, err := e.newAgent(ctx, Spec{Role: CuratorRole, Organization: organization, Repository: repository})
	if err != nil {
		delete(e.curators, repository)
		return err
	}
	requests, err := e.queries.ListCuratorRequests(ctx, repository)
	if err != nil {
		delete(e.curators, repository)
		return a.Fail(context.WithoutCancel(ctx), err)
	}
	key := curatorKey(a.id)
	started := e.startWorker(key, func(ctx context.Context) {
		defer e.stop(key)
		if err := e.curate(ctx, a, requests); err != nil {
			log.Printf("Curator %d of %s: %v", a.id, repository, err)
		}
		e.curatorsMu.Lock()
		defer e.curatorsMu.Unlock()
		if !e.curators[repository] || ctx.Err() != nil {
			delete(e.curators, repository)
			return
		}
		e.curators[repository] = false
		if err := e.runCurator(ctx, repository); err != nil {
			log.Printf("start the next Curator of %s: %v", repository, err)
		}
	})
	if !started {
		delete(e.curators, repository)
		return errors.Join(refuse("Mobius stops, so no Curator starts now."), a.End(context.WithoutCancel(ctx), "declined"))
	}
	return nil
}

// curate runs the session a of the Curator in a worktree that is detached at the default branch. When requests of the
// Owner are not empty, the Triager chat gets the result.
func (e *Engine) curate(ctx context.Context, a *Agent, requests []store.ListCuratorRequestsRow) error {
	repository := a.spec.Repository
	newest, err := e.queries.GetNewestMemoryVersionID(ctx, repository)
	if err != nil {
		return errors.Join(a.Fail(context.WithoutCancel(ctx), err), e.answerRequests(ctx, a, requests, newest, "The Curator failed: "+err.Error()))
	}
	if err := a.waitForSlot(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return errors.Join(err, e.answerRequests(ctx, a, requests, newest, "The Curator failed: "+err.Error()))
	}
	a.spec.Dir = runner.CuratorDir(e.config.DataDir, repository, a.id)
	err = e.curateTurn(ctx, a, requests)
	a.closeHarness()
	ended := context.WithoutCancel(ctx)
	err = errors.Join(err, e.removeCopy(ended, repository, a.spec.Dir))
	var endErr error
	var failure string
	switch {
	case ctx.Err() != nil:
		return a.End(ended, "stopped")
	case errors.Is(err, errHung):
		endErr, failure = a.endHung(ended), "The Curator hung: "+err.Error()
	case err != nil:
		endErr, failure = a.Fail(ended, err), "The Curator failed: "+err.Error()
	default:
		endErr = a.End(ended, "done")
	}
	return errors.Join(endErr, e.answerRequests(ended, a, requests, newest, failure))
}

// answerRequests gives the result of the session a of the Curator on requests to the Triager chat, and then removes the
// requests from the store. When the delivery fails, the requests stay, so the next Curator or the next run of Mobius
// answers them. A stop of Mobius does not call it, so the next run of Mobius starts a Curator for them.
func (e *Engine) answerRequests(ctx context.Context, a *Agent, requests []store.ListCuratorRequestsRow, newest int64, failure string) error {
	if len(requests) == 0 {
		return nil
	}
	if err := e.deliverCuratorResult(ctx, a, requests, newest, failure); err != nil {
		return err
	}
	return e.queries.DeleteCuratorRequestsUpTo(ctx, store.DeleteCuratorRequestsUpToParams{Repository: a.spec.Repository, ID: requests[len(requests)-1].ID})
}

// deliverCuratorResult gives the result of the session a of the Curator on requests to the Triager chat as a Curator
// message. newest is the newest version of the memory file before the session. failure is "" when the session is done.
func (e *Engine) deliverCuratorResult(ctx context.Context, a *Agent, requests []store.ListCuratorRequestsRow, newest int64, failure string) error {
	reasons, err := e.queries.ListCuratorReasonsAfter(ctx, store.ListCuratorReasonsAfterParams{Repository: a.spec.Repository, ID: newest})
	if err != nil {
		return err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Result of the Curator %d on the requests of the Owner:\n\n", a.id)
	for _, request := range requests {
		fmt.Fprintf(&text, "- %s\n", request.Text)
	}
	if len(reasons) == 0 {
		text.WriteString("\nThe Curator did not change the memory file.\n")
	} else {
		text.WriteString("\nThe Curator changed the memory file. The reasons of the changes:\n\n")
		for _, reason := range reasons {
			fmt.Fprintf(&text, "- %s\n", reason)
		}
	}
	if failure != "" {
		fmt.Fprintf(&text, "\n%s\n", failure)
	}
	if reply := strings.TrimSpace(a.replyText()); reply != "" {
		fmt.Fprintf(&text, "\nReply of the Curator:\n\n%s", reply)
	}
	return e.postChat(ctx, ChatKey{Organization: a.spec.Organization}, curatorAuthor, text.String(), nil)
}

// curateTurn makes the worktree of the Curator a and runs its turn.
func (e *Engine) curateTurn(ctx context.Context, a *Agent, requests []store.ListCuratorRequestsRow) error {
	repository, err := e.repository(a.spec.Repository)
	if err != nil {
		return err
	}
	if err := e.addCopy(ctx, repository, a.spec.Dir); err != nil {
		return err
	}
	sections, err := e.repositorySections(ctx, repository, CuratorRole)
	if err != nil {
		return err
	}
	since, err := e.lastDoneCuratorStart(ctx, repository.FullName)
	if err != nil {
		return err
	}
	leads, err := e.leadMemories(repository.FullName, since)
	if err != nil {
		return err
	}
	problems, err := e.sessionProblems(ctx, repository.FullName, since)
	if err != nil {
		return err
	}
	versions, err := e.memoryHistory(ctx, repository.FullName)
	if err != nil {
		return err
	}
	if err := a.open(ctx); err != nil {
		return err
	}
	return a.Prompt(ctx, curatorPrompt+"\n"+requestsSection(requests)+sections+versions+problems+leads, nil)
}

// requestsSection gives the requests of the Owner as a prompt section, or "" for no request.
func requestsSection(requests []store.ListCuratorRequestsRow) string {
	if len(requests) == 0 {
		return ""
	}
	var section strings.Builder
	section.WriteString("# Requests of the Owner\n\n")
	for i, request := range requests {
		fmt.Fprintf(&section, "## Request %d\n\n%s\n\n", i+1, request.Text)
	}
	return section.String()
}

// memoryHistory gives the last versions of the memory file of repository as a prompt section, newest first.
func (e *Engine) memoryHistory(ctx context.Context, repository string) (string, error) {
	versions, err := e.queries.ListRecentMemoryVersions(ctx, repository)
	if err != nil || len(versions) == 0 {
		return "", err
	}
	var section strings.Builder
	section.WriteString("# Last versions of the memory file\n\nThe time, the author and the reason of each version, newest first.\n\n")
	for _, version := range versions {
		fmt.Fprintf(&section, "- %s, %s: %s\n", version.Time, version.Author, version.Reason)
	}
	section.WriteString("\n")
	return section.String(), nil
}

var notAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]`)

// claudeMemoryDir gives the directory of the Claude Code memory of the Lead with the directory leadDir.
func claudeMemoryDir(home, leadDir string) string {
	return filepath.Join(home, ".claude", "projects", notAlphanumeric.ReplaceAllString(leadDir, "-"), "memory")
}

// lastDoneCuratorStart gives the start of the last Curator session of repository that ended "done". With no such
// session, it gives the zero time.
func (e *Engine) lastDoneCuratorStart(ctx context.Context, repository string) (time.Time, error) {
	started, err := e.queries.GetLastDoneCuratorStart(ctx, repository)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, started)
}

// leadMemories gives the .md files of each Lead of repository as prompt sections. A Lead has a section only when one
// of its files changed after since.
func (e *Engine) leadMemories(repository string, since time.Time) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	leads := filepath.Join(e.config.DataDir, "leads", repository)
	entries, err := os.ReadDir(leads)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var sections strings.Builder
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(leads, entry.Name())
		var files strings.Builder
		changed := false
		for _, notes := range []string{dir, claudeMemoryDir(home, dir)} {
			text, newer, err := readNotes(notes, since)
			if err != nil {
				return "", err
			}
			changed = changed || newer
			files.WriteString(text)
		}
		if changed && files.Len() > 0 {
			fmt.Fprintf(&sections, "# Notes of the Lead of Workstream %s\n\n%s", entry.Name(), files.String())
		}
	}
	return sections.String(), nil
}

// readNotes gives the .md files of dir as prompt subsections, in the order of their names, and tells if a file was
// modified after since. A missing dir has no file.
func readNotes(dir string, since time.Time) (string, bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var sections strings.Builder
	changed := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return "", false, err
		}
		changed = changed || info.ModTime().After(since)
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return "", false, err
		}
		if text := strings.TrimSpace(string(data)); text != "" {
			fmt.Fprintf(&sections, "## %s\n\n%s\n\n", path, text)
		}
	}
	return sections.String(), changed, nil
}

// editMemory replaces the one occurrence of input.Old in the memory file of the repository of c with input.New. An
// empty input.Old adds input.New at the end of the file. It saves input.Reason in the version.
func (e *Engine) editMemory(ctx context.Context, c caller, _ github.Repository, input editMemoryInput) (string, error) {
	if strings.TrimSpace(input.Reason) == "" {
		return "", refuse("The reason is empty. Name the type of change and the evidence.")
	}
	e.memoryMu.Lock()
	defer e.memoryMu.Unlock()
	text, err := e.readMemory(c.repository)
	if err != nil {
		return "", err
	}
	if input.Old == "" {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += input.New
	} else {
		switch count := strings.Count(text, input.Old); count {
		case 0:
			return "", refuse("The old text does not occur in the memory file.")
		case 1:
		default:
			return "", refuse("The old text occurs %d times in the memory file. Make it longer, so that it occurs one time.", count)
		}
		text = strings.Replace(text, input.Old, input.New, 1)
	}
	if err := e.saveMemory(ctx, c.repository, "curator", input.Reason, text); err != nil {
		return "", err
	}
	return fmt.Sprintf("Saved the memory file. It has %d of %d lines.", countLines(text), maxMemoryLines), nil
}
