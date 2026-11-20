package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// closedText is the comment on each issue and pull request that the close of a Workstream closes.
const closedText = "Workstream closed"

// CompleteWorkstream closes the open Workstream number of repository as completed, when the Workstream issue has
// closed sub-issues or no sub-issue after a report of its Lead.
func (e *Engine) CompleteWorkstream(ctx context.Context, repositoryName string, number int64) error {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return err
	}
	stored, err := e.queries.HasCopiedWorkstream(ctx, store.HasCopiedWorkstreamParams{Repository: repository.FullName, Number: number})
	if err != nil {
		return err
	}
	if !stored {
		return refuse("The issue is not an open Workstream.")
	}
	issue, err := repository.Issue(ctx, number)
	if err != nil {
		return err
	}
	if issue == nil || issue.GetState() != "open" || !hasLabel(issue, workstreamLabel) {
		return refuse("The issue is not an open Workstream.")
	}
	tasks, err := repository.SubIssues(ctx, number)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		finishedAt, err := e.queries.GetWorkstreamFinishedAt(ctx, store.GetWorkstreamFinishedAtParams{Repository: repository.FullName, Number: number})
		if errors.Is(err, sql.ErrNoRows) {
			return refuse("The issue is not an open Workstream.")
		}
		if err != nil {
			return err
		}
		if !finishedAt.Valid {
			return refuse("The Workstream has no task, and its Lead did not report it as finished.")
		}
	} else if slices.ContainsFunc(tasks, func(task *gh.Issue) bool { return task.GetState() != "closed" }) {
		return refuse("A task of the Workstream is open.")
	}
	todos, err := e.queries.CountOpenWorkstreamTodos(ctx, store.CountOpenWorkstreamTodosParams{
		WorkstreamRepository: repository.FullName,
		Workstream:           number,
	})
	if err != nil {
		return err
	}
	if todos == 1 {
		return refuse("The Workstream has 1 open todo.")
	}
	if todos > 1 {
		return refuse("The Workstream has %d open todos.", todos)
	}
	ctx = context.WithoutCancel(ctx)
	defer e.beginClose(repository.FullName, number)()
	if err := e.handleWorkstreamClose(ctx, repository, number, closedCompleted); err != nil {
		return err
	}
	e.addRecord(ctx, leadChat(repository.FullName, number), "You closed the Workstream.")
	return nil
}

// setBrief writes brief with no Lead chat link line into the open Workstream row and adds an outgoing body change in
// the same transaction, then sends it. It gives the id of the outgoing change, or 0 when the Workstream is not open.
func (e *Engine) setBrief(ctx context.Context, repository github.Repository, number int64, brief string) (int64, error) {
	var id int64
	err := e.inTx(ctx, func(q *store.Queries) error {
		updated, err := q.SetWorkstreamBody(ctx, store.SetWorkstreamBodyParams{
			Body:       e.WithoutChatLink(repository.FullName, number, brief),
			Repository: repository.FullName,
			Number:     number,
		})
		if err != nil || updated == 0 {
			return err
		}
		id, err = addFieldChange(ctx, q, repository.FullName, number, bodyField)
		return err
	})
	if err != nil || id == 0 {
		return 0, err
	}
	e.publish(Change{Workstreams: true})
	if err := e.sendNow(ctx, repository, id); err != nil {
		return 0, err
	}
	return id, nil
}

// changeBrief is setBrief for the change of the Brief by actor on GitHub. When the Workstream is open, it also adds a
// brief_change event for the Lead.
func (e *Engine) changeBrief(ctx context.Context, repository github.Repository, number int64, actor, brief string) error {
	id, err := e.setBrief(ctx, repository, number, brief)
	if err != nil || id == 0 {
		return err
	}
	row, err := e.queries.GetWorkstream(ctx, store.GetWorkstreamParams{Repository: repository.FullName, Number: number})
	if err != nil {
		return err
	}
	text := eventText(time.Now(), "change of the Brief of Workstream", fmt.Sprintf("#%d", number), &gh.Issue{Title: &row.Title}, actor, row.Body)
	return e.addLeadEvent(ctx, repository.FullName, number, "", sql.NullInt64{}, "brief_change", text)
}

// setTitle writes title into the Workstream row and adds an outgoing title change in the same transaction, then sends it.
func (e *Engine) setTitle(ctx context.Context, repository github.Repository, number int64, title string) error {
	var id int64
	err := e.inTx(ctx, func(q *store.Queries) error {
		updated, err := q.SetWorkstreamTitle(ctx, store.SetWorkstreamTitleParams{Title: title, Repository: repository.FullName, Number: number})
		if err != nil || updated == 0 {
			return err
		}
		id, err = addFieldChange(ctx, q, repository.FullName, number, titleField)
		return err
	})
	if err != nil || id == 0 {
		return err
	}
	e.publish(Change{Workstreams: true})
	return e.sendNow(ctx, repository, id)
}

// handleWorkstreamClose closes the Workstream number of repository in workstreams, removes its tasks and copied tree,
// writes its state to GitHub, stops its work, and starts a Curator. The creator Lead gets a close event with reason.
// When the row is already closed and no earlier closeWorkstream failed, it stops.
func (e *Engine) handleWorkstreamClose(ctx context.Context, repository github.Repository, number int64, reason string) error {
	var changeID int64
	closed, err := e.closeWorkstreamRow(ctx, repository.FullName, number, reason, func(q *store.Queries) error {
		if err := deleteWorkstreamRecurringTasks(ctx, q, repository.FullName, number); err != nil {
			return err
		}
		if err := q.DeleteWorkstreamMcpServers(ctx, store.DeleteWorkstreamMcpServersParams{Repository: repository.FullName, Workstream: number}); err != nil {
			return err
		}
		var err error
		changeID, err = addFieldChange(ctx, q, repository.FullName, number, stateField)
		return err
	})
	if err != nil {
		return err
	}
	if !closed {
		pending, err := e.queries.IsWorkstreamClosePending(ctx, store.IsWorkstreamClosePendingParams{Repository: repository.FullName, Number: number})
		if err != nil || !pending {
			return err
		}
	} else {
		e.publish(Change{Workstreams: true})
		if err := e.sendNow(ctx, repository, changeID); err != nil {
			return err
		}
	}
	if err := e.closeUncopiedSubIssues(ctx, repository, number); err != nil {
		return err
	}
	if err := e.closeWorkstream(ctx, repository, number); err != nil {
		return err
	}
	if err := e.removeClosedWorkstreamTree(ctx, repository.FullName, number); err != nil {
		return err
	}
	if err := e.queries.ClearWorkstreamClosePending(ctx, store.ClearWorkstreamClosePendingParams{Repository: repository.FullName, Number: number}); err != nil {
		return err
	}
	if err := e.startCurator(ctx, repository.FullName); err != nil {
		log.Printf("start a Curator of %s: %v", repository.FullName, err)
	}
	return nil
}

// retryWorkstreamCloses runs again the close of each Workstream of repository whose earlier closeWorkstream failed. The
// close event of a send or of the close as "won't do" is an own write, so it is no incoming change that runs it again.
// A Workstream whose close runs now waits for the next poll.
func (e *Engine) retryWorkstreamCloses(ctx context.Context, repository github.Repository) error {
	numbers, err := e.queries.ListClosePendingWorkstreams(ctx, repository.FullName)
	if err != nil {
		return err
	}
	for _, number := range numbers {
		if e.IsClosing(repository.FullName, number) {
			continue
		}
		if err := e.handleWorkstreamClose(ctx, repository, number, ""); err != nil {
			return err
		}
	}
	return nil
}

const (
	closedCompleted  = "completed"
	closedNotPlanned = "not planned"
)

// closeReason gives the close reason of the closed issue of a Workstream.
func closeReason(issue *gh.Issue) string {
	if reason := issue.GetStateReason(); reason == "not_planned" || reason == "duplicate" {
		return closedNotPlanned
	}
	return closedCompleted
}

// closeWorkstreamRow sets the row of the Workstream number of repository to closed and runs write in the same
// transaction. When the row was open and its creator Workstream is open, the transaction also adds a close event for
// the creator Lead, and the Lead gets it after the commit. It tells if the row was open.
func (e *Engine) closeWorkstreamRow(ctx context.Context, repository string, number int64, reason string, write func(*store.Queries) error) (bool, error) {
	e.chatOrder.Lock()
	defer e.chatOrder.Unlock()
	closed := false
	creator := int64(0)
	var message store.ChatMessage
	err := e.inTx(ctx, func(q *store.Queries) error {
		updated, err := q.CloseWorkstream(ctx, store.CloseWorkstreamParams{Repository: repository, Number: number})
		if err != nil || updated == 0 {
			return err
		}
		closed = true
		if err := write(q); err != nil {
			return err
		}
		row, err := q.GetOpenCreatorOfWorkstream(ctx, store.GetOpenCreatorOfWorkstreamParams{Repository: repository, Number: number})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		creator = row.Number
		text := fmt.Sprintf("%s close of Workstream #%d \"%s\" that you created: %s.", time.Now().UTC().Format(timeFormat), number, row.Title, reason)
		message, _, err = e.storeLeadEvent(ctx, q, repository, creator, "", sql.NullInt64{}, "workstream_closed", text, now(), commentRef{})
		return err
	})
	if err != nil || !closed {
		return false, err
	}
	if creator != 0 {
		if err := e.announceChatMessage(ctx, leadChat(repository, creator), eventAuthor, message); err != nil {
			return true, err
		}
		if err := e.sendEvents(ctx, repository, creator); err != nil {
			return true, err
		}
	}
	return true, nil
}

// reopenWorkstream reopens the Workstream of issue when its row is closed (filling title, body and created_at from the
// issue when body is empty), adds an outgoing state change, copies its tree when copyTree is true, and adds a reopen
// event for the Lead. It tells if the tree copy failed.
func (e *Engine) reopenWorkstream(ctx context.Context, repository github.Repository, issue *gh.Issue, actor string, copyTree bool) (bool, error) {
	number := int64(issue.GetNumber())
	row, err := e.queries.GetWorkstream(ctx, store.GetWorkstreamParams{Repository: repository.FullName, Number: number})
	if err == nil && row.State != "closed" {
		return false, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var changeID int64
	reopened := false
	reopenTx := func(q *store.Queries) error {
		row, err := q.GetWorkstream(ctx, store.GetWorkstreamParams{Repository: repository.FullName, Number: number})
		if errors.Is(err, sql.ErrNoRows) {
			added, err := e.addWorkstreamRow(ctx, q, repository.FullName, issue)
			if err != nil || added == 0 {
				return err
			}
			reopened = true
			changeID, err = addFieldChange(ctx, q, repository.FullName, number, stateField)
			return err
		}
		if err != nil || row.State != "closed" {
			return err
		}
		var updated int64
		if row.Body == "" {
			updated, err = q.FillAndReopenWorkstream(ctx, store.FillAndReopenWorkstreamParams{
				Title:      issue.GetTitle(),
				Body:       e.WithoutChatLink(repository.FullName, number, issue.GetBody()),
				CreatedAt:  issue.GetCreatedAt().UTC().Format(time.RFC3339Nano),
				Repository: repository.FullName,
				Number:     number,
			})
		} else {
			updated, err = q.ReopenWorkstream(ctx, store.ReopenWorkstreamParams{Repository: repository.FullName, Number: number})
		}
		if err != nil || updated == 0 {
			return err
		}
		reopened = true
		if err := detachIssueNode(ctx, q, repository.FullName, number, false); err != nil {
			return err
		}
		changeID, err = addFieldChange(ctx, q, repository.FullName, number, stateField)
		return err
	}
	copyFailed := false
	if copyTree {
		before := e.labelChangesNow()
		tree, treeErr := e.readTree(ctx, repository, number)
		if treeErr == nil {
			err = e.writeRead(ctx, before, []copiedTree{tree}, func(q *store.Queries) error {
				if err := reopenTx(q); err != nil || !reopened {
					return err
				}
				return replaceTree(ctx, q, repository.FullName, number, tree)
			})
		} else {
			log.Printf("copy the Workstream %s#%d: %v", repository.FullName, number, treeErr)
			copyFailed = true
			err = e.inTx(ctx, func(q *store.Queries) error {
				if err := reopenTx(q); err != nil {
					return err
				}
				return deleteUnusedIssues(ctx, q)
			})
		}
	} else {
		err = e.inTx(ctx, func(q *store.Queries) error {
			if err := reopenTx(q); err != nil {
				return err
			}
			return deleteUnusedIssues(ctx, q)
		})
	}
	if err != nil || !reopened {
		return copyFailed, err
	}
	e.publish(Change{Workstreams: true})
	if err := e.sendNow(ctx, repository, changeID); err != nil {
		return copyFailed, err
	}
	return copyFailed, e.addLeadEvent(ctx, repository.FullName, number, "", sql.NullInt64{}, "reopen", eventText(time.Now(), "reopen of Workstream", fmt.Sprintf("#%d", number), issue, actor, issue.GetBody()))
}

func detachIssueNode(ctx context.Context, q *store.Queries, repository string, number int64, cleanup bool) error {
	node, err := q.GetIssueTreeNode(ctx, store.GetIssueTreeNodeParams{Repository: repository, Number: number})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := detachIssueSubtreeTx(ctx, q, node.ID); err != nil {
		return err
	}
	if cleanup {
		return deleteUnusedIssues(ctx, q)
	}
	return nil
}

// addWorkstreamFromEvent adds the row of the open Workstream issue if missing, copies its tree when copyTree is true,
// and adds a creation event for the Lead when it added the row or when no creation event exists yet. It tells if the
// tree copy failed.
func (e *Engine) addWorkstreamFromEvent(ctx context.Context, repository github.Repository, issue *gh.Issue, actor string, copyTree bool) (bool, error) {
	if issue.GetState() != "open" || !hasLabel(issue, workstreamLabel) {
		return false, nil
	}
	number := int64(issue.GetNumber())
	var added int64
	copyFailed := false
	var err error
	if copyTree {
		before := e.labelChangesNow()
		tree, treeErr := e.readTree(ctx, repository, number)
		if treeErr == nil {
			err = e.writeRead(ctx, before, []copiedTree{tree}, func(q *store.Queries) error {
				added, err = e.addWorkstreamRow(ctx, q, repository.FullName, issue)
				if err != nil {
					return err
				}
				if err := detachIssueNode(ctx, q, repository.FullName, number, false); err != nil {
					return err
				}
				return replaceTree(ctx, q, repository.FullName, number, tree)
			})
			if err == nil {
				e.publish(Change{Workstreams: true})
			}
		} else {
			log.Printf("copy the Workstream %s#%d: %v", repository.FullName, number, treeErr)
			copyFailed = true
			err = e.inTx(ctx, func(q *store.Queries) error {
				added, err = e.addWorkstreamRow(ctx, q, repository.FullName, issue)
				if err != nil {
					return err
				}
				return detachIssueNode(ctx, q, repository.FullName, number, true)
			})
			if err == nil && added > 0 {
				e.publish(Change{Workstreams: true})
			}
		}
	} else {
		err = e.inTx(ctx, func(q *store.Queries) error {
			added, err = e.addWorkstreamRow(ctx, q, repository.FullName, issue)
			if err != nil {
				return err
			}
			return detachIssueNode(ctx, q, repository.FullName, number, true)
		})
		if err == nil && added > 0 {
			e.publish(Change{Workstreams: true})
		}
	}
	if err != nil {
		return copyFailed, err
	}
	addCreation := added > 0
	if !addCreation {
		hasCreation, err := e.queries.HasLeadEventKind(ctx, store.HasLeadEventKindParams{Repository: repository.FullName, Workstream: number, Kind: "creation"})
		if err != nil {
			return copyFailed, err
		}
		addCreation = !hasCreation
	}
	if addCreation {
		if err := e.addLeadEvent(ctx, repository.FullName, number, "", sql.NullInt64{}, "creation", eventText(time.Now(), "creation of Workstream", fmt.Sprintf("#%d", number), issue, actor, issue.GetBody())); err != nil {
			return copyFailed, err
		}
	}
	return copyFailed, nil
}

// removeWorkstream deletes the row of the Workstream number of repository, removes its copied tree and owned data,
// rebuilds blocked Workstreams, and stops its live work.
func (e *Engine) removeWorkstream(ctx context.Context, repository github.Repository, number int64) error {
	deleted := false
	err := e.inTx(ctx, func(q *store.Queries) error {
		if err := removeTree(ctx, q, repository.FullName, number); err != nil {
			return err
		}
		n, err := q.DeleteCopiedWorkstream(ctx, store.DeleteCopiedWorkstreamParams{Repository: repository.FullName, Number: number})
		if err != nil || n == 0 {
			return err
		}
		deleted = true
		if err := deleteWorkstreamRecurringTasks(ctx, q, repository.FullName, number); err != nil {
			return err
		}
		return q.DeleteWorkstreamMcpServers(ctx, store.DeleteWorkstreamMcpServersParams{Repository: repository.FullName, Workstream: number})
	})
	if err != nil {
		return err
	}
	if deleted {
		e.publish(Change{Workstreams: true})
		if err := e.recopyAfterRemoval(ctx, repository, number); err != nil {
			log.Printf("copy the trees after the removal of the Workstream %s#%d: %v", repository.FullName, number, err)
		}
	}
	return e.stopWorkstream(ctx, repository, number)
}

// WorkstreamDetails is what the local copy knows about an open Workstream.
type WorkstreamDetails struct {
	CreatedAt      time.Time
	CompletedTasks int64
	OpenTasks      int64
	Evergreen      bool
	Maintenance    bool
	Skills         bool
	// Lead is the binding of the next Lead session of the Workstream.
	Lead NextSetting
	// Operator is the binding of the next Operator session of the Workstream.
	Operator NextSetting
	// Implementer is the binding of the next Implementer session of a task with no setting of its own.
	Implementer NextSetting
	// McpServers are the ids of the MCP servers that the Owner selected for the Workstream, in name order of the servers.
	McpServers []int64
}

// treeIssue is an issue of the tree of a Workstream, with the values of its row in issues.
type treeIssue struct {
	number int64
	title  string
	state  string
	author string
	kind   string
	labels []string
	triage bool
	// blocked is true when the issue has an open blocker.
	blocked bool
}

// walkTasks calls visit for each issue of the tree of the Workstream number of repository, depth first in the order of
// the tree, with the repository of the issue. The tree is the rows of issues, so a nested Workstream and the issues
// below it are no part of it. An issue in a repository that the App cannot read, the issues below it, and an issue with
// no number are no tasks. When visit gives true, the walk ends and walkTasks gives true.
func (e *Engine) walkTasks(ctx context.Context, repository github.Repository, number int64, visit func(github.Repository, treeIssue) (bool, error)) (bool, error) {
	var rows []store.ListCopiedTreeRow
	var blockedIDs []int64
	err := e.readTx(ctx, func(q *store.Queries) error {
		var err error
		rows, err = q.ListCopiedTree(ctx, store.ListCopiedTreeParams{Repository: repository.FullName, Number: number})
		if err != nil {
			return err
		}
		blockedIDs, err = q.ListOpenBlockedIssues(ctx, store.ListOpenBlockedIssuesParams{Repository: repository.FullName, Number: number})
		return err
	})
	if err != nil {
		return false, err
	}
	tree, err := orderTree(rows)
	if err != nil {
		return false, err
	}
	skipped := map[int64]bool{}
	for _, row := range tree {
		source, ok := e.namedRepository(row.IssueRepository, repository)
		if !ok || row.Parent.Valid && skipped[row.Parent.Int64] {
			skipped[row.ID] = true
			continue
		}
		if row.Number == 0 {
			continue
		}
		issue := treeIssue{
			number:  row.Number,
			title:   row.Title,
			state:   row.State,
			author:  row.Author,
			kind:    row.Kind,
			labels:  row.labels,
			triage:  row.Triage,
			blocked: slices.Contains(blockedIDs, row.ID),
		}
		if done, err := visit(source, issue); err != nil || done {
			return done, err
		}
	}
	return false, nil
}

// copiedTask is a task in the copied tree of a Workstream.
type copiedTask struct {
	repository string
	number     int64
	title      string
	state      string
	url        string
	bucket     bool
}

// afterCopiedTasksTree is a hook of the tests. copiedTasks calls it after ListCopiedTree.
var afterCopiedTasksTree func()

// copiedTasks gives the buckets of the Workstream number of repository, and then the tasks of its copied tree in the
// order of the tree. It leaves out an issue in a repository that the App cannot read. It keeps a bucket, because
// closeWorkstream closes it.
func (e *Engine) copiedTasks(ctx context.Context, repository github.Repository, number int64) ([]copiedTask, error) {
	var rows []store.ListCopiedTreeRow
	var storedBuckets string
	err := e.readTx(ctx, func(q *store.Queries) error {
		var err error
		rows, err = q.ListCopiedTree(ctx, store.ListCopiedTreeParams{Repository: repository.FullName, Number: number})
		if err != nil {
			return err
		}
		if afterCopiedTasksTree != nil {
			afterCopiedTasksTree()
		}
		storedBuckets, err = q.GetCopiedBuckets(ctx, store.GetCopiedBucketsParams{Repository: repository.FullName, Number: number})
		return err
	})
	if err != nil {
		return nil, err
	}
	tree, err := orderTree(rows)
	if err != nil {
		return nil, err
	}
	var buckets []copiedBucket
	if err := json.Unmarshal([]byte(storedBuckets), &buckets); err != nil {
		return nil, err
	}
	var tasks []copiedTask
	for _, bucket := range buckets {
		tasks = append(tasks, copiedTask{repository.FullName, bucket.Number, bucket.Title, bucket.State, issueLink(repository.FullName, bucket.Number), true})
	}
	for _, row := range tree {
		if row.Number == 0 {
			continue
		}
		source, ok := e.namedRepository(row.IssueRepository, repository)
		if !ok {
			continue
		}
		tasks = append(tasks, copiedTask{source.FullName, row.Number, row.Title, row.State, issueLink(source.FullName, row.Number), false})
	}
	return tasks, nil
}

// WorkstreamDetails reads the open Workstream number of repository from the local copy. A task is an issue in the
// sub-issue tree of the Workstream, also an issue of another repository. A nested Workstream and the issues below it
// are no tasks.
func (e *Engine) WorkstreamDetails(ctx context.Context, repositoryName string, number int64) (WorkstreamDetails, error) {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return WorkstreamDetails{}, err
	}
	copied, err := e.queries.GetCopiedWorkstream(ctx, store.GetCopiedWorkstreamParams{Repository: repository.FullName, Number: number})
	if errors.Is(err, sql.ErrNoRows) {
		return WorkstreamDetails{}, refuse("The issue is not an open Workstream.")
	}
	if err != nil {
		return WorkstreamDetails{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, copied.CreatedAt)
	if err != nil {
		return WorkstreamDetails{}, err
	}
	tasks, err := e.copiedTasks(ctx, repository, number)
	if err != nil {
		return WorkstreamDetails{}, err
	}
	details := WorkstreamDetails{CreatedAt: createdAt, Evergreen: copied.Evergreen, Maintenance: copied.Maintenance, Skills: copied.Skills}
	if details.Lead, err = e.nextSetting(ctx, "lead", repository.FullName, number); err != nil {
		return WorkstreamDetails{}, err
	}
	if details.Operator, err = e.nextSetting(ctx, "operator", repository.FullName, number); err != nil {
		return WorkstreamDetails{}, err
	}
	if details.Implementer, err = e.nextSetting(ctx, "implementer", repository.FullName, number); err != nil {
		return WorkstreamDetails{}, err
	}
	if details.McpServers, err = e.workstreamMcpServerIDs(ctx, repository.FullName, number); err != nil {
		return WorkstreamDetails{}, err
	}
	for _, task := range tasks {
		if task.bucket {
			continue
		}
		if task.state == "open" {
			details.OpenTasks++
		} else {
			details.CompletedTasks++
		}
	}
	return details, nil
}

// ClosureItem is an issue or a pull request that the close of a Workstream as "won't do" closes.
type ClosureItem struct {
	PullRequest bool
	Repository  string
	Number      int64
	Title       string
	URL         string
}

// WorkstreamClosure gives the items that CloseWorkstreamWontDo closes for the open Workstream number of repository.
func (e *Engine) WorkstreamClosure(ctx context.Context, repositoryName string, number int64) ([]ClosureItem, error) {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return nil, err
	}
	return e.closureItems(ctx, repository, number)
}

// CloseWorkstreamWontDo closes the open Workstream number of repository and the items of WorkstreamClosure as "won't
// do", also when the Workstream has open issues. Then it does the work of a close on GitHub.
func (e *Engine) CloseWorkstreamWontDo(ctx context.Context, repositoryName string, number int64) error {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return err
	}
	if _, err := e.openWorkstream(ctx, repository, number); err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	defer e.beginClose(repository.FullName, number)()
	if err := e.stopWorkstream(ctx, repository, number); err != nil {
		return err
	}
	items, err := e.closureItems(ctx, repository, number)
	if err != nil {
		return err
	}
	for _, item := range items {
		source, err := e.repository(item.Repository)
		if err != nil {
			return err
		}
		workstream := fmt.Sprintf("#%d", number)
		if item.Repository != repository.FullName {
			workstream = fmt.Sprintf("%s#%d", repository.FullName, number)
		}
		if err := e.postCommentOnce(ctx, source, item.Number, fmt.Sprintf("The Workstream %s closed as \"won't do\".", workstream)); err != nil {
			return err
		}
		switch {
		case item.PullRequest:
			err = source.ClosePullRequest(ctx, item.Number)
		case item.Repository == repository.FullName && item.Number == number:
			// The issue can be closed on GitHub after a failed call, so the own write stays.
			if _, ownErr := addOwnWrite(ctx, e.queries, repository.FullName, number, stateField, "", closedChange); ownErr != nil {
				return ownErr
			}
			_, err = source.CloseIssue(ctx, item.Number, "not_planned")
		default:
			err = e.closeTreeIssue(ctx, source, item.Number, "not_planned")
		}
		if err != nil {
			return err
		}
	}
	if err := e.closeCopiedWorkstream(ctx, repository, number, closedNotPlanned); err != nil {
		return err
	}
	if err := e.closeWorkstream(ctx, repository, number); err != nil {
		return err
	}
	if err := e.removeClosedWorkstreamTree(ctx, repository.FullName, number); err != nil {
		return err
	}
	if err := e.queries.ClearWorkstreamClosePending(ctx, store.ClearWorkstreamClosePendingParams{Repository: repository.FullName, Number: number}); err != nil {
		return err
	}
	if err := e.startCurator(ctx, repository.FullName); err != nil {
		log.Printf("start a Curator of %s: %v", repository.FullName, err)
	}
	e.addRecord(ctx, leadChat(repository.FullName, number), "You closed the Workstream as won't do.")
	return nil
}

// beginClose hides the Workstream number of repository from the Workstream list. The returned function shows it
// again, unless the close removed it from the copy.
func (e *Engine) beginClose(repository string, number int64) (end func()) {
	e.setClosing(pullKey{repository, number}, true)
	return func() { e.setClosing(pullKey{repository, number}, false) }
}

func (e *Engine) setClosing(key pullKey, closing bool) {
	e.closingMu.Lock()
	if closing {
		e.closing[key] = true
	} else {
		delete(e.closing, key)
	}
	e.closingMu.Unlock()
	e.publish(Change{Workstreams: true})
}

// IsClosing tells if the close of the Workstream number of repository runs.
func (e *Engine) IsClosing(repository string, number int64) bool {
	e.closingMu.Lock()
	defer e.closingMu.Unlock()
	return e.closing[pullKey{repository, number}]
}

func (e *Engine) openWorkstream(ctx context.Context, repository github.Repository, number int64) (*gh.Issue, error) {
	issue, err := repository.Issue(ctx, number)
	if err != nil {
		return nil, err
	}
	if issue == nil || issue.GetState() != "open" || !hasLabel(issue, workstreamLabel) {
		return nil, refuse("The issue is not an open Workstream.")
	}
	return issue, nil
}

// closureItems gives the open items that a close as "won't do" closes: the open pull requests of the tasks, also
// when the issue of the task is closed, the open task issues, and the Workstream issue last. It reads the issues below
// the Workstream from the local copy, as closeWorkstream walks them.
func (e *Engine) closureItems(ctx context.Context, repository github.Repository, workstream int64) ([]ClosureItem, error) {
	copied, err := e.queries.GetCopiedWorkstream(ctx, store.GetCopiedWorkstreamParams{Repository: repository.FullName, Number: workstream})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, refuse("The issue is not an open Workstream.")
	}
	if err != nil {
		return nil, err
	}
	tasks, err := e.copiedTasks(ctx, repository, workstream)
	if err != nil {
		return nil, err
	}
	rows, err := e.queries.ListTaskPullRequests(ctx, store.ListTaskPullRequestsParams{WorkstreamRepository: repository.FullName, Workstream: workstream})
	if err != nil {
		return nil, err
	}
	var items []ClosureItem
	var seen []pullKey
	for _, row := range rows {
		key := pullKey{row.Repository, row.PullRequest}
		if slices.Contains(seen, key) {
			continue
		}
		seen = append(seen, key)
		source, err := e.repository(row.Repository)
		if err != nil {
			return nil, err
		}
		pullRequest, err := source.Issue(ctx, row.PullRequest)
		if err != nil {
			return nil, err
		}
		if pullRequest.GetState() == "open" {
			items = append(items, closureItem(row.Repository, pullRequest, true))
		}
	}
	for _, task := range tasks {
		if task.state == "open" {
			items = append(items, ClosureItem{Repository: task.repository, Number: task.number, Title: task.title, URL: task.url})
		}
	}
	return append(items, ClosureItem{Repository: repository.FullName, Number: workstream, Title: copied.Title, URL: issueLink(repository.FullName, workstream)}), nil
}

func closureItem(repository string, issue *gh.Issue, pullRequest bool) ClosureItem {
	return ClosureItem{PullRequest: pullRequest, Repository: repository, Number: int64(issue.GetNumber()), Title: issue.GetTitle(), URL: issue.GetHTMLURL()}
}

// closeWorkstream ends the work of the Workstream, sets its Autopilot level to Off, removes mobius:question,
// dismisses the Inbox items of its issues, closes the open pull requests of its
// tasks, and closes the issues below it as not planned. A nested Workstream and the issues below it stay open.
func (e *Engine) closeWorkstream(ctx context.Context, repository github.Repository, workstream int64) error {
	if err := e.deleteRecurringTasks(ctx, repository.FullName, workstream); err != nil {
		return err
	}
	if err := e.stopWorkstream(ctx, repository, workstream); err != nil {
		return err
	}
	// The row of workstreams stays after the close, so a reopened Workstream starts with the level Off.
	if _, err := e.setLevel(ctx, repository, workstream, levelOff); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := repository.RemoveLabel(ctx, workstream, questionLabel); err != nil {
		return err
	}
	if err := e.dismissIssueItems(ctx, repository.FullName, workstream, inboxKinds...); err != nil {
		return err
	}
	pullRequests, err := e.queries.ListTaskPullRequests(ctx, store.ListTaskPullRequestsParams{WorkstreamRepository: repository.FullName, Workstream: workstream})
	if err != nil {
		return err
	}
	for _, row := range pullRequests {
		source, err := e.repository(row.Repository)
		if err != nil {
			return err
		}
		pullRequest, err := source.Issue(ctx, row.PullRequest)
		if err != nil {
			return err
		}
		if pullRequest.GetState() != "open" {
			continue
		}
		if err := e.postCommentOnce(ctx, source, row.PullRequest, closedText); err != nil {
			return err
		}
		if err := source.ClosePullRequest(ctx, row.PullRequest); err != nil {
			return err
		}
	}
	_, err = e.walkTasks(ctx, repository, workstream, func(source github.Repository, issue treeIssue) (bool, error) {
		return false, e.closeBelowWorkstream(ctx, source, issue.number, issue.labels, issue.state == "open")
	})
	return err
}

// closeBelowWorkstream dismisses the Inbox items of the issue number below a closing Workstream, removes its Mobius
// labels among labels, and, when open is true and the issue is still open, closes it as not planned.
func (e *Engine) closeBelowWorkstream(ctx context.Context, source github.Repository, number int64, labels []string, open bool) error {
	if err := e.dismissIssueItems(ctx, source.FullName, number, inboxKinds...); err != nil {
		return err
	}
	if err := e.clearStart(ctx, source.FullName, number); err != nil {
		return err
	}
	for _, label := range []string{workingLabel, needsHumanLabel, questionLabel, reviewLabel} {
		if !slices.Contains(labels, label) {
			continue
		}
		if err := source.RemoveLabel(ctx, number, label); err != nil {
			return err
		}
	}
	if !open {
		return nil
	}
	current, err := source.Issue(ctx, number)
	if err != nil || current == nil || current.GetState() != "open" {
		return err
	}
	if err := e.postCommentOnce(ctx, source, number, closedText); err != nil {
		return err
	}
	return e.closeTreeIssue(ctx, source, number, "not_planned")
}

// closeUncopiedSubIssues closes each open sub-issue on GitHub that has no row in issues, at each depth below the
// Workstream and below each open issue of its copied tree, as closeBelowWorkstream does. A poll that sees a new
// sub-issue and the close of its Workstream together does not copy the sub-issue, because the tree check skips a
// closed Workstream, and the tree check does not read below a closed issue. As in the tree check, an issue stays open
// when the actor of its newest add as a sub-issue is not trusted, and the sub-issues of that issue stay open. An issue
// that was added as a sub-issue at the close of the Workstream or later stays open. It runs before closeWorkstream,
// which sets the rows of the tree to closed, so a retry after a failed step reads the same open rows.
func (e *Engine) closeUncopiedSubIssues(ctx context.Context, repository github.Repository, workstream int64) error {
	issue, err := repository.Issue(ctx, workstream)
	if err != nil {
		return err
	}
	closedAt := issue.GetClosedAt().Time
	if closedAt.IsZero() {
		closedAt = time.Now()
	}
	if err := e.closeSubIssuesOf(ctx, repository, workstream, closedAt, repository, workstream); err != nil {
		return err
	}
	_, err = e.walkTasks(ctx, repository, workstream, func(source github.Repository, issue treeIssue) (bool, error) {
		if issue.state != "open" {
			return false, nil
		}
		return false, e.closeSubIssuesOf(ctx, repository, workstream, closedAt, source, issue.number)
	})
	return err
}

// closeSubIssuesOf closes the sub-issues of the issue parent of from that closeUncopiedSubIssues closes. It closes the
// sub-issues of a sub-issue first, so a retry after a failed close reaches them.
func (e *Engine) closeSubIssuesOf(ctx context.Context, repository github.Repository, workstream int64, closedAt time.Time, from github.Repository, parent int64) error {
	var leaves copiedTree
	subIssues, err := e.flatSubIssues(ctx, from, parent, 0, false, &leaves)
	if err != nil {
		return err
	}
	bucketRefs := map[issueRef]bool{}
	for _, bucket := range leaves.buckets {
		bucketRefs[lowerRef(from.FullName, bucket.Number)] = true
	}
	eventsOf := func(repo github.Repository, number int64) ([]github.TreeEvent, error) {
		return repo.TreeEvents(ctx, number)
	}
	for _, sub := range subIssues {
		if sub.issue.GetState() != "open" {
			continue
		}
		source, readable := e.issueRepository(sub.issue, sub.from)
		if !readable {
			continue
		}
		number := int64(sub.issue.GetNumber())
		_, inTree, err := e.treeIssueOf(ctx, source.FullName, number)
		if err != nil {
			return err
		}
		if inTree {
			continue
		}
		added, parentRepository, parentNumber, found, err := findAddedParentEvent(from, parent, from.FullName, lowerRef(from.FullName, parent), sub, source.FullName, number, source, readable, bucketRefs, repository, workstream, eventsOf)
		if err != nil {
			return err
		}
		actor := added.Actor.Login
		if !found || !added.CreatedAt.Before(closedAt) || !e.isTreeAppActor(repository, actor, source.FullName, parentMetadata{parentRepository, parentNumber}) && !e.trustedUser(actor) {
			continue
		}
		if err := e.closeSubIssuesOf(ctx, repository, workstream, closedAt, source, number); err != nil {
			return err
		}
		if err := e.closeBelowWorkstream(ctx, source, number, labelNames(sub.issue), true); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) closeTreeIssue(ctx context.Context, repository github.Repository, number int64, reason string) error {
	ownWrite, err := addOwnWrite(ctx, e.queries, repository.FullName, number, stateField, "", closedChange)
	if err != nil {
		return err
	}
	if _, err := repository.CloseIssue(ctx, number, reason); err != nil {
		if delErr := e.queries.DeleteOwnWrite(ctx, ownWrite); delErr != nil {
			return errors.Join(err, delErr)
		}
		return err
	}
	_, err = e.queries.SetIssueStateByRepoNumber(ctx, store.SetIssueStateByRepoNumberParams{
		State:      "closed",
		Repository: repository.FullName,
		Number:     number,
	})
	return err
}

// stopWorkstream stops the Lead and ends the live tasks of the Workstream. The queued events of the Lead go to no
// later Lead.
func (e *Engine) stopWorkstream(ctx context.Context, repository github.Repository, workstream int64) error {
	e.stopLead(repository.FullName, workstream)
	err := e.queries.DeliverLeadEvents(ctx, store.DeliverLeadEventsParams{
		DeliveredAt: sql.NullString{String: now(), Valid: true},
		Repository:  repository.FullName,
		Workstream:  workstream,
	})
	if err != nil {
		return err
	}
	tasks, err := e.queries.ListWorkstreamLiveTasks(ctx, store.ListWorkstreamLiveTasksParams{WorkstreamRepository: repository.FullName, Workstream: workstream})
	if err != nil {
		return err
	}
	for _, task := range tasks {
		source, err := e.repository(task.Repository)
		if err != nil {
			return err
		}
		if err := e.endTask(ctx, source, task); err != nil {
			return err
		}
	}
	return nil
}

// hasWork tells if the Workstream has a Lead or live tasks. An issue that lost mobius:workstream can still have them.
func (e *Engine) hasWork(ctx context.Context, repository string, workstream int64) (bool, error) {
	if e.hasLead(repository, workstream) {
		return true, nil
	}
	tasks, err := e.queries.ListWorkstreamLiveTasks(ctx, store.ListWorkstreamLiveTasksParams{WorkstreamRepository: repository, Workstream: workstream})
	return len(tasks) > 0, err
}

// SetAutopilot sets the Autopilot level of the open Workstream number of repository. It changes workstreams and acts
// at once. The send of the outgoing changes writes the labels.
func (e *Engine) SetAutopilot(ctx context.Context, repositoryName string, number int64, level string) error {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return err
	}
	copied, err := e.queries.GetCopiedWorkstream(ctx, store.GetCopiedWorkstreamParams{Repository: repository.FullName, Number: number})
	if errors.Is(err, sql.ErrNoRows) {
		return refuse("The issue is not an open Workstream.")
	}
	if err != nil {
		return err
	}
	if err := e.changeLevel(ctx, repository, number, copied.Title, level, "the Owner"); err != nil {
		return err
	}
	if err := e.applyLevel(ctx, repository.FullName, number, level); err != nil {
		return err
	}
	e.addRecord(ctx, leadChat(repository.FullName, number), fmt.Sprintf("You set the Autopilot level to %s.", levelNames[level]))
	return nil
}

// changeLevel is the handler of a change of the Autopilot level of the Workstream number of repository to level. It
// sets the level and publishes the change. A level that differs from the old level also adds an event for the Lead.
// title is the title of the Workstream. actor names who changed the level. The caller acts on the live tasks
// (applyLevel).
func (e *Engine) changeLevel(ctx context.Context, repository github.Repository, number int64, title, level, actor string) error {
	changed, err := e.setLevel(ctx, repository, number, level)
	if err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	if !changed {
		return nil
	}
	text := fmt.Sprintf("%s Autopilot of Workstream #%d \"%s\" is \"%s\", by %s.", time.Now().UTC().Format(timeFormat), number, title, levelNames[level], actor)
	return e.addLeadEvent(ctx, repository.FullName, number, "", sql.NullInt64{}, "autopilot", text)
}

// setLevel sets the Autopilot level of the Workstream number of repository in workstreams, adds the outgoing changes
// that write mobius:autopilot and mobius:auto-merge in the same transaction, and sends them. It tells if the level
// differs from the old level. A Workstream with no row gives sql.ErrNoRows.
func (e *Engine) setLevel(ctx context.Context, repository github.Repository, number int64, level string) (bool, error) {
	var ids []int64
	changed := false
	err := e.inTx(ctx, func(q *store.Queries) error {
		before, err := q.GetWorkstreamSettings(ctx, store.GetWorkstreamSettingsParams{Repository: repository.FullName, Number: number})
		if err != nil {
			return err
		}
		changed = AutopilotLevel(before.Autopilot, before.AutoMerge) != level
		err = q.SetCopiedAutopilot(ctx, store.SetCopiedAutopilotParams{Autopilot: startsTasks(level), AutoMerge: mergesPullRequests(level), Repository: repository.FullName, Number: number})
		if err != nil {
			return err
		}
		for _, field := range []string{autopilotField, autoMergeField} {
			id, err := addSettingChange(ctx, q, repository.FullName, number, field)
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if err := e.sendNow(ctx, repository, id); err != nil {
			return false, err
		}
	}
	return changed, nil
}

// SetEvergreen makes the open Workstream number of repository evergreen or not evergreen. It refuses the Maintenance
// Workstream and the Skills Workstream, because Mobius manages them.
func (e *Engine) SetEvergreen(ctx context.Context, repositoryName string, number int64, on bool) error {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return err
	}
	copied, err := e.queries.GetCopiedWorkstream(ctx, store.GetCopiedWorkstreamParams{Repository: repository.FullName, Number: number})
	if errors.Is(err, sql.ErrNoRows) {
		return refuse("The issue is not an open Workstream.")
	}
	if err != nil {
		return err
	}
	if copied.Maintenance {
		return refuse("#%d is the Maintenance Workstream. Mobius manages it, so its evergreen value cannot change.", number)
	}
	if copied.Skills {
		return refuse("#%d is the Skills Workstream. Mobius manages it, so its evergreen value cannot change.", number)
	}
	if err := e.setEvergreen(ctx, repository, number, on); err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	state := "off"
	if on {
		state = "on"
	}
	e.addRecord(ctx, leadChat(repository.FullName, number), fmt.Sprintf("You turned Evergreen %s.", state))
	return nil
}

// setEvergreen is the handler of a change of the evergreen setting of the Workstream number of repository. It sets the
// value in workstreams, adds the outgoing change that writes mobius:evergreen in the same transaction, and sends it.
func (e *Engine) setEvergreen(ctx context.Context, repository github.Repository, number int64, on bool) error {
	var id int64
	err := e.inTx(ctx, func(q *store.Queries) error {
		if err := q.SetCopiedEvergreen(ctx, store.SetCopiedEvergreenParams{Evergreen: on, Repository: repository.FullName, Number: number}); err != nil {
			return err
		}
		var err error
		id, err = addSettingChange(ctx, q, repository.FullName, number, evergreenField)
		return err
	})
	if err != nil {
		return err
	}
	return e.sendNow(ctx, repository, id)
}

// setMaintenance is the handler of a change of the maintenance setting of the Workstream number of repository. It sets
// the value in workstreams, adds the outgoing change that writes mobius:maintenance in the same transaction, and sends
// it. A Maintenance Workstream is evergreen, so a value on with evergreen off also sets evergreen on and adds its
// outgoing change.
func (e *Engine) setMaintenance(ctx context.Context, repository github.Repository, number int64, on bool) error {
	var ids []int64
	err := e.inTx(ctx, func(q *store.Queries) error {
		if err := q.SetCopiedMaintenance(ctx, store.SetCopiedMaintenanceParams{Maintenance: on, Repository: repository.FullName, Number: number}); err != nil {
			return err
		}
		id, err := addSettingChange(ctx, q, repository.FullName, number, maintenanceField)
		if err != nil {
			return err
		}
		ids = append(ids, id)
		settings, err := q.GetWorkstreamSettings(ctx, store.GetWorkstreamSettingsParams{Repository: repository.FullName, Number: number})
		if err != nil || !on || settings.Evergreen {
			return err
		}
		if err := q.SetCopiedEvergreen(ctx, store.SetCopiedEvergreenParams{Evergreen: true, Repository: repository.FullName, Number: number}); err != nil {
			return err
		}
		id, err = addSettingChange(ctx, q, repository.FullName, number, evergreenField)
		ids = append(ids, id)
		return err
	})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := e.sendNow(ctx, repository, id); err != nil {
			return err
		}
	}
	return nil
}

// setSkills is the handler of a change of the skills setting of the Workstream number of repository. It sets the value
// in workstreams, adds the outgoing change that writes mobius:skills in the same transaction, and sends it.
func (e *Engine) setSkills(ctx context.Context, repository github.Repository, number int64, on bool) error {
	var id int64
	err := e.inTx(ctx, func(q *store.Queries) error {
		if err := q.SetCopiedSkills(ctx, store.SetCopiedSkillsParams{Skills: on, Repository: repository.FullName, Number: number}); err != nil {
			return err
		}
		var err error
		id, err = addSettingChange(ctx, q, repository.FullName, number, skillsField)
		return err
	})
	if err != nil {
		return err
	}
	return e.sendNow(ctx, repository, id)
}

// The Autopilot levels. mobius:autopilot is the start part of a level, and mobius:auto-merge is the merge part.
const (
	levelOff           = "off"
	levelStart         = "start"
	levelMerge         = "merge"
	levelStartAndMerge = "start_and_merge"
)

var levelNames = map[string]string{
	levelOff:           "Off",
	levelStart:         "Start tasks",
	levelMerge:         "Merge only",
	levelStartAndMerge: "Start and merge",
}

// AutopilotLevel gives the level of mobius:autopilot and mobius:auto-merge.
func AutopilotLevel(autopilot, autoMerge bool) string {
	switch {
	case autopilot && autoMerge:
		return levelStartAndMerge
	case autopilot:
		return levelStart
	case autoMerge:
		return levelMerge
	}
	return levelOff
}

func startsTasks(level string) bool {
	return level == levelStart || level == levelStartAndMerge
}

func mergesPullRequests(level string) bool {
	return level == levelMerge || level == levelStartAndMerge
}

// workstreamLevel gives the Autopilot level of the copy for the Workstream number.
func (e *Engine) workstreamLevel(ctx context.Context, repository string, number int64) (string, error) {
	row, err := e.queries.GetCopiedAutopilot(ctx, store.GetCopiedAutopilotParams{Repository: repository, Number: number})
	if errors.Is(err, sql.ErrNoRows) {
		return levelOff, nil
	}
	return AutopilotLevel(row.Autopilot, row.AutoMerge), err
}

// eventText gives the text of a Lead event about the issue with the reference ref, for example "creation of
// Workstream", with the quoted body, for example the body of the issue or of a comment.
func eventText(t time.Time, what, ref string, issue *gh.Issue, actor, body string) string {
	return fmt.Sprintf("%s %s %s \"%s\" by @%s:\n\n%s", t.UTC().Format(timeFormat), what, ref, issue.GetTitle(), actor, quote(body))
}

// eventRef gives the reference of an issue or a pull request in the repository in a Lead event text of the
// Workstream in workstreamRepository: #N in the Workstream repository, else owner/name#N.
func eventRef(workstreamRepository, repository string, number int64) string {
	if repository == workstreamRepository {
		return fmt.Sprintf("#%d", number)
	}
	return fmt.Sprintf("%s#%d", repository, number)
}

// taskEventRef gives the reference of an issue or a pull request of the task, as eventRef does.
func taskEventRef(task store.Task, number int64) string {
	return eventRef(task.WorkstreamRepository, task.Repository, number)
}
