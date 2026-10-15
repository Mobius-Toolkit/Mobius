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

// triageEndpoint is the endpoint in the sync_cursors table that holds the time of the last mobius:triage label that
// started a turn of the issue number.
func triageEndpoint(number int64) string {
	return fmt.Sprintf("triage/%d", number)
}

// startTriage starts the first turn of the Triager of the open issue, which has no Workstream, in two cases. A trusted
// user created the issue at or after since. Or the issue has mobius:triage, and a trusted user added that label at or after
// since, and no earlier turn started for that label.
// The first case adds mobius:triage. The first poll of a repository, with no since, starts no Triager. It adds the
// reaction of an agent to the comments, because the first turn reads them. It gives true when it started a turn.
func (e *Engine) startTriage(ctx context.Context, repository github.Repository, issue *gh.Issue, comments []*gh.IssueComment, since time.Time) (bool, error) {
	if issue.IsPullRequest() || issue.GetState() != "open" || since.IsZero() {
		return false, nil
	}
	number := int64(issue.GetNumber())
	labeled := hasLabel(issue, triageLabel)
	labeledAt := ""
	if !labeled && (!e.trustedUser(issue.GetUser().GetLogin()) || issue.GetCreatedAt().Before(since)) {
		return false, nil
	}
	workstream, err := e.commentWorkstream(ctx, repository, issue)
	if err != nil || workstream != 0 {
		return false, err
	}
	if labeled {
		events, err := repository.IssueEvents(ctx, number)
		if err != nil {
			return false, err
		}
		_, event := lastEvent(events, "labeled", triageLabel)
		if event != nil {
			labeledAt = event.GetCreatedAt().UTC().Format(time.RFC3339)
		}
		if event == nil || event.GetCreatedAt().Before(since) || !e.trustedUser(event.GetActor().GetLogin()) {
			return false, nil
		}
		cursor, err := e.queries.GetSyncCursor(ctx, store.GetSyncCursorParams{Repository: repository.FullName, Endpoint: triageEndpoint(number)})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if cursor.Since.String == labeledAt {
			return false, nil
		}
	} else if err := repository.AddLabel(ctx, number, triageLabel); err != nil {
		return false, err
	}
	replies, _ := e.replies(repository.AppSlug, comments)
	if err := acknowledge(ctx, repository, replies); err != nil {
		return false, err
	}
	if err := e.restartTriager(ctx, repository, number); err != nil {
		return false, err
	}
	if labeledAt == "" {
		return true, nil
	}
	return true, e.queries.SetSyncCursor(ctx, store.SetSyncCursorParams{Repository: repository.FullName, Endpoint: triageEndpoint(number), Since: sql.NullString{String: labeledAt, Valid: true}})
}

// retriage starts the Triager of the open issue with mobius:triage and no Workstream again after the comments of events,
// and adds the reaction of an agent that gets them. The Triager reads the comments of the issue.
func (e *Engine) retriage(ctx context.Context, repository github.Repository, issue *gh.Issue, events []*gh.IssueComment) error {
	if err := acknowledge(ctx, repository, events); err != nil {
		return err
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

// startHeldTriagers starts the Triagers that the drain held, when the issue is still open, has mobius:triage and has no
// Workstream. It starts none while the drain is on.
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
		triaging, err := e.triaging(ctx, repository, issue)
		if err != nil {
			return err
		}
		if !triaging {
			continue
		}
		if err := e.restartTriager(ctx, repository, number); err != nil {
			return err
		}
	}
	return nil
}

// triaging tells if the issue is open, has triageLabel and has no Workstream.
func (e *Engine) triaging(ctx context.Context, repository github.Repository, issue *gh.Issue) (bool, error) {
	if issue == nil || issue.GetState() != "open" || !hasLabel(issue, triageLabel) {
		return false, nil
	}
	workstream, err := e.commentWorkstream(ctx, repository, issue)
	return workstream == 0, err
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
	workstreams, err := e.openWorkstreams(ctx, []github.Repository{repository})
	if err != nil {
		return fail(err)
	}
	comments, err := repository.Comments(ctx, number)
	if err != nil {
		return fail(err)
	}
	e.launchTriagerComments(ctx, repository, comments)
	comments = e.trustedComments(repository, comments)
	over, err := e.closeEndedTriage(ctx, repository, number, comments)
	if err != nil {
		return fail(err)
	}
	if over {
		return a.End(ended, "done")
	}
	prompt := fmt.Sprintf("%s\n%s\n# Issue\n\n%s#%d %s\n\n%s", triagerPrompt, workstreams, repository.FullName, number, issue.GetTitle(), issue.GetBody())
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
	text := a.replyText()
	if err := a.End(ended, "done"); err != nil {
		return err
	}
	issue, err = repository.Issue(ctx, number)
	if err != nil || issue == nil || issue.GetState() != "open" || !hasLabel(issue, triageLabel) {
		return err
	}
	comments, err = repository.Comments(ctx, number)
	if err != nil {
		return err
	}
	over, err = e.closeEndedTriage(ctx, repository, number, e.trustedComments(repository, comments))
	if err != nil || over || empty(text) {
		return err
	}
	workstream, err := e.commentWorkstream(ctx, repository, issue)
	if err != nil || workstream != 0 {
		return err
	}
	return e.addOrKeepComment(ctx, repository, number, text)
}

// trustedComments gives the comments of trusted authors and of the Mobius App.
func (e *Engine) trustedComments(repository github.Repository, comments []*gh.IssueComment) []*gh.IssueComment {
	return slices.DeleteFunc(slices.Clone(comments), func(comment *gh.IssueComment) bool {
		return !e.TrustedAuthor(repository.AppSlug, comment.GetUser().GetLogin())
	})
}

// closeEndedTriage closes the issue number as completed when the Mobius App commented that the triage ends, and tells if
// it did. A close that failed after that comment stays open until this step runs.
func (e *Engine) closeEndedTriage(ctx context.Context, repository github.Repository, number int64, comments []*gh.IssueComment) (bool, error) {
	if !slices.ContainsFunc(comments, func(comment *gh.IssueComment) bool {
		return strings.EqualFold(comment.GetUser().GetLogin(), appLogin(repository.AppSlug)) && strings.HasPrefix(comment.GetBody(), triageEndText)
	}) {
		return false, nil
	}
	_, err := repository.CloseIssue(ctx, number, "completed")
	return err == nil, err
}

// stopTriager stops the Triager of the issue number when a person removed mobius:triage last. Mobius ignores its own
// removal of that label.
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
		if event.GetEvent() != "unlabeled" || event.GetLabel().GetName() != triageLabel {
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
