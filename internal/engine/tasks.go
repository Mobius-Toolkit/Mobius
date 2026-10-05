package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// taskLine is a line of the task list of a Workstream.
type taskLine struct {
	number    int64
	title     string
	state     string
	blockedBy []blocker
}

type blocker struct {
	number int64
	// workstreamTitle is the title of the Workstream of a blocker in another Workstream, or "".
	workstreamTitle string
}

// taskLines gives a line for each open issue of a trusted author below the Workstream issue, depth first,
// so a nested task follows its parent. An issue below a Workstream issue of its own belongs to that Workstream.
func (e *Engine) taskLines(ctx context.Context, repository github.Repository, workstream int64) ([]taskLine, error) {
	var lines []taskLine
	return lines, e.addTaskLines(ctx, repository, workstream, workstream, &lines)
}

func (e *Engine) addTaskLines(ctx context.Context, repository github.Repository, workstream, parent int64, lines *[]taskLine) error {
	issues, err := repository.SubIssues(ctx, parent)
	if err != nil {
		return err
	}
	for _, issue := range issues {
		if hasLabel(issue, workstreamLabel) {
			continue
		}
		// The number of an issue in another repository names a different issue here, so this repository
		// cannot give its blockers, its task state or its sub-issues.
		own := !inOtherRepository(issue, repository.FullName)
		if issue.GetState() == "open" && e.TrustedAuthor(repository.AppSlug, issue.GetUser().GetLogin()) {
			line, err := e.taskLine(ctx, repository, workstream, issue, own)
			if err != nil {
				return err
			}
			*lines = append(*lines, line)
		}
		// The sub-issues of a closed or untrusted issue still belong to the Workstream.
		if !own {
			continue
		}
		if err := e.addTaskLines(ctx, repository, workstream, int64(issue.GetNumber()), lines); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) taskLine(ctx context.Context, repository github.Repository, workstream int64, issue *gh.Issue, own bool) (taskLine, error) {
	line := taskLine{number: int64(issue.GetNumber()), title: issue.GetTitle(), state: "open"}
	for _, label := range issue.Labels {
		if state, ok := strings.CutPrefix(label.GetName(), "mobius:"); ok {
			line.state = state
			break
		}
	}
	if !own {
		return line, nil
	}
	if issue.GetIssueDependenciesSummary().GetBlockedBy() > 0 {
		blockers, err := blockers(ctx, repository, workstream, int64(issue.GetNumber()))
		if err != nil {
			return taskLine{}, err
		}
		line.blockedBy = blockers
	}
	if line.state == "working" {
		task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: int64(issue.GetNumber())})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return taskLine{}, err
		}
		if task.State == "queued" {
			line.state = "queued"
		}
	}
	return line, nil
}

// blockers gives the open blockers of the issue number in repository. A blocker in another Workstream has the title of that Workstream.
func blockers(ctx context.Context, repository github.Repository, workstream, number int64) ([]blocker, error) {
	issues, err := repository.BlockedBy(ctx, number)
	if err != nil {
		return nil, err
	}
	var found []blocker
	for _, issue := range issues {
		if issue.GetState() != "open" || inOtherRepository(issue, repository.FullName) {
			continue
		}
		b := blocker{number: int64(issue.GetNumber())}
		other, err := workstreamOf(ctx, repository, b.number)
		if err != nil {
			return nil, err
		}
		if other != 0 && other != workstream {
			otherIssue, err := repository.Issue(ctx, other)
			if err != nil {
				return nil, err
			}
			b.workstreamTitle = otherIssue.GetTitle()
		}
		found = append(found, b)
	}
	return found, nil
}

func taskText(lines []taskLine) string {
	var b strings.Builder
	for _, line := range lines {
		var blockers []string
		for _, blocker := range line.blockedBy {
			if blocker.workstreamTitle == "" {
				blockers = append(blockers, fmt.Sprintf("#%d", blocker.number))
			} else {
				blockers = append(blockers, fmt.Sprintf("#%d (Workstream \"%s\")", blocker.number, blocker.workstreamTitle))
			}
		}
		blockedBy := ""
		if len(blockers) > 0 {
			blockedBy = ", blocked by " + strings.Join(blockers, ", ")
		}
		fmt.Fprintf(&b, "#%d %s: %s%s\n", line.number, line.title, line.state, blockedBy)
	}
	return b.String()
}

// TaskLine is a line of the Tasks tab of a Workstream.
type TaskLine struct {
	Number int64
	Title  string
	// State is the Mobius label with no "mobius:", queued for a task that waits for a slot, or open.
	State string
	URL   string
	// Depth is 0 for a sub-issue of the Workstream issue, and one more for each level below.
	Depth int64
	// BlockedBy holds the open blockers.
	BlockedBy []Blocker
}

// Blocker is an open blocker of a task.
type Blocker struct {
	Number int64
	// WorkstreamTitle is the title of the Workstream of a blocker in another Workstream, or "".
	WorkstreamTitle string
}

// Tasks gives a line for each open issue of a trusted author below the Workstream number of repositoryName, from the
// local copy, depth first. The sub-issues of a closed or untrusted issue take the depth of that issue, so a nested
// task does not move below an unrelated sibling. An issue in another repository has no blockers and no sub-issues
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
		visible := row.State == "open" && e.TrustedAuthor(repository.AppSlug, row.Author)
		if visible {
			line := TaskLine{Number: row.Number, Title: row.Title, State: labelState(labels[row.Position]), URL: row.HtmlUrl, Depth: depth, BlockedBy: []Blocker{}}
			if own {
				line.BlockedBy = append(line.BlockedBy, blockers[row.Position]...)
				if line.State == "working" {
					task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repositoryName, Issue: row.Number})
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						return nil, err
					}
					if task.State == "queued" {
						line.State = "queued"
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

// labelState gives the first Mobius label of labels in the order of Labels, with no "mobius:", or open.
func labelState(labels []string) string {
	for _, label := range Labels {
		if slices.Contains(labels, label.Name) {
			return strings.TrimPrefix(label.Name, "mobius:")
		}
	}
	return "open"
}

// NeedsHuman is an open issue with mobius:needs-human in the tree of a Workstream.
type NeedsHuman struct {
	Repository string
	Workstream int64
	Number     int64
	Title      string
	URL        string
	// PullRequest is the pull request of the live task of the issue, or 0.
	PullRequest    int64
	PullRequestURL string
}

// NeedsHuman gives the open issues of trusted authors with mobius:needs-human in the trees of the Workstreams, from the
// local copy, by repository, Workstream and number.
func (e *Engine) NeedsHuman(ctx context.Context) ([]NeedsHuman, error) {
	rows, err := e.queries.ListCopiedIssuesWithLabel(ctx, needsHumanLabel)
	if err != nil {
		return nil, err
	}
	issues := []NeedsHuman{}
	for _, row := range rows {
		repository, ok := e.github.Repository(row.Repository)
		if !ok || row.State != "open" || otherRepository(row.RepositoryUrl, row.Repository) || !e.TrustedAuthor(repository.AppSlug, row.Author) {
			continue
		}
		issue := NeedsHuman{Repository: row.Repository, Workstream: row.Workstream, Number: row.Number, Title: row.Title, URL: row.HtmlUrl}
		task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: row.Repository, Issue: row.Number})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if task.PullRequest.Valid {
			issue.PullRequest = task.PullRequest.Int64
			// The web URL of a pull request differs from the web URL of its issue only in the path.
			issue.PullRequestURL = strings.TrimSuffix(row.HtmlUrl, fmt.Sprintf("/issues/%d", row.Number)) + fmt.Sprintf("/pull/%d", task.PullRequest.Int64)
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

// ResumeIssue replaces mobius:needs-human of the issue number of repositoryName with mobius:ready. The label change
// uses the user token of the Owner, because the dispatch trusts mobius:ready only from a trusted user.
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
	if err := asOwner.RemoveLabel(ctx, number, needsHumanLabel); err != nil {
		return err
	}
	return asOwner.AddLabel(ctx, number, readyLabel)
}
