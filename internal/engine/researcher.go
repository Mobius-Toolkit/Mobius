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

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

//go:embed prompts/researcher.md
var researcherPrompt string

// researcherKey is the key in e.stops of the Worker of the Researcher with this session id.
type researcherKey int64

type researcherInput struct {
	ID int64 `json:"id"`
}

type researcherTextInput struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

// startResearcher starts a Researcher with the question of the Lead or of the Triager chat c. The report goes to the
// chat of c as a Researcher message. A stop of the Lead stops its Researchers with no report. It gives the id of the
// Researcher.
func (e *Engine) startResearcher(ctx context.Context, c caller, repository github.Repository, input questionInput) (string, error) {
	if c.role == TriagerRole && c.repository != "" {
		return "", refuse("Only the Triager chat or the Lead chat starts a Researcher.")
	}
	if empty(input.Question) {
		return "", refuse("question must not be empty.")
	}
	if e.draining() {
		return "", refuse("Mobius prepares an upgrade, so no Researcher starts now.")
	}
	a, err := e.newAgent(ctx, Spec{
		Role:         ResearcherRole,
		Organization: c.organization,
		Repository:   repository.FullName,
		Workstream:   c.workstream,
		Parent:       sql.NullInt64{Int64: c.session, Valid: true},
	})
	if err != nil {
		return "", err
	}
	key := researcherKey(a.id)
	e.detailsMu.Lock()
	e.researchers[a.id] = a
	e.detailsMu.Unlock()
	started := e.startWorker(key, func(ctx context.Context) {
		defer e.stop(key)
		defer func() {
			e.detailsMu.Lock()
			delete(e.researchers, a.id)
			e.detailsMu.Unlock()
		}()
		if err := e.research(ctx, c, repository, a, input.Question); err != nil {
			log.Printf("Researcher %d of %s#%d: %v", a.id, c.repository, c.workstream, err)
		}
	})
	if !started {
		e.detailsMu.Lock()
		delete(e.researchers, a.id)
		e.detailsMu.Unlock()
		return "", errors.Join(refuse("Mobius stops, so no Researcher starts now."), a.End(context.WithoutCancel(ctx), "declined"))
	}
	return fmt.Sprintf("Started the Researcher %d. The report arrives later.", a.id), nil
}

// ownedResearcher gives the Researcher id that runs for the Workstream of c, or nil.
func (e *Engine) ownedResearcher(c caller, id int64) *Agent {
	e.detailsMu.Lock()
	defer e.detailsMu.Unlock()
	return e.ownedResearcherLocked(c, id)
}

// ownedResearcherLocked is ownedResearcher for a caller that holds detailsMu.
func (e *Engine) ownedResearcherLocked(c caller, id int64) *Agent {
	a, ok := e.researchers[id]
	if !ok || a.spec.Repository != c.repository || a.spec.Workstream != c.workstream {
		return nil
	}
	return a
}

// sendResearcherDetails gives new details of the Owner to the Researcher of the input. A turn that runs ends, and the
// details go to the agent as the next prompt.
func (e *Engine) sendResearcherDetails(_ context.Context, c caller, _ github.Repository, input researcherTextInput) (string, error) {
	if empty(input.Text) {
		return "", refuse("text must not be empty.")
	}
	if e.ownedResearcher(c, input.ID) == nil || !e.addDetails(e.researchers, input.ID, input.Text) {
		return "", refuse("No Researcher %d of this Workstream runs now.", input.ID)
	}
	return fmt.Sprintf("Sent the details to the Researcher %d.", input.ID), nil
}

// stopResearcher stops the Researcher of the input with no report. The Lead chat gets a message that tells the stop.
// The Researcher leaves e.researchers and gets the stop in one step, so it either gives its report or stops.
func (e *Engine) stopResearcher(ctx context.Context, c caller, _ github.Repository, input researcherInput) (string, error) {
	e.detailsMu.Lock()
	if e.ownedResearcherLocked(c, input.ID) == nil {
		e.detailsMu.Unlock()
		return "", refuse("No Researcher %d of this Workstream runs now.", input.ID)
	}
	delete(e.researchers, input.ID)
	e.stop(researcherKey(input.ID))
	e.detailsMu.Unlock()
	text := fmt.Sprintf("The Researcher %d stopped. No report arrives.", input.ID)
	if err := e.postChat(ctx, leadChat(c.repository, c.workstream), researcherAuthor, text, nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("Stopped the Researcher %d.", input.ID), nil
}

// stopResearchers stops each Researcher of the Workstream with no report.
func (e *Engine) stopResearchers(repository string, workstream int64) {
	e.detailsMu.Lock()
	var ids []int64
	for id, a := range e.researchers {
		if a.spec.Repository == repository && a.spec.Workstream == workstream {
			ids = append(ids, id)
		}
	}
	e.detailsMu.Unlock()
	for _, id := range ids {
		e.stop(researcherKey(id))
	}
}

// research runs the session a of the Researcher on the question in a worktree that is detached at the default branch.
// A failed Researcher reports its error.
func (e *Engine) research(ctx context.Context, c caller, repository github.Repository, a *Agent, question string) error {
	if err := a.waitForSlot(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	a.spec.Dir = runner.ResearchDir(e.config.DataDir, repository.FullName, a.id)
	report, err := e.researchTurn(ctx, a, question)
	a.closeHarness()
	stopped := e.leave(ctx, a.id)
	ended := context.WithoutCancel(ctx)
	e.gitMu.Lock()
	if _, statErr := os.Stat(a.spec.Dir); !errors.Is(statErr, fs.ErrNotExist) {
		err = errors.Join(err, runner.RemoveWorktree(ended, e.config.DataDir, repository.FullName, a.spec.Dir))
	}
	e.gitMu.Unlock()
	switch {
	case stopped:
		return a.End(ended, "stopped")
	case errors.Is(err, errHung):
		return errors.Join(a.endHung(ended), e.deliverReport(ended, c, a.id, question, "The Researcher failed: "+err.Error()))
	case err != nil:
		failure := a.Fail(ended, err)
		return errors.Join(failure, e.deliverReport(ended, c, a.id, question, "The Researcher failed: "+err.Error()))
	}
	if err := a.End(ended, "done"); err != nil {
		return err
	}
	return e.deliverReport(ended, c, a.id, question, report)
}

// leave removes the Researcher id from e.researchers. It tells if a stop came before, as ctx shows.
func (e *Engine) leave(ctx context.Context, id int64) bool {
	e.detailsMu.Lock()
	defer e.detailsMu.Unlock()
	delete(e.researchers, id)
	return ctx.Err() != nil
}

// researchTurn makes the worktree of the Researcher a and runs its turn. It gives the report: the reply text after the
// last tool call.
func (e *Engine) researchTurn(ctx context.Context, a *Agent, question string) (string, error) {
	repository, err := e.repository(a.spec.Repository)
	if err != nil {
		return "", err
	}
	token, err := repository.Token(ctx)
	if err != nil {
		return "", err
	}
	e.gitMu.Lock()
	err = runner.Fetch(ctx, e.config.DataDir, repository.FullName, repository.CloneURL, token)
	if err == nil {
		err = runner.AddDetachedWorktree(ctx, e.config.DataDir, repository.FullName, a.spec.Dir, "origin/"+repository.DefaultBranch)
	}
	e.gitMu.Unlock()
	if err != nil {
		return "", err
	}
	briefSection := ""
	if a.spec.Workstream != 0 {
		brief, err := brief(ctx, repository, a.spec.Workstream)
		if err != nil {
			return "", err
		}
		briefSection = "# Brief\n\n" + brief + "\n\n"
	}
	sections, err := e.repositorySections(ctx, repository, ResearcherRole)
	if err != nil {
		return "", err
	}
	if err := a.open(ctx); err != nil {
		return "", err
	}
	prompt := fmt.Sprintf("%s\n%s%s# Question\n\n%s", researcherPrompt, sections, briefSection, question)
	for {
		err := a.Prompt(ctx, prompt, nil)
		if details := e.takeDetails(e.researchers, a.id, a, true); details != "" && ctx.Err() == nil {
			prompt = detailsPrompt("question", details)
			continue
		}
		return a.replyText(), err
	}
}

// deliverReport gives the report of the Researcher id on the question to the chat of c as a Researcher message.
func (e *Engine) deliverReport(ctx context.Context, c caller, id int64, question, report string) error {
	text := fmt.Sprintf("Report of the Researcher %d on \"%s\":\n\n%s", id, question, report)
	return e.postChat(ctx, ChatKey{c.organization, c.repository, c.workstream}, researcherAuthor, text, nil)
}
