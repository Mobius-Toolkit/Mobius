package engine

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

//go:embed prompts/triager.md
var triagerPrompt string

// triageKey names the Triager session of one issue.
type triageKey struct {
	repository string
	number     int64
}

// triage is a running Triager of an issue.
type triage struct {
	stop context.CancelFunc
	// done closes when the Triager ends.
	done chan struct{}
}

// startTriage sets the triage state of the open issue, which has no Workstream, when a trusted user created the issue at
// or after since. The triage handler starts the first turn of the Triager. The first poll of a repository, with no
// issues row in the cursors, starts no Triager. It adds the reaction of an agent to the comments, because the first
// turn reads them. It gives true when it set the triage state.
func (e *Engine) startTriage(ctx context.Context, repository github.Repository, issue *gh.Issue, comments []*gh.IssueComment, since time.Time, firstPoll bool) (bool, error) {
	if issue.IsPullRequest() || hasLabel(issue, bucketLabel) || issue.GetState() != "open" || firstPoll ||
		issue.GetCreatedAt().Before(since) || !e.trustedUser(issue.GetUser().GetLogin()) {
		return false, nil
	}
	number := int64(issue.GetNumber())
	_, workstream, err := e.commentWorkstream(ctx, repository, issue)
	if err != nil || workstream != 0 {
		return false, err
	}
	changed, err := e.setTriage(ctx, repository, number, true)
	if err != nil || !changed {
		return false, err
	}
	replies, _, err := e.replies(ctx, repository, number, comments)
	if err != nil {
		return false, err
	}
	return true, acknowledge(ctx, repository, replies)
}

// retriage starts the Triager of the open issue with the triage state and no Workstream again after the comments of
// events, and adds the reaction of an agent that gets them. The Triager reads the comments of the issue.
func (e *Engine) retriage(ctx context.Context, repository github.Repository, issue *gh.Issue, events []*gh.IssueComment) error {
	if err := acknowledge(ctx, repository, events); err != nil {
		return err
	}
	return e.restartTriager(ctx, repository, int64(issue.GetNumber()))
}

// afterTriagerStop is a hook of the tests. restartTriager calls it after a stopped Triager ended, with the start lock of
// its issue held.
var afterTriagerStop func()

// triagerStart gives the start lock of the issue key.
func (e *Engine) triagerStart(key triageKey) *sync.Mutex {
	e.triagesMu.Lock()
	defer e.triagesMu.Unlock()
	if e.triagerStarts[key] == nil {
		e.triagerStarts[key] = &sync.Mutex{}
	}
	return e.triagerStarts[key]
}

// restartTriager starts the Triager of the issue number again. A running Triager stops first, so the new run reads the
// comments again. The database keeps the issue until the turn ends, so startHeldTriagers starts the turn again when the
// drain or a restart of Mobius stops it.
func (e *Engine) restartTriager(ctx context.Context, repository github.Repository, number int64) error {
	start := e.triagerStart(triageKey{repository.FullName, number})
	start.Lock()
	defer start.Unlock()
	return e.restartTriagerLocked(ctx, repository, number)
}

// restartTriagerLocked is restartTriager for a caller that holds the start lock of the issue.
func (e *Engine) restartTriagerLocked(ctx context.Context, repository github.Repository, number int64) error {
	turn := store.AddTriagerTurnParams{Repository: repository.FullName, Issue: number}
	if !e.tryTrack() {
		return e.queries.AddTriagerTurn(ctx, turn)
	}
	e.triagesMu.Lock()
	running, ok := e.triages[triageKey{repository.FullName, number}]
	e.triagesMu.Unlock()
	if ok {
		running.stop()
		select {
		case <-running.done:
		case <-ctx.Done():
			e.untrack()
			return ctx.Err()
		}
		if afterTriagerStop != nil {
			afterTriagerStop()
		}
	}
	if err := e.queries.AddTriagerTurn(ctx, turn); err != nil {
		e.untrack()
		return err
	}
	e.startTriager(repository, number)
	return nil
}

// restartHeldTriager starts the Triager of the held turn number. It starts none while another restart of the same issue
// runs, because that restart starts the Triager, and none when the Triager runs or its turn ended while the caller read
// GitHub.
func (e *Engine) restartHeldTriager(ctx context.Context, repository github.Repository, number int64) error {
	key := triageKey{repository.FullName, number}
	start := e.triagerStart(key)
	if !start.TryLock() {
		return nil
	}
	defer start.Unlock()
	e.triagesMu.Lock()
	_, running := e.triages[key]
	e.triagesMu.Unlock()
	if running {
		return nil
	}
	held, err := e.queries.ListTriagerTurns(ctx, repository.FullName)
	if err != nil || !slices.Contains(held, number) {
		return err
	}
	return e.restartTriagerLocked(ctx, repository, number)
}

// startHeldTriagers starts the turns that did not end and do not run, when the issue is still open, has the triage state
// and has no Workstream. For another issue, it ends the turn. It starts none while the drain is on. It keeps the issue
// held while a comment write of the issue waits, because that comment can be the report of a Researcher.
func (e *Engine) startHeldTriagers(ctx context.Context, repository github.Repository) error {
	if e.draining() {
		return nil
	}
	e.triagesMu.Lock()
	numbers, err := e.queries.ListTriagerTurns(ctx, repository.FullName)
	numbers = slices.DeleteFunc(numbers, func(number int64) bool {
		_, running := e.triages[triageKey{repository.FullName, number}]
		return running
	})
	e.triagesMu.Unlock()
	if err != nil {
		return err
	}
	changes, err := e.queries.ListPendingOutgoingChanges(ctx, repository.FullName)
	if err != nil {
		return err
	}
	for _, number := range numbers {
		if slices.ContainsFunc(changes, func(change store.OutgoingChange) bool { return change.Number.Int64 == number }) {
			continue
		}
		issue, err := repository.Issue(ctx, number)
		if err != nil {
			return err
		}
		triaging, err := e.triaging(ctx, repository, issue)
		if err != nil {
			return err
		}
		if !triaging {
			if err := e.queries.EndTriagerTurn(ctx, store.EndTriagerTurnParams{Repository: repository.FullName, Issue: number}); err != nil {
				return err
			}
			continue
		}
		if err := e.restartHeldTriager(ctx, repository, number); err != nil {
			return err
		}
	}
	return nil
}

// triaging tells if the issue is open, is no bucket, and has the triage state and no Workstream in issues.
func (e *Engine) triaging(ctx context.Context, repository github.Repository, issue *gh.Issue) (bool, error) {
	if issue == nil || issue.GetState() != "open" || hasLabel(issue, bucketLabel) {
		return false, nil
	}
	return e.queries.IsTriaging(ctx, store.IsTriagingParams{Repository: repository.FullName, Number: int64(issue.GetNumber())})
}

// startTriager runs the Triager of the issue number in a goroutine. The caller holds a tryTrack. A turn that ends with
// no stop ends in the database. A turn that a stop ends stays there.
func (e *Engine) startTriager(repository github.Repository, number int64) {
	triageCtx, stop := context.WithCancel(context.Background())
	key := triageKey{repository.FullName, number}
	running := triage{stop, make(chan struct{})}
	e.triagesMu.Lock()
	e.triages[key] = running
	e.triagesMu.Unlock()
	go func() {
		defer close(running.done)
		var err error
		for {
			err = e.runTriager(triageCtx, repository, number)
			if !errors.Is(err, errMoved) {
				break
			}
			if triageCtx.Err() != nil || !e.tryTrack() {
				break
			}
		}
		moved := errors.Is(err, errMoved)
		if err != nil && !moved {
			log.Printf("Triager of %s#%d: %v", repository.FullName, number, err)
		}
		e.triagesMu.Lock()
		if triageCtx.Err() == nil && !moved {
			if err := e.queries.EndTriagerTurn(context.Background(), store.EndTriagerTurnParams{Repository: repository.FullName, Issue: number}); err != nil {
				log.Printf("Triager of %s#%d: %v", repository.FullName, number, err)
			}
		}
		delete(e.triages, key)
		e.triagesMu.Unlock()
		stop()
	}()
}

// runTriager runs one turn of the Triager session of the issue number. When the issue is open, keeps mobius:triage and
// has no Workstream, the last text of the Triager goes to the issue as a comment. The end of ctx stops the session.
func (e *Engine) runTriager(ctx context.Context, repository github.Repository, number int64) error {
	a, err := e.Start(ctx, Spec{
		Role:         TriagerRole,
		Organization: repository.Owner(),
		Repository:   repository.FullName,
		Issue:        sql.NullInt64{Int64: number, Valid: true},
		Tracked:      true,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	ended := context.WithoutCancel(ctx)
	fail := func(err error) error {
		if ctx.Err() != nil {
			return a.End(ended, "stopped")
		}
		return a.Fail(ended, err)
	}
	issue, err := issueOf(ctx, repository, number)
	if err != nil {
		return fail(err)
	}
	if e.trustedUser(issue.GetUser().GetLogin()) {
		err = e.queries.SetTriageIssueText(ctx, store.SetTriageIssueTextParams{Title: issue.GetTitle(), Body: issue.GetBody(), Repository: repository.FullName, Number: number})
		if err != nil {
			return fail(err)
		}
	}
	issueText, err := e.queries.GetIssueText(ctx, store.GetIssueTextParams{Repository: repository.FullName, Number: number})
	if err != nil {
		return fail(err)
	}
	var workstreams string
	if found, err := e.workstreamRepository(ctx, repository.Owner()); err != nil {
		workstreams = fmt.Sprintf("# Open Workstreams\n\nThe tools that use a Workstream refuse: %s\n", err)
	} else if workstreams, err = e.openWorkstreams(ctx, []github.Repository{found}); err != nil {
		return fail(err)
	}
	comments, err := repository.Comments(ctx, number)
	if err != nil {
		return fail(err)
	}
	if err := e.launchTriagerComments(ctx, repository, comments); err != nil {
		return fail(err)
	}
	comments = e.trustedComments(repository, comments)
	over, err := e.closeEndedTriage(ctx, repository, number)
	if err != nil {
		return fail(err)
	}
	if over {
		return a.End(ended, "done")
	}
	prompt := fmt.Sprintf("%s\n%s%s\n# Issue\n\n%s#%d %s\n\n%s", triagerPrompt, serverSection(), workstreams, repository.FullName, number, issueText.Title, issueText.Body)
	if len(comments) > 0 {
		prompt += "\n\n# Comments\n"
		for _, comment := range comments {
			prompt += entry(comment.GetUser().GetLogin(), comment.GetCreatedAt().Time, "", comment.GetBody())
		}
	}
	err = a.Prompt(ctx, prompt, nil)
	if err == nil {
		err = a.waitBackground(ctx, backgroundPrompt)
	}
	switch {
	case ctx.Err() != nil:
		return a.End(ended, "stopped")
	case errors.Is(err, errHung):
		return a.endHung(ended)
	case errors.Is(err, errMoved):
		return errors.Join(errMoved, a.End(ended, "moved"))
	case err != nil:
		return a.Fail(ended, err)
	}
	text := a.replyText()
	if err := a.End(ended, "done"); err != nil {
		return err
	}
	issue, err = repository.Issue(ctx, number)
	if err != nil {
		return err
	}
	triaging, err := e.triaging(ctx, repository, issue)
	if err != nil || !triaging {
		return err
	}
	over, err = e.closeEndedTriage(ctx, repository, number)
	if err != nil || over || empty(text) {
		return err
	}
	_, workstream, err := e.commentWorkstream(ctx, repository, issue)
	if err != nil || workstream != 0 {
		return err
	}
	if text, err = e.withMark(ctx, caller{role: TriagerRole, repository: repository.FullName, issue: number}, repository.FullName, text); err != nil {
		return err
	}
	return e.postComment(ctx, repository, number, text)
}

// trustedComments gives the comments of trusted authors and of the Mobius App.
func (e *Engine) trustedComments(repository github.Repository, comments []*gh.IssueComment) []*gh.IssueComment {
	return slices.DeleteFunc(slices.Clone(comments), func(comment *gh.IssueComment) bool {
		return !e.TrustedAuthor(repository.AppSlug, comment.GetUser().GetLogin())
	})
}

// closeEndedTriage closes the issue number as completed when its triage ended and nobody reopened the issue after the
// end, and tells if it did. A close that failed at the end stays open until this step runs.
func (e *Engine) closeEndedTriage(ctx context.Context, repository github.Repository, number int64) (bool, error) {
	endedAt, err := e.queries.GetIssueTriageEndedAt(ctx, store.GetIssueTriageEndedAtParams{Repository: repository.FullName, Number: number})
	if err != nil || !endedAt.Valid {
		return false, err
	}
	ended, err := time.Parse(time.RFC3339Nano, endedAt.String)
	if err != nil {
		return false, err
	}
	events, err := repository.IssueEvents(ctx, number)
	if err != nil {
		return false, err
	}
	if slices.ContainsFunc(events, func(event *gh.IssueEvent) bool {
		return event.GetEvent() == "reopened" && event.GetCreatedAt().After(ended)
	}) {
		return false, nil
	}
	_, err = repository.CloseIssue(ctx, number, "completed")
	return err == nil, err
}

// stopTriager stops the Triager of the issue number, and ends its turn in the database.
func (e *Engine) stopTriager(ctx context.Context, repository github.Repository, number int64) error {
	e.triagesMu.Lock()
	if running, ok := e.triages[triageKey{repository.FullName, number}]; ok {
		running.stop()
	}
	e.triagesMu.Unlock()
	return e.queries.EndTriagerTurn(ctx, store.EndTriagerTurnParams{Repository: repository.FullName, Issue: number})
}

// organizationRepositories gives the repositories of the organization.
func (e *Engine) organizationRepositories(organization string) []github.Repository {
	return slices.DeleteFunc(e.github.Repositories(), func(repository github.Repository) bool { return repository.Owner() != organization })
}

// openWorkstreams gives the prompt section of the open Workstreams of the repositories, from the local copy.
func (e *Engine) openWorkstreams(ctx context.Context, repositories []github.Repository) (string, error) {
	copied, err := e.queries.ListCopiedWorkstreams(ctx)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	text.WriteString("# Open Workstreams\n")
	for _, repository := range repositories {
		for _, workstream := range copied {
			if workstream.Repository == repository.FullName {
				fmt.Fprintf(&text, "\n#%d %s (%s)\n\n%s\n", workstream.Number, workstream.Title, repository.FullName, workstream.Body)
			}
		}
	}
	return text.String(), nil
}

// appLogin gives the login of the bot of the Mobius App appSlug.
func appLogin(appSlug string) string {
	return appSlug + "[bot]"
}
