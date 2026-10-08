package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

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

// triage starts the Triager of the issue number, which has no Workstream: the issue goes from mobius:ready to
// mobius:no-workstream. It gives false while the drain is on, and then the issue keeps mobius:ready.
func (e *Engine) triage(ctx context.Context, repository github.Repository, number int64) (bool, error) {
	if !e.tryTrack() {
		return false, nil
	}
	err := repository.AddLabel(ctx, number, noWorkstreamLabel)
	if err == nil {
		err = repository.RemoveLabel(ctx, number, readyLabel)
	}
	if err != nil {
		e.untrack()
		return false, err
	}
	e.startTriager(repository, number)
	return true, nil
}

// retriage starts the Triager of the issue again after the comments, when the issue is open, has mobius:no-workstream,
// and a comment is an event.
func (e *Engine) retriage(ctx context.Context, repository github.Repository, issue *gh.Issue, comments []*gh.IssueComment) error {
	if issue.GetState() != "open" || !hasLabel(issue, noWorkstreamLabel) ||
		!slices.ContainsFunc(comments, func(comment *gh.IssueComment) bool { return e.commentIsEvent(repository.AppSlug, comment) }) {
		return nil
	}
	return e.restartTriager(ctx, repository, int64(issue.GetNumber()))
}

// restartTriager starts the Triager of the issue number again. A running Triager stops first, so the new run reads the
// comments again. While the drain is on, the database keeps the issue for startHeldTriagers.
func (e *Engine) restartTriager(ctx context.Context, repository github.Repository, number int64) error {
	if !e.tryTrack() {
		return e.queries.HoldTriager(ctx, store.HoldTriagerParams{Repository: repository.FullName, Issue: number})
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
	}
	e.startTriager(repository, number)
	return nil
}

// startHeldTriagers starts the Triagers that the drain held, when the issue is still open and has
// mobius:no-workstream. It starts none while the drain is on.
func (e *Engine) startHeldTriagers(ctx context.Context, repository github.Repository) error {
	if e.draining() {
		return nil
	}
	numbers, err := e.queries.ListHeldTriagers(ctx, repository.FullName)
	if err != nil {
		return err
	}
	for _, number := range numbers {
		issue, err := repository.Issue(ctx, number)
		if err != nil {
			return err
		}
		if err := e.queries.ReleaseTriager(ctx, store.ReleaseTriagerParams{Repository: repository.FullName, Issue: number}); err != nil {
			return err
		}
		if issue == nil || issue.GetState() != "open" || !hasLabel(issue, noWorkstreamLabel) {
			continue
		}
		if err := e.restartTriager(ctx, repository, number); err != nil {
			return err
		}
	}
	return nil
}

// startTriager runs the Triager of the issue number in a goroutine. The caller holds a tryTrack.
func (e *Engine) startTriager(repository github.Repository, number int64) {
	triageCtx, stop := context.WithCancel(context.Background())
	key := triageKey{repository.FullName, number}
	running := triage{stop, make(chan struct{})}
	e.triagesMu.Lock()
	e.triages[key] = running
	e.triagesMu.Unlock()
	go func() {
		defer close(running.done)
		if err := e.runTriager(triageCtx, repository, number); err != nil {
			log.Printf("Triager of %s#%d: %v", repository.FullName, number, err)
		}
		e.triagesMu.Lock()
		delete(e.triages, key)
		e.triagesMu.Unlock()
		stop()
	}()
}

// runTriager runs the Triager session of the issue number. When the issue keeps mobius:no-workstream, the last text of
// the Triager goes to the issue as its proposal. The end of ctx stops the session.
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
	issue, err := issueOf(ctx, repository, number)
	if err != nil {
		return a.Fail(ended, err)
	}
	workstreams, err := openWorkstreams(ctx, []github.Repository{repository})
	if err != nil {
		return a.Fail(ended, err)
	}
	comments, err := repository.Comments(ctx, number)
	if err != nil {
		return a.Fail(ended, err)
	}
	prompt := fmt.Sprintf("%s\n%s\n# Issue\n\n#%d %s\n\n%s", triagerPrompt, workstreams, number, issue.GetTitle(), issue.GetBody())
	comments = slices.DeleteFunc(comments, func(comment *gh.IssueComment) bool {
		return !e.TrustedAuthor(repository.AppSlug, comment.GetUser().GetLogin())
	})
	if len(comments) > 0 {
		prompt += "\n\n# Comments\n"
		for _, comment := range comments {
			prompt += entry(comment.GetUser().GetLogin(), comment.GetCreatedAt().Time, "", comment.GetBody())
		}
	}
	err = a.Prompt(ctx, prompt, nil)
	switch {
	case ctx.Err() != nil:
		return a.End(ended, "stopped")
	case errors.Is(err, errHung):
		return a.endHung(ended)
	case err != nil:
		return a.Fail(ended, err)
	}
	proposal := a.replyText()
	if err := a.End(ended, "done"); err != nil {
		return err
	}
	if empty(proposal) {
		return nil
	}
	issue, err = repository.Issue(ctx, number)
	if err != nil || issue == nil || !hasLabel(issue, noWorkstreamLabel) {
		return err
	}
	_, err = repository.AddComment(ctx, number, proposal)
	return err
}

// stopTriager stops the Triager of the issue number when a person removed mobius:no-workstream last. Mobius ignores
// its own removal of that label, for example in move_issue.
func (e *Engine) stopTriager(ctx context.Context, repository github.Repository, number int64) error {
	key := triageKey{repository.FullName, number}
	e.triagesMu.Lock()
	_, running := e.triages[key]
	e.triagesMu.Unlock()
	if !running {
		return nil
	}
	events, err := repository.IssueEvents(ctx, number)
	if err != nil {
		return err
	}
	for _, event := range slices.Backward(events) {
		if event.GetEvent() != "unlabeled" || event.GetLabel().GetName() != noWorkstreamLabel {
			continue
		}
		if strings.EqualFold(event.GetActor().GetLogin(), appLogin(repository.AppSlug)) {
			return nil
		}
		e.triagesMu.Lock()
		if running, ok := e.triages[key]; ok {
			running.stop()
		}
		e.triagesMu.Unlock()
		return nil
	}
	return nil
}

// organizationRepositories gives the repositories of the organization.
func (e *Engine) organizationRepositories(organization string) []github.Repository {
	return slices.DeleteFunc(e.github.Repositories(), func(repository github.Repository) bool { return repository.Owner() != organization })
}

// openWorkstreams gives the prompt section of the open Workstreams of the repositories.
func openWorkstreams(ctx context.Context, repositories []github.Repository) (string, error) {
	var text strings.Builder
	text.WriteString("# Open Workstreams\n")
	for _, repository := range repositories {
		issues, err := repository.OpenIssuesWithLabel(ctx, workstreamLabel)
		if err != nil {
			return "", err
		}
		for _, issue := range issues {
			fmt.Fprintf(&text, "\n#%d %s (%s)\n\n%s\n", issue.GetNumber(), issue.GetTitle(), repository.FullName, issue.GetBody())
		}
	}
	return text.String(), nil
}

// appLogin gives the login of the bot of the Mobius App appSlug.
func appLogin(appSlug string) string {
	return appSlug + "[bot]"
}
