package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/mobius-go/internal/github"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
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
