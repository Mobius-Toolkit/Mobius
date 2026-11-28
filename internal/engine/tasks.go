package engine

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

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

// taskText gives a line of text for each line, with the repository of each task and of each blocker. A blocker with no
// repository is in workstreamRepository.
func taskText(workstreamRepository string, lines []TaskLine) string {
	var b strings.Builder
	for _, line := range lines {
		var blockers []string
		for _, blocker := range line.BlockedBy {
			name := cmp.Or(blocker.Repository, workstreamRepository)
			if blocker.WorkstreamTitle == "" {
				blockers = append(blockers, fmt.Sprintf("%s#%d", name, blocker.Number))
			} else {
				blockers = append(blockers, fmt.Sprintf("%s#%d (Workstream \"%s\")", name, blocker.Number, blocker.WorkstreamTitle))
			}
		}
		blockedBy := ""
		if len(blockers) > 0 {
			blockedBy = ", blocked by " + strings.Join(blockers, ", ")
		}
		wait := ""
		if line.Wait != "" {
			wait = fmt.Sprintf(" (wait: %s)", line.Wait)
		}
		evergreen := ""
		if line.Evergreen {
			evergreen = " (evergreen Project)"
		}
		fmt.Fprintf(&b, "%s#%d %s%s: %s%s%s\n", line.Repository, line.Number, line.Title, evergreen, line.State, wait, blockedBy)
	}
	return b.String()
}

// TaskLine is a line of the Tasks tab of a Workstream.
type TaskLine struct {
	Number int64
	Title  string
	// Kind is "task" or "project".
	Kind string
	// Evergreen is true for a Project that has no end.
	Evergreen bool
	// State is the Mobius label with no "mobius:", or open, or closed for a closed issue. A Start that waits shows "start waits". Wait holds the wait of a live task.
	State string
	URL   string
	// Repository is the full name of the repository of the issue.
	Repository string
	// Depth is 0 for a sub-issue of the Workstream issue, and one more for each level below.
	Depth int64
	// OtherRepository is true for an issue in another repository than the Workstream.
	OtherRepository bool
	// BlockedBy holds the open blockers.
	BlockedBy []Blocker
	// Implementer is the own Implementer setting of an open task, or nil.
	Implementer *store.IssueBinding
	// StartWait is the reason that a Start waits when State is "start waits", or "". An open blocker gives no reason,
	// because BlockedBy shows it.
	StartWait string
	// Wait is the wait of the live task of the issue, or "".
	Wait Wait
	// PausedUntil is the end of the pause of the Harness when Wait is "paused", or nil.
	PausedUntil *time.Time
}

// startWaits holds the state that Tasks reads one time to give the reason of each waiting Start. ciStops has the
// result of ciStop of each repository that a waiting Start needs.
type startWaits struct {
	draining   bool
	startOff   bool
	repairsOff bool
	ciStops    map[string]map[int64]bool
}

func (e *Engine) readStartWaits(ctx context.Context) (*startWaits, error) {
	startOff, err := e.startOff(ctx)
	if err != nil {
		return nil, err
	}
	repairsOff, err := e.repairsOff(ctx)
	if err != nil {
		return nil, err
	}
	return &startWaits{draining: e.draining(), startOff: startOff, repairsOff: repairsOff, ciStops: map[string]map[int64]bool{}}, nil
}

// startWait gives the reason that the Start of the issue of repository waits, or "". Triage comes first, because it
// holds the Start also after the drain ends. The other reasons follow the order of the holds of dispatchReady. It writes
// no held work.
func (e *Engine) startWait(ctx context.Context, reads *startWaits, repository string, row treeRow, blocked bool) (string, error) {
	if row.Triage {
		return "waits for the triage", nil
	}
	if reads.draining {
		return "waits for the upgrade", nil
	}
	stop, ok := reads.ciStops[repository]
	if !ok {
		var err error
		if stop, err = e.ciStop(ctx, repository); err != nil {
			return "", err
		}
		reads.ciStops[repository] = stop
	}
	switch {
	case len(stop) > 0 && !stop[row.Number]:
		return "waits for the CI break of main", nil
	case blocked:
		return "", nil
	case row.Kind == projectKind:
		return "a Project does not start", nil
	case reads.startOff:
		return "waits for the Start switch", nil
	case reads.repairsOff:
		repair, err := e.repairIssue(ctx, repository, row.Number)
		if repair {
			return "waits for the Repairs switch", nil
		}
		return "", err
	}
	return "", nil
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
// issue and of a bucket take the depth of that issue, so a nested task does not move below an unrelated sibling. An issue in another
// repository has its own blockers, sub-issues and task state.
func (e *Engine) Tasks(ctx context.Context, repositoryName string, workstream int64) ([]TaskLine, error) {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return nil, err
	}
	var treeRows []store.ListCopiedTreeRow
	var blockerRows []store.ListCopiedTreeBlockersRow
	err = e.readTx(ctx, func(q *store.Queries) error {
		var err error
		treeRows, err = q.ListCopiedTree(ctx, store.ListCopiedTreeParams{Repository: repositoryName, Number: workstream})
		if err != nil {
			return err
		}
		blockerRows, err = q.ListCopiedTreeBlockers(ctx, store.ListCopiedTreeBlockersParams{Repository: repositoryName, Number: workstream})
		return err
	})
	if err != nil {
		return nil, err
	}
	tree, err := orderTree(treeRows)
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
		blockers[row.Issue] = append(blockers[row.Issue], blocker)
	}
	reads, err := e.readStartWaits(ctx)
	if err != nil {
		return nil, err
	}
	sessions, err := e.sessionWaits(ctx)
	if err != nil {
		return nil, err
	}
	lines := []TaskLine{}
	// depths holds the depth of the sub-issues of each issue by its id. The id 0 is the Workstream issue.
	depths := map[int64]int64{}
	for _, row := range tree {
		if row.Number == 0 {
			depths[row.ID] = depths[row.Parent.Int64]
			continue
		}
		issueRepositoryName := repositoryName
		own := strings.EqualFold(row.IssueRepository, repositoryName)
		if !own {
			issueRepositoryName = row.IssueRepository
		}
		depth := depths[row.Parent.Int64]
		visible := e.TrustedAuthor(repository.AppSlug, row.Author)
		if visible {
			line := TaskLine{Number: row.Number, Title: row.Title, Kind: row.Kind, Evergreen: row.Evergreen, State: labelState(row.labels), URL: issueLink(issueRepositoryName, row.Number), Repository: issueRepositoryName, Depth: depth, OtherRepository: !own, BlockedBy: []Blocker{}}
			if row.State != "open" {
				line.State = "closed"
			} else {
				line.BlockedBy = append(line.BlockedBy, blockers[row.ID]...)
				setting, found, err := e.IssueBinding(ctx, "implementer", issueRepositoryName, row.Number)
				if err != nil {
					return nil, err
				}
				if found {
					line.Implementer = &setting
				}
				if line.State == "working" || line.State == "open" || line.State == "triage" {
					task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: issueRepositoryName, Issue: row.Number})
					live := err == nil
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						return nil, err
					}
					switch {
					case line.State == "working":
						heldEvent, err := e.queries.HasHeldLeadEvent(ctx, store.HasHeldLeadEventParams{WorkstreamRepository: repositoryName, Repository: issueRepositoryName, Issue: sql.NullInt64{Int64: row.Number, Valid: true}})
						if err != nil {
							return nil, err
						}
						if line.Wait, line.PausedUntil, err = e.taskWait(ctx, task.State, heldEvent, sessions[issueRef{issueRepositoryName, row.Number}]); err != nil {
							return nil, err
						}
					case line.State == "open" && task.State == "needs_human":
						line.State = "ready"
					case line.State == "open" && task.State == "owner_action":
						line.Wait = waitOf(task.State, false, "")
					case row.StartRequested && (!live || task.State == "stopped" && !task.PullRequest.Valid):
						line.State = "start waits"
						line.StartWait, err = e.startWait(ctx, reads, issueRepositoryName, row, len(line.BlockedBy) > 0)
						if err != nil {
							return nil, err
						}
					case row.StartRequested && live && line.State == "open":
						line.State = "ready"
					}
				}
			}
			lines = append(lines, line)
		}
		depths[row.ID] = depth
		if visible {
			depths[row.ID]++
		}
	}
	return lines, nil
}

// Task is the detail of a task in the tree of a Workstream.
type Task struct {
	Body           string
	PullRequest    int64
	PullRequestURL string
}

// Task gives the detail of one visible task in the tree of the Workstream number of repositoryName.
func (e *Engine) Task(ctx context.Context, repositoryName string, workstream int64, taskRepositoryName string, issue int64) (*Task, error) {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return nil, err
	}
	tree, err := e.queries.ListCopiedTree(ctx, store.ListCopiedTreeParams{Repository: repositoryName, Number: workstream})
	if err != nil {
		return nil, err
	}
	for _, row := range tree {
		issueRepositoryName := repositoryName
		if !strings.EqualFold(row.IssueRepository, repositoryName) {
			issueRepositoryName = row.IssueRepository
		}
		if issueRepositoryName != taskRepositoryName || row.Number != issue {
			continue
		}
		if !e.TrustedAuthor(repository.AppSlug, row.Author) {
			return nil, refuse("The task %s#%d is not visible in the tree of Workstream #%d.", taskRepositoryName, issue, workstream)
		}
		body, err := e.queries.GetCopiedIssueBody(ctx, store.GetCopiedIssueBodyParams{Repository: row.IssueRepository, Number: row.Number})
		if err != nil {
			return nil, err
		}
		detail := &Task{Body: body}
		task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: issueRepositoryName, Issue: row.Number})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if task.PullRequest.Valid {
			detail.PullRequest = task.PullRequest.Int64
			detail.PullRequestURL = pullRequestLink(issueRepositoryName, task.PullRequest.Int64)
		}
		return detail, nil
	}
	return nil, refuse("The task %s#%d is not in the tree of Workstream #%d.", taskRepositoryName, issue, workstream)
}

// labelState gives the first Mobius label of labels in the order of Labels, with no "mobius:", or open.
// mobius:ready and mobius:evergreen are no states of a task, so labelState skips them.
func labelState(labels []string) string {
	for _, label := range Labels {
		if label.Name == readyLabel || label.Name == evergreenLabel {
			continue
		}
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
	// Stopped is true when the live task of the issue is in needs_human, or the issue has an open lost Inbox item.
	Stopped bool
	// Question is true when the issue has an open question Inbox item: the Lead waits for an answer.
	Question bool
	// PullRequest is the pull request of the live task of the issue, or 0.
	PullRequest    int64
	PullRequestURL string
	// Reason is the text of the newest stop of the issue when Stopped is true, or empty.
	Reason string
}

// NeedsHuman gives the open issues of trusted authors that are stopped or have an open question in the trees of the
// Workstreams, from the local copy, by repository, Workstream and number.
func (e *Engine) NeedsHuman(ctx context.Context) ([]NeedsHuman, error) {
	rows, err := e.queries.ListIssuesNeedingHuman(ctx)
	if err != nil {
		return nil, err
	}
	issues := []NeedsHuman{}
	for _, row := range rows {
		home, ok := e.github.Repository(row.Repository)
		if !ok {
			continue
		}
		repository, ok := e.namedRepository(row.IssueRepository, home)
		if !ok || row.State != "open" || !e.TrustedAuthor(repository.AppSlug, row.Author) {
			continue
		}
		issue := NeedsHuman{Repository: row.Repository, Workstream: row.Workstream, Number: row.Number, Title: row.Title, URL: issueLink(repository.FullName, row.Number), Stopped: row.Stopped, Question: row.Question}
		task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: row.Number})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if task.PullRequest.Valid {
			issue.PullRequest = task.PullRequest.Int64
			issue.PullRequestURL = pullRequestLink(repository.FullName, task.PullRequest.Int64)
		}
		if issue.Stopped {
			reason, err := e.queries.GetNewestStopText(ctx, store.GetNewestStopTextParams{WorkstreamRepository: row.Repository, Repository: repository.FullName, Issue: sql.NullInt64{Int64: row.Number, Valid: true}})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			issue.Reason = stopReason(row.Number, reason)
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

// BoardTask is a task on the Tasks board.
type BoardTask struct {
	Repository           string
	Number               int64
	Title                string
	URL                  string
	WorkstreamRepository string
	Workstream           int64
	WorkstreamTitle      string
	State                string
	StateAt              time.Time
	// PullRequest is the pull request of the task, or 0.
	PullRequest    int64
	PullRequestURL string
	FixRounds      int64
	ReviewRounds   int64
	// TotalFixRounds and TotalReviewRounds count every round of the task, also the rounds before a reset of FixRounds and ReviewRounds.
	TotalFixRounds    int64
	TotalReviewRounds int64
	// Merging is true for a task in ready_for_review whose merge the Owner started.
	Merging bool
	// MergeRefusal is the reason that GitHub gave for the refused merge of a task in ready_for_review, or "".
	MergeRefusal string
	// MergeQueuePlace is the place of the task in the merge queue of its repository, from 1, or nil.
	MergeQueuePlace *int64
	// MergeQueueSize is the number of tasks in the merge queue of that repository, or 0 when MergeQueuePlace is nil.
	MergeQueueSize int64
	// Wait is the wait of the task, or "".
	Wait Wait
	// PausedUntil is the end of the pause of the Harness when Wait is "paused", or nil.
	PausedUntil *time.Time
}

// BoardTasks gives the tasks that the Tasks board shows, newest state change first: each task that has not ended, and
// each task that ended in the last 24 hours. A task whose issue or Workstream is not in the local copy is not in the list.
func (e *Engine) BoardTasks(ctx context.Context) ([]BoardTask, error) {
	rows, err := e.queries.ListBoardTasks(ctx)
	if err != nil {
		return nil, err
	}
	queued, err := e.queries.ListAllMergeQueueTasks(ctx)
	if err != nil {
		return nil, err
	}
	repoQueue := make(map[string][]int64)
	for _, q := range queued {
		repoQueue[q.Repository] = append(repoQueue[q.Repository], q.ID)
	}
	sessions, err := e.sessionWaits(ctx)
	if err != nil {
		return nil, err
	}
	since := e.timeNow().Add(-24 * time.Hour)
	tasks := []BoardTask{}
	for _, row := range rows {
		stateAt, err := time.Parse(time.RFC3339Nano, row.StateAt)
		if err != nil {
			return nil, err
		}
		if row.State == "ended" && stateAt.Before(since) {
			continue
		}
		task := BoardTask{Repository: row.Repository, Number: row.Issue, Title: row.IssueTitle, URL: issueLink(row.Repository, row.Issue), WorkstreamRepository: row.WorkstreamRepository, Workstream: row.Workstream, WorkstreamTitle: row.WorkstreamTitle, State: row.State, StateAt: stateAt, FixRounds: row.FixRounds, ReviewRounds: row.ReviewRounds, TotalFixRounds: row.TotalFixRounds, TotalReviewRounds: row.TotalReviewRounds}
		heldEvent := false
		if row.State == "approval" {
			heldEvent, err = e.queries.HasHeldLeadEvent(ctx, store.HasHeldLeadEventParams{WorkstreamRepository: row.WorkstreamRepository, Repository: row.Repository, Issue: sql.NullInt64{Int64: row.Issue, Valid: true}})
			if err != nil {
				return nil, err
			}
		}
		if task.Wait, task.PausedUntil, err = e.taskWait(ctx, row.State, heldEvent, sessions[issueRef{row.Repository, row.Issue}]); err != nil {
			return nil, err
		}
		if row.MergeQueuedAt.Valid {
			queue := repoQueue[row.Repository]
			idx := slices.Index(queue, row.ID)
			if idx >= 0 {
				place := int64(idx + 1)
				task.MergeQueuePlace = &place
				task.MergeQueueSize = int64(len(queue))
			}
		}
		if row.State == "ready_for_review" {
			task.Merging = row.Merging != 0
			if !task.Merging {
				task.MergeRefusal = row.MergeRefusal
			}
		}
		if row.PullRequest.Valid {
			task.PullRequest = row.PullRequest.Int64
			task.PullRequestURL = pullRequestLink(row.Repository, row.PullRequest.Int64)
		}
		tasks = append(tasks, task)
	}
	slices.SortStableFunc(tasks, func(a, b BoardTask) int { return b.StateAt.Compare(a.StateAt) })
	return tasks, nil
}

// stopReason removes the time, the kind, the number and the title from the start of the text of a stop event or of a
// stale pull request event of the issue number. The title is the title at the time of the stop, so the prefix ends at
// the first quote that a colon follows. Any other text stays as it is.
func stopReason(number int64, text string) string {
	prefix := regexp.MustCompile(fmt.Sprintf(`(?s)^\d{4}-\d{2}-\d{2} \d{2}:\d{2} UTC (?:stop|stale pull request #\d+) of #%d ".*?": `, number))
	return prefix.ReplaceAllString(text, "")
}

// ResumeIssue continues a task in needs_human at once, as resume says. The send of the state labels then removes
// mobius:needs-human from the issue and from the pull request of the task. An issue with no live task has lost its
// task: ResumeIssue requests a Start instead, so the dispatch starts a new task, and it dismisses the lost item and
// sends the state labels, so the issue loses mobius:needs-human.
func (e *Engine) ResumeIssue(ctx context.Context, repositoryName string, number int64) (Record, error) {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return Record{}, err
	}
	return e.resumeIssue(ctx, repository, number, "ui", "You resumed")
}

// resumeIssue is the handler of the resume of the issue number for the actor: the Owner in the UI, or a trusted user
// who removed mobius:needs-human on GitHub. what is the start of the Record for the chat of the Lead.
func (e *Engine) resumeIssue(ctx context.Context, repository github.Repository, number int64, actor, what string) (Record, error) {
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: number})
	if errors.Is(err, sql.ErrNoRows) {
		if err := e.requestStart(ctx, repository, number, actor); err != nil {
			return Record{}, err
		}
		if err := e.dismissIssueItems(ctx, repository.FullName, number, stoppedKind, lostKind); err != nil {
			return Record{}, err
		}
		change, err := addStateLabelsChange(ctx, e.queries, repository.FullName, number)
		if err != nil {
			return Record{}, err
		}
		if err := e.sendNow(ctx, repository, change); err != nil {
			return Record{}, err
		}
		return e.issueRecord(ctx, repository, number, what), nil
	}
	if err != nil {
		return Record{}, err
	}
	if task.State == "needs_human" {
		issue, err := e.existingIssue(ctx, repository, number)
		if err != nil {
			return Record{}, err
		}
		if err := e.resumeNeedsHuman(ctx, repository, issue, task, actor); err != nil {
			return Record{}, err
		}
	}
	if err := e.dismissIssueItems(ctx, repository.FullName, number, stoppedKind, lostKind); err != nil {
		return Record{}, err
	}
	return e.issueRecord(ctx, repository, number, what), nil
}

// resumeNeedsHuman continues the task of the issue as resume says, when the live task of the issue is still the task in
// needs_human that the caller read. The Resume of a person, mark_ready, and each resume of the poll for a task in
// needs_human call it. It holds stopMu and reads the task again, so two of them do not continue one task two times.
func (e *Engine) resumeNeedsHuman(ctx context.Context, repository github.Repository, issue *gh.Issue, task store.Task, actor string) error {
	e.stopMu.Lock()
	defer e.stopMu.Unlock()
	current, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: task.Repository, Issue: task.Issue})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.State != "needs_human" || current.NeedsHumanAt != task.NeedsHumanAt {
		return nil
	}
	return e.resume(ctx, repository, issue, current, actor)
}

// issueRecord gives the Record "<what> <issue>." for the Lead chat of the Workstream of the issue number. The live
// task gives the Workstream, else the parents of the issue give it. An issue in no Workstream gets a Record with no
// chat. A failed read only goes to the log, because the action has succeeded.
func (e *Engine) issueRecord(ctx context.Context, repository github.Repository, number int64, what string) Record {
	workstreamRepository, workstream, err := e.issueWorkstream(ctx, repository, number)
	if err != nil {
		log.Printf("find the Workstream of %s#%d for a record: %v", repository.FullName, number, err)
		return Record{}
	}
	if workstream == 0 {
		return Record{}
	}
	return recordIn(fmt.Sprintf("%s %s.", what, eventRef(workstreamRepository, repository.FullName, number)), leadChat(workstreamRepository, workstream))
}

func (e *Engine) issueWorkstream(ctx context.Context, repository github.Repository, number int64) (string, int64, error) {
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: number})
	if errors.Is(err, sql.ErrNoRows) {
		return e.workstreamOf(ctx, repository, number)
	}
	return task.WorkstreamRepository, task.Workstream, err
}

// stopLiveTask is the handler of the stop of the live task that is not stopped: it clears the Start of its issue and
// stops the task with summary. what is the start of the Record for the chat of the Lead. The caller holds stopMu.
func (e *Engine) stopLiveTask(ctx context.Context, repository github.Repository, task store.Task, summary, what string) (Record, error) {
	if err := e.clearStart(ctx, task.Repository, task.Issue); err != nil {
		return Record{}, err
	}
	if err := e.stopTask(ctx, repository, task, summary); err != nil {
		return Record{}, err
	}
	return e.issueRecord(ctx, repository, task.Issue, what), nil
}

// StopIssue stops the work on the issue number of repositoryName. A live task that is not stopped stops with its
// Workers and clears any waiting Start. An issue with a waiting Start and no such task clears its Start.
func (e *Engine) StopIssue(ctx context.Context, repositoryName string, number int64) (Record, error) {
	e.stopMu.Lock()
	defer e.stopMu.Unlock()
	repository, err := e.repository(repositoryName)
	if err != nil {
		return Record{}, err
	}
	if _, err := e.existingIssue(ctx, repository, number); err != nil {
		return Record{}, err
	}
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: number})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Record{}, err
	}
	if err == nil && task.State != "stopped" {
		return e.stopLiveTask(ctx, repository, task, "Stopped by the Owner.", "You stopped")
	}
	start, err := e.queries.GetIssueStart(ctx, store.GetIssueStartParams{Repository: repository.FullName, Number: number})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Record{}, err
	}
	if err == nil && start.StartRequestedAt.Valid {
		if err := e.clearStart(ctx, repository.FullName, number); err != nil {
			return Record{}, err
		}
		return e.issueRecord(ctx, repository, number, "You stopped"), nil
	}
	workstreamRepository, _, err := e.issueWorkstream(ctx, repository, number)
	if err != nil {
		return Record{}, err
	}
	return Record{}, refuse("%s has no task to stop.", eventRef(workstreamRepository, repository.FullName, number))
}

// StartIssue clears the triage state of the issue number of repositoryName and requests a Start.
func (e *Engine) StartIssue(ctx context.Context, repositoryName string, number int64) (Record, error) {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return Record{}, err
	}
	if _, err := e.setTriage(ctx, repository, number, false); err != nil {
		return Record{}, err
	}
	if err := e.requestStart(ctx, repository, number, "ui"); err != nil {
		return Record{}, err
	}
	return e.issueRecord(ctx, repository, number, "You started"), nil
}
