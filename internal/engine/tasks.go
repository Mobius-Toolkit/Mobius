package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// taskLines gives a line for each open issue of a trusted author below the Workstream issue, from the local copy,
// depth first, so a nested task follows its parent. An issue below a Workstream issue of its own belongs to that Workstream.
func (e *Engine) taskLines(ctx context.Context, repository github.Repository, workstream int64) ([]TaskLine, error) {
	lines, err := e.Tasks(ctx, repository.FullName, workstream)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(lines, func(line TaskLine) bool { return line.State == "closed" }), nil
}

// taskText gives a line of text for each line. A blocker in another repository has no text.
func taskText(lines []TaskLine) string {
	var b strings.Builder
	for _, line := range lines {
		var blockers []string
		for _, blocker := range line.BlockedBy {
			if blocker.Repository != "" {
				continue
			}
			if blocker.WorkstreamTitle == "" {
				blockers = append(blockers, fmt.Sprintf("#%d", blocker.Number))
			} else {
				blockers = append(blockers, fmt.Sprintf("#%d (Workstream \"%s\")", blocker.Number, blocker.WorkstreamTitle))
			}
		}
		blockedBy := ""
		if len(blockers) > 0 {
			blockedBy = ", blocked by " + strings.Join(blockers, ", ")
		}
		fmt.Fprintf(&b, "#%d %s: %s%s\n", line.Number, line.Title, line.State, blockedBy)
	}
	return b.String()
}

// TaskLine is a line of the Tasks tab of a Workstream.
type TaskLine struct {
	Number int64
	Title  string
	// State is the Mobius label with no "mobius:", or open, or closed for a closed issue. A task that waits for a slot shows "queued". A task that waits for CI shows "waits for CI". A task that waits for the Lead shows "waits for Lead". A task that waits for start_implementer shows "waits for start_implementer".
	State string
	URL   string
	// Depth is 0 for a sub-issue of the Workstream issue, and one more for each level below.
	Depth int64
	// OtherRepository is true for an issue in another repository than the Workstream.
	OtherRepository bool
	// BlockedBy holds the open blockers.
	BlockedBy []Blocker
}

// Blocker is an open blocker of a task.
type Blocker struct {
	Number int64
	// Repository is the full name of the repository of a blocker in another repository, or "".
	Repository string
	// WorkstreamTitle is the title of the Workstream of a blocker in another Workstream, or "".
	WorkstreamTitle string
}

// Tasks gives a line for each issue of a trusted author below the Workstream number of repositoryName, from the
// local copy, depth first. A closed issue has the state closed, and no blockers. The sub-issues of an untrusted
// issue take the depth of that issue, so a nested task does not move below an unrelated sibling. An issue in another repository has no blockers and no sub-issues
// here, because its number names a different issue in this repository.
func (e *Engine) Tasks(ctx context.Context, repositoryName string, workstream int64) ([]TaskLine, error) {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return nil, err
	}
	tree, err := e.queries.ListCopiedTree(ctx, store.ListCopiedTreeParams{Repository: repositoryName, Workstream: workstream})
	if err != nil {
		return nil, err
	}
	labelRows, err := e.queries.ListCopiedTreeLabels(ctx, store.ListCopiedTreeLabelsParams{Repository: repositoryName, Workstream: workstream})
	if err != nil {
		return nil, err
	}
	labels := map[int64][]string{}
	for _, row := range labelRows {
		labels[row.Position] = append(labels[row.Position], row.Name)
	}
	blockerRows, err := e.queries.ListCopiedTreeBlockers(ctx, store.ListCopiedTreeBlockersParams{Repository: repositoryName, Workstream: workstream})
	if err != nil {
		return nil, err
	}
	blockers := map[int64][]Blocker{}
	for _, row := range blockerRows {
		blocker := Blocker{Number: row.Number}
		if row.BlockerRepository != repositoryName {
			blocker.Repository = row.BlockerRepository
		}
		if row.BlockerWorkstream.Valid && row.BlockerWorkstream.Int64 != workstream {
			blocker.WorkstreamTitle = row.BlockerWorkstreamTitle.String
		}
		blockers[row.Position] = append(blockers[row.Position], blocker)
	}
	lines := []TaskLine{}
	// depths holds the depth of the sub-issues of each issue of this repository.
	depths := map[int64]int64{workstream: 0}
	for _, row := range tree {
		if slices.Contains(labels[row.Position], workstreamLabel) {
			continue
		}
		depth := depths[row.Parent]
		own := !otherRepository(row.RepositoryUrl, repositoryName)
		visible := e.TrustedAuthor(repository.AppSlug, row.Author)
		if visible {
			line := TaskLine{Number: row.Number, Title: row.Title, State: labelState(labels[row.Position]), URL: row.HtmlUrl, Depth: depth, OtherRepository: !own, BlockedBy: []Blocker{}}
			if row.State != "open" {
				line.State = "closed"
			} else if own {
				line.BlockedBy = append(line.BlockedBy, blockers[row.Position]...)
				if line.State == "working" || line.State == "open" {
					task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repositoryName, Issue: row.Number})
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						return nil, err
					}
					if line.State == "working" {
						line.State = waitState(task.State, line.State)
					} else if task.State == "needs_human" {
						line.State = "ready"
					}
				}
			}
			lines = append(lines, line)
		}
		if own {
			depths[row.Number] = depth
			if visible {
				depths[row.Number]++
			}
		}
	}
	return lines, nil
}

// waitState gives the line state of a task that waits for a slot, for CI, for the Lead or for start_implementer. Any other task keeps the Mobius label.
func waitState(task, label string) string {
	switch task {
	case "queued":
		return "queued"
	case "checks":
		return "waits for CI"
	case "approval":
		return "waits for Lead"
	case "dispatched":
		return "waits for start_implementer"
	}
	return label
}

// labelState gives the first Mobius label of labels in the order of Labels, with no "mobius:", or open.
func labelState(labels []string) string {
	for _, label := range Labels {
		if slices.Contains(labels, label.Name) {
			return strings.TrimPrefix(label.Name, "mobius:")
		}
	}
	return "open"
}

// NeedsHuman is an open issue with mobius:needs-human or mobius:question in the tree of a Workstream.
type NeedsHuman struct {
	Repository string
	Workstream int64
	Number     int64
	Title      string
	URL        string
	// Stopped is true when the issue has mobius:needs-human: the task stopped.
	Stopped bool
	// Question is true when the issue has mobius:question: the Lead waits for an answer.
	Question bool
	// PullRequest is the pull request of the live task of the issue, or 0.
	PullRequest    int64
	PullRequestURL string
	// Reason is the text of the newest stop of the issue when Stopped is true, or empty.
	Reason string
}

// NeedsHuman gives the open issues of trusted authors with mobius:needs-human or mobius:question in the trees of the
// Workstreams, from the local copy, by repository, Workstream and number.
func (e *Engine) NeedsHuman(ctx context.Context) ([]NeedsHuman, error) {
	rows, err := e.queries.ListCopiedIssuesWithLabels(ctx, store.ListCopiedIssuesWithLabelsParams{NeedsHuman: needsHumanLabel, Question: questionLabel})
	if err != nil {
		return nil, err
	}
	issues := []NeedsHuman{}
	for _, row := range rows {
		repository, ok := e.github.Repository(row.Repository)
		if !ok || row.State != "open" || otherRepository(row.RepositoryUrl, row.Repository) || !e.TrustedAuthor(repository.AppSlug, row.Author) {
			continue
		}
		issue := NeedsHuman{Repository: row.Repository, Workstream: row.Workstream, Number: row.Number, Title: row.Title, URL: row.HtmlUrl, Stopped: row.HasNeedsHuman, Question: row.HasQuestion}
		task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: row.Repository, Issue: row.Number})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if task.PullRequest.Valid {
			issue.PullRequest = task.PullRequest.Int64
			// The web URL of a pull request differs from the web URL of its issue only in the path.
			issue.PullRequestURL = strings.TrimSuffix(row.HtmlUrl, fmt.Sprintf("/issues/%d", row.Number)) + fmt.Sprintf("/pull/%d", task.PullRequest.Int64)
		}
		if issue.Stopped {
			reason, err := e.queries.GetNewestStopText(ctx, store.GetNewestStopTextParams{Repository: row.Repository, Issue: sql.NullInt64{Int64: row.Number, Valid: true}})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			issue.Reason = stopReason(row.Number, reason)
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

// stopReason removes the time, the kind, the number and the title from the start of the text of a stop event or of a
// stale pull request event of the issue number. The title is the title at the time of the stop, so the prefix ends at
// the first quote that a colon follows. Any other text stays as it is.
func stopReason(number int64, text string) string {
	prefix := regexp.MustCompile(fmt.Sprintf(`(?s)^\d{4}-\d{2}-\d{2} \d{2}:\d{2} UTC (?:stop|stale pull request #\d+) of #%d ".*?": `, number))
	return prefix.ReplaceAllString(text, "")
}

// ResumeIssue removes mobius:needs-human from the issue number of repositoryName, and from the pull request of its
// task. The next poll continues the task. An issue with no live task has lost its task: ResumeIssue adds mobius:ready
// instead, so the dispatch starts a new task. The label change uses the user token of the Owner, because the poll
// trusts a label change only from a trusted user.
func (e *Engine) ResumeIssue(ctx context.Context, repositoryName string, number int64) error {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return err
	}
	asOwner, err := e.github.AsOwner(ctx, repository)
	if err != nil {
		// The text tells the Owner how to authorize the Mobius App.
		return refusal(err.Error())
	}
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: number})
	if errors.Is(err, sql.ErrNoRows) {
		if err := asOwner.RemoveLabel(ctx, number, needsHumanLabel); err != nil {
			return err
		}
		if err := asOwner.AddLabel(ctx, number, readyLabel); err != nil {
			return err
		}
		if err := e.changeCopiedLabels(ctx, repository.FullName, number, []string{readyLabel}, []string{needsHumanLabel}); err != nil {
			return err
		}
		return e.dismissIssueItems(ctx, repository.FullName, number, stoppedKind, lostKind)
	}
	if err != nil {
		return err
	}
	if err := removeNeedsHuman(ctx, asOwner, task); err != nil {
		return err
	}
	if err := e.changeCopiedLabels(ctx, repository.FullName, number, nil, []string{needsHumanLabel}); err != nil {
		return err
	}
	return e.dismissIssueItems(ctx, repository.FullName, number, stoppedKind, lostKind)
}

// StartIssue adds mobius:ready to the issue number of repositoryName. The label change uses the user token of the
// Owner, because the dispatch trusts mobius:ready only from a trusted user.
func (e *Engine) StartIssue(ctx context.Context, repositoryName string, number int64) error {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return err
	}
	asOwner, err := e.github.AsOwner(ctx, repository)
	if err != nil {
		// The text tells the Owner how to authorize the Mobius App.
		return refusal(err.Error())
	}
	if err := asOwner.AddLabel(ctx, number, readyLabel); err != nil {
		return err
	}
	return e.changeCopiedLabels(ctx, repository.FullName, number, []string{readyLabel}, nil)
}
