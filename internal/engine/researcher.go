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

// startResearcher starts a Researcher with the question of the Lead or of the Triager chat c. The report goes to the
// chat of c as a Researcher message. A stop of the Lead stops the Researcher with no report.
func (e *Engine) startResearcher(_ context.Context, c caller, repository github.Repository, input questionInput) (string, error) {
	if c.role == TriagerRole && c.repository != "" {
		return "", refuse("Only the Triager chat or the Lead chat starts a Researcher.")
	}
	if empty(input.Question) {
		return "", refuse("question must not be empty.")
	}
	if e.draining() {
		return "", refuse("Mobius prepares an upgrade, so no Researcher starts now.")
	}
	e.startWorker(ChatKey{c.organization, c.repository, c.workstream}, func(ctx context.Context) {
		if err := e.research(ctx, c, repository, input.Question); err != nil {
			log.Printf("Researcher of %s#%d: %v", c.repository, c.workstream, err)
		}
	})
	return "Started a Researcher. The report arrives later.", nil
}

// research runs the Researcher session of the question in a worktree that is detached at the default branch. A failed
// Researcher reports its error.
func (e *Engine) research(ctx context.Context, c caller, repository github.Repository, question string) error {
	a, err := e.addAgent(ctx, Spec{
		Role:         ResearcherRole,
		Organization: c.organization,
		Repository:   repository.FullName,
		Workstream:   c.workstream,
		Parent:       sql.NullInt64{Int64: c.session, Valid: true},
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	a.spec.Dir = runner.ResearchDir(e.config.DataDir, repository.FullName, a.id)
	report, err := e.researchTurn(ctx, a, question)
	a.closeHarness()
	ended := context.WithoutCancel(ctx)
	e.gitMu.Lock()
	if _, statErr := os.Stat(a.spec.Dir); !errors.Is(statErr, fs.ErrNotExist) {
		err = errors.Join(err, runner.RemoveWorktree(ended, e.config.DataDir, repository.FullName, a.spec.Dir))
	}
	e.gitMu.Unlock()
	switch {
	case ctx.Err() != nil:
		return a.End(ended, "stopped")
	case err != nil:
		failure := a.Fail(ended, err)
		return errors.Join(failure, e.deliverReport(ended, c, question, "The Researcher failed: "+err.Error()))
	}
	if err := a.End(ended, "done"); err != nil {
		return err
	}
	return e.deliverReport(ended, c, question, report)
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
	if err := a.Prompt(ctx, fmt.Sprintf("%s\n%s%s# Question\n\n%s", researcherPrompt, sections, briefSection, question)); err != nil {
		return "", err
	}
	return a.replyText(), nil
}

// deliverReport gives the report on the question to the chat of c as a Researcher message.
func (e *Engine) deliverReport(ctx context.Context, c caller, question, report string) error {
	text := fmt.Sprintf("Report of the Researcher on \"%s\":\n\n%s", question, report)
	return e.postChat(ctx, ChatKey{c.organization, c.repository, c.workstream}, researcherAuthor, text)
}
