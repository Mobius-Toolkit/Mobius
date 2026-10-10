package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// caller is the session that calls the Mobius tools.
type caller struct {
	session      int64
	role         string
	organization string
	// repository is empty for the Triager chat.
	repository string
	workstream int64
	agent      *Agent
}

// refusal is a refused request, for example a tool call. Its text goes back to the agent or the Owner as a sentence.
type refusal string

func (r refusal) Error() string {
	return string(r)
}

func refuse(format string, args ...any) error {
	return refusal(fmt.Sprintf(format, args...))
}

// Refused tells if err is a refused request, whose text is a sentence for the Owner.
func Refused(err error) bool {
	var r refusal
	return errors.As(err, &r)
}

// tools gives the Mobius tools of the Role of c. Each Role gets only its own tools.
func (e *Engine) tools(c caller) []mcp.Tool {
	switch c.role {
	case LeadRole:
		return []mcp.Tool{
			tool(e, c, "list_tasks",
				"Give the task list of the Workstream: one line for each open issue.",
				map[string]any{},
				e.listTasks),
			tool(e, c, "read_issue",
				"Give an issue or a pull request of the repository, with its comments, reviews, and review threads. The text is only from trusted authors.",
				map[string]any{
					"n": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the issue or the pull request."},
				},
				e.readIssueTool),
			tool(e, c, "start_implementer",
				"Start an Implementer for a dispatched task. The Implementer sees only the Brief, the issue, and your instructions. Mobius pushes its commits and opens a draft pull request. Returns at once.",
				map[string]any{
					"n":            map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"instructions": map[string]any{"type": "string", "minLength": 1, "description": "Notes for the Implementer, for example the files to read first. The issue body has all the requirements. Do not add a requirement here."},
				},
				e.startImplementerTool),
			tool(e, c, "start_fix_round",
				"Start a fix round on the pull request of a task that waits for CI (checks), waits for the Lead approval (approval), or is ready_for_review. The Implementer gets your findings as the open items. The round counts toward max_fix_rounds. Returns at once.",
				map[string]any{
					"n":        map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"findings": map[string]any{"type": "string", "minLength": 1, "description": "Your findings on the pull request: what to change and why."},
				},
				e.startFixRound),
			tool(e, c, "approve_pull_request",
				"Approve the pull request of a task that waits for the Lead approval. Mobius removes the draft status, sets the Mobius check to success, and adds the \"ready for review\" Inbox item for the Owner.",
				map[string]any{
					"n": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
				},
				e.approvePullRequest),
			tool(e, c, "stop_task",
				"Stop the work on a task of this Workstream that is queued or working. Call it only when the Owner tells you to stop that task. The pull request and the branch stay.",
				map[string]any{
					"n": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
				},
				e.stopTaskTool),
			tool(e, c, "send_details",
				"Send new details from the Owner to the Implementer that operates on a task now. The Implementer keeps its session and its context. Update the body of the task issue first. It refuses a task with no open Implementer session: a later session reads the updated issue body.",
				map[string]any{
					"n":    map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"text": map[string]any{"type": "string", "minLength": 1, "description": "The new details."},
				},
				e.sendDetails),
			tool(e, c, "start_researcher",
				"Start a Researcher that answers a question about the code of the default branch. The Researcher sees only the Brief and the question. The tool returns the id of the Researcher at once, and the report arrives later.",
				map[string]any{
					"question": map[string]any{"type": "string", "minLength": 1, "description": "The question, with the context that the Researcher needs."},
				},
				e.startResearcher),
			tool(e, c, "send_researcher_details",
				"Send new details from the Owner to a Researcher that runs now. The Researcher keeps its session and its context. The report of the Researcher answers the new details. It refuses a Researcher that does not run.",
				map[string]any{
					"id":   map[string]any{"type": "integer", "minimum": 1, "description": "The id of the Researcher, from start_researcher or from its report."},
					"text": map[string]any{"type": "string", "minLength": 1, "description": "The new details."},
				},
				e.sendResearcherDetails),
			tool(e, c, "stop_researcher",
				"Stop a Researcher that runs now. The Researcher gives no report. Call it only when the Owner tells you to.",
				map[string]any{
					"id": map[string]any{"type": "integer", "minimum": 1, "description": "The id of the Researcher, from start_researcher or from its report."},
				},
				e.stopResearcher),
			tool(e, c, "ask",
				"Ask the people on a task issue a question. Mobius posts the question as a comment, adds mobius:question, and adds an Inbox item for the Owner. The reply arrives later as an event.",
				map[string]any{
					"n":    map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"text": map[string]any{"type": "string", "minLength": 1, "description": "The question for the people on the issue."},
				},
				e.ask),
			tool(e, c, "hold_task",
				"Stop a dispatched task before its Implementer starts, because the task has a problem. The task waits for the Owner and holds no Worker slot. Mobius posts the reason as a comment, adds mobius:needs-human, and adds an Inbox item for the Owner. After the Owner resumes the task, you get a dispatch event again.",
				map[string]any{
					"n":      map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"reason": map[string]any{"type": "string", "minLength": 1, "description": "The question or the explanation for the Owner."},
				},
				e.holdTask),
			tool(e, c, "decline",
				"Decline a task. Mobius posts the reason as a comment on the issue, removes mobius:working, and ends the task.",
				map[string]any{
					"n":      map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"reason": map[string]any{"type": "string", "minLength": 1, "description": "The reason for the people on the issue."},
				},
				e.decline),
			tool(e, c, "create_issue",
				"Create an issue below an issue of the Workstream. Mobius adds the blockers as native issue dependencies.",
				map[string]any{
					"title":      map[string]any{"type": "string", "minLength": 1, "description": "The title of the issue."},
					"body":       map[string]any{"type": "string", "description": "The body of the issue, with the sections Goal, Today (optional), Change, Limits, and Done, in this order."},
					"parent":     map[string]any{"type": "integer", "minimum": 1, "description": "The Workstream issue or an issue below it."},
					"blocked_by": map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 1}, "description": "The issues that block this issue. They can be in another Workstream."},
				},
				e.createIssue),
			tool(e, c, "mark_ready",
				"Add mobius:ready to an issue of the Workstream. Use it when the Owner tells you to start an issue. For a task that waits for a human, with Autopilot on, it removes mobius:needs-human instead, so the task continues.",
				map[string]any{
					"n": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the issue."},
				},
				e.markReady),
			tool(e, c, "reply_thread",
				"Reply in a review thread or to a conversation comment of the pull request of a task, for example with the link to a follow-up issue.",
				map[string]any{
					"thread": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the thread or the comment in the event."},
					"text":   map[string]any{"type": "string", "minLength": 1, "description": "A follow-up link, an answer, or a reason to reject. Do not write an acknowledgement."},
				},
				e.replyThread),
			tool(e, c, "comment_pull_request",
				"Post a comment on the pull request of a task, for example to propose that a human closes a stale pull request.",
				map[string]any{
					"n":    map[string]any{"type": "integer", "minimum": 1, "description": "The number of the pull request."},
					"text": map[string]any{"type": "string", "minLength": 1, "description": "The comment."},
				},
				e.commentPullRequest),
			tool(e, c, "create_workstream",
				"Create a Workstream in this repository: an issue with mobius:workstream. Call it only after the Owner approves the exact title and Brief in the chat.",
				workstreamProperties,
				e.createWorkstream),
			tool(e, c, "move_task",
				"Make a task of this Workstream a sub-issue of a different open Workstream in this repository. Call it only after the Owner approves the move in the chat. The task must not be in progress.",
				map[string]any{
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"workstream": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the target Workstream issue."},
				},
				e.moveTask),
			tool(e, c, "message_lead",
				"Send a message to the Lead of a different open Workstream in this repository. Call it only after the Owner approves the target Workstream and the exact message in the chat.",
				map[string]any{
					"workstream": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the target Workstream issue."},
					"text":       map[string]any{"type": "string", "minLength": 1, "description": "The message for the Lead."},
				},
				e.messageLead),
			tool(e, c, "hold_event",
				"Hold the event of this turn until the Owner decides. Mobius sends the event again after the end of your next reply to the Owner. A later event of the same task issue waits behind it. Call it only in a turn for an event.",
				map[string]any{},
				e.holdEvent),
			tool(e, c, "tell_owner",
				"Tell the Owner something. Mobius adds the text to the Lead chat and adds an Inbox item.",
				map[string]any{
					"text": map[string]any{"type": "string", "minLength": 1, "description": "The text for the Owner."},
				},
				e.tellOwner),
		}
	case ImplementerRole:
		return []mcp.Tool{
			tool(e, c, "cannot_do",
				"Tell the Lead that you cannot do the task. Mobius ends your turn and pushes nothing. Use it only for a task that you cannot do. Do not use it to wait, for example for a background command or for the check. Mobius refuses it when your worktree has work that Mobius did not push.",
				map[string]any{
					"reason": map[string]any{"type": "string", "minLength": 1, "description": "The reason for the Lead."},
				},
				e.cannotDo),
			tool(e, c, "reply_thread",
				"Reply in a review thread of the pull request in a fix round. Mobius posts the reply after it pushes your commits, so the SHA of a fix commit in the text links to a pushed commit. Mobius resolves the thread after the reply.",
				map[string]any{
					"thread": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the thread or the comment in the prompt. Mobius cannot resolve a conversation comment."},
					"text":   map[string]any{"type": "string", "minLength": 1, "description": "The SHA of the fix commit, an answer, a follow-up link, or a reason to reject. Do not write an acknowledgement."},
				},
				e.holdReply),
		}
	case ReviewerRole:
		return []mcp.Tool{
			tool(e, c, "submit_review",
				"Post your review on the pull request as one GitHub review with inline comments. Call it one time. With no findings and no follow-ups, do not call it.",
				map[string]any{
					"body": map[string]any{"type": "string", "minLength": 1, "description": "The summary of the review."},
					"comments": map[string]any{
						"type":        "array",
						"description": "One inline comment for each finding and each follow-up.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"path":      map[string]any{"type": "string", "minLength": 1, "description": "The file path, relative to the repository root."},
								"line":      map[string]any{"type": "integer", "minimum": 1, "description": "The line in the new version of the file. It must be in the diff."},
								"body":      map[string]any{"type": "string", "minLength": 1, "description": "The finding."},
								"follow_up": map[string]any{"type": "boolean", "description": "True for a correct finding outside the scope of the task. The Lead decides what to do with it. The default is false."},
							},
							"required":             []string{"path", "line", "body"},
							"additionalProperties": false,
						},
					},
				},
				e.submitReview),
		}
	case JudgeRole:
		return []mcp.Tool{
			tool(e, c, "submit_verdicts",
				"Give the actions for each item of the batch, one entry for each item. Items of trusted users take fix, question, and follow-up. Items of trusted bots take fix and reject. A later valid call replaces an earlier one.",
				map[string]any{
					"items": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"item": map[string]any{"type": "integer", "description": "The number of the thread or the comment in the prompt."},
								"actions": map[string]any{
									"type":     "array",
									"minItems": 1,
									"items": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"verdict": map[string]any{"type": "string", "enum": []string{fixVerdict, questionVerdict, followUpVerdict, rejectVerdict}},
											"text":    map[string]any{"type": "string", "minLength": 1, "description": "For fix and question, the work for the Implementer. For follow-up, the goal of the new issue. For reject, the reason for the author."},
										},
										"required":             []string{"verdict", "text"},
										"additionalProperties": false,
									},
								},
							},
							"required":             []string{"item", "actions"},
							"additionalProperties": false,
						},
					},
				},
				e.submitVerdicts),
		}
	case CuratorRole:
		return []mcp.Tool{
			tool(e, c, "edit_memory",
				"Change one part of the memory file of the repository. Give an old text that occurs one time in the file, and the new text. An empty old text adds the new text at the end of the file. An empty new text removes the old text. Give the reason of the change. The tool refuses an empty reason and a result of more than 200 lines.",
				map[string]any{
					"old":    map[string]any{"type": "string", "description": "The text to replace. It occurs one time in the memory file. Empty to add the new text at the end."},
					"new":    map[string]any{"type": "string", "description": "The replacement text. Empty to remove the old text."},
					"reason": map[string]any{"type": "string", "minLength": 1, "description": "Why you make the change. Name the type of change (add, merge, change or remove) and the evidence, for example the Workstream, the issue or the pull request."},
				},
				e.editMemory),
		}
	case TriagerRole:
		return []mcp.Tool{
			tool(e, c, "create_workstream",
				"Create a Workstream: an issue with mobius:workstream. In the chat, call it only after the Owner approves the exact title and Brief.",
				workstreamProperties,
				e.createWorkstream),
			tool(e, c, "move_issue",
				"Make an issue a sub-issue of an open Workstream. For an issue with mobius:no-workstream, Mobius then adds mobius:ready again.",
				map[string]any{
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the issue."},
					"workstream": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the Workstream issue."},
				},
				e.moveIssue),
			tool(e, c, "message_lead",
				"Send a message to the Lead of an open Workstream. In the chat, call it only after the Owner selects the Workstream and approves the exact message.",
				map[string]any{
					"workstream": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the Workstream issue that the Owner selects."},
					"text":       map[string]any{"type": "string", "minLength": 1, "description": "The message for the Lead."},
				},
				e.messageLead),
			tool(e, c, "start_researcher",
				"Start a Researcher that answers a question about the code of the default branch. The Researcher sees only the question. The tool returns at once, and the report arrives later.",
				map[string]any{
					"question": map[string]any{"type": "string", "minLength": 1, "description": "The question, with the context that the Researcher needs."},
				},
				e.startResearcher),
			toolOf(e, c, noRepository, "tell_curator",
				"Send a request to the Curator of a repository. The Curator changes the memory file of the repository. In the chat, call it only after the Owner approves the exact message. The tool returns at once, and the result arrives later.",
				map[string]any{
					"repository": map[string]any{"type": "string", "minLength": 1, "description": "The full name of the repository (owner/name) in the organization of the chat."},
					"text":       map[string]any{"type": "string", "minLength": 1, "description": "The request for the Curator."},
				},
				e.tellCurator),
		}
	}
	return nil
}

var workstreamProperties = map[string]any{
	"title": map[string]any{"type": "string", "minLength": 1, "description": "The name of the Workstream."},
	"brief": map[string]any{"type": "string", "minLength": 1, "description": "The Brief: the goal, the scope, and the limits of the Workstream."},
}

// tool gives the Mobius tool name of c. The tool decodes the arguments into In for run, and adds the call
// with its result or its error to the Transcript of the session.
func tool[In any](e *Engine, c caller, name, description string, properties map[string]any, run func(context.Context, caller, github.Repository, In) (string, error)) mcp.Tool {
	return toolOf(e, c, e.callerRepository, name, description, properties, run)
}

// noRepository is the resolve of a tool that takes the repository as an input. The run of the tool gets the zero
// Repository.
func noRepository(caller) (github.Repository, error) {
	return github.Repository{}, nil
}

// toolOf is tool for a tool whose run gets the repository that resolve gives.
func toolOf[In any](e *Engine, c caller, resolve func(caller) (github.Repository, error), name, description string, properties map[string]any, run func(context.Context, caller, github.Repository, In) (string, error)) mcp.Tool {
	return mcp.Tool{
		Name:        name,
		Description: description,
		Properties:  properties,
		Run: func(ctx context.Context, arguments json.RawMessage) (string, error) {
			c.agent.touch()
			text, err := call(ctx, c, resolve, name, arguments, run)
			if recordErr := e.recordCall(ctx, c.session, name, arguments, text, err); recordErr != nil {
				return "", recordErr
			}
			return text, err
		},
	}
}

func call[In any](ctx context.Context, c caller, resolve func(caller) (github.Repository, error), name string, arguments json.RawMessage, run func(context.Context, caller, github.Repository, In) (string, error)) (string, error) {
	repository, err := resolve(c)
	if err != nil {
		return "", err
	}
	var input In
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return "", refuse("Invalid arguments for %s: %v.", name, err)
	}
	return run(ctx, c, repository, input)
}

func (e *Engine) recordCall(ctx context.Context, session int64, name string, arguments json.RawMessage, text string, err error) error {
	row := struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
		Result    *string         `json:"result,omitempty"`
		Error     *string         `json:"error,omitempty"`
	}{Tool: name, Arguments: arguments}
	if err != nil {
		row.Error = new(err.Error())
	} else {
		row.Result = &text
	}
	data, marshalErr := compact(row)
	if marshalErr != nil {
		return marshalErr
	}
	return e.addRow(ctx, session, "mcp_call", data, false)
}

// repository gives the repository name of the last read of the Mobius Apps.
func (e *Engine) repository(name string) (github.Repository, error) {
	repository, ok := e.github.Repository(name)
	if !ok {
		return github.Repository{}, refuse("The Mobius App has no access to %s.", name)
	}
	return repository, nil
}

// callerRepository gives the repository of c. The Triager chat has no repository, so it uses the one repository of its organization.
func (e *Engine) callerRepository(c caller) (github.Repository, error) {
	if c.repository != "" {
		return e.repository(c.repository)
	}
	var found []github.Repository
	for _, repository := range e.github.Repositories() {
		if repository.Owner() == c.organization {
			found = append(found, repository)
		}
	}
	if len(found) != 1 {
		return github.Repository{}, refuse("The Triager chat needs exactly one repository in %s.", c.organization)
	}
	return found[0], nil
}

type noInput struct{}

type numberInput struct {
	N int64 `json:"n"`
}

type textInput struct {
	N    int64  `json:"n"`
	Text string `json:"text"`
}

type tellInput struct {
	Text string `json:"text"`
}

type threadInput struct {
	Thread int64  `json:"thread"`
	Text   string `json:"text"`
}

type issueInput struct {
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	Parent    int64   `json:"parent"`
	BlockedBy []int64 `json:"blocked_by"`
}

type workstreamInput struct {
	Title string `json:"title"`
	Brief string `json:"brief"`
}

type moveInput struct {
	N          int64 `json:"n"`
	Workstream int64 `json:"workstream"`
}

type messageLeadInput struct {
	Workstream int64  `json:"workstream"`
	Text       string `json:"text"`
}

type implementerInput struct {
	N            int64  `json:"n"`
	Instructions string `json:"instructions"`
}

type findingsInput struct {
	N        int64  `json:"n"`
	Findings string `json:"findings"`
}

type questionInput struct {
	Question string `json:"question"`
}

type declineInput struct {
	N      int64  `json:"n"`
	Reason string `json:"reason"`
}

type reasonInput struct {
	Reason string `json:"reason"`
}

type reviewInput struct {
	Body     string                 `json:"body"`
	Comments []github.InlineComment `json:"comments"`
}

type verdictsInput struct {
	Items []itemVerdicts `json:"items"`
}

func empty(text string) bool {
	return strings.TrimSpace(text) == ""
}

func (e *Engine) listTasks(ctx context.Context, c caller, repository github.Repository, _ noInput) (string, error) {
	lines, err := e.taskLines(ctx, repository, c.workstream)
	return taskText(lines), err
}

func (e *Engine) readIssueTool(ctx context.Context, _ caller, repository github.Repository, input numberInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	return e.readIssue(ctx, repository, input.N)
}

func (e *Engine) createIssue(ctx context.Context, c caller, repository github.Repository, input issueInput) (string, error) {
	if empty(input.Title) {
		return "", refuse("title must not be empty.")
	}
	if input.Parent < 1 || slices.ContainsFunc(input.BlockedBy, func(number int64) bool { return number < 1 }) {
		return "", refuse("parent and each blocked_by must be 1 or more.")
	}
	if input.Parent != c.workstream {
		in, err := inWorkstream(ctx, repository, c.workstream, input.Parent)
		if err != nil {
			return "", err
		}
		if !in {
			return "", refuse("#%d is not in this Workstream.", input.Parent)
		}
	}
	var blockerIDs []int64
	for _, number := range input.BlockedBy {
		blocker, err := repository.Issue(ctx, number)
		if err != nil {
			return "", err
		}
		if blocker == nil || blocker.IsPullRequest() {
			return "", refuse("#%d is not an issue of the repository.", number)
		}
		if slices.Contains(blockerIDs, blocker.GetID()) {
			return "", refuse("#%d is two times in blocked_by.", number)
		}
		blockerIDs = append(blockerIDs, blocker.GetID())
	}
	owner, name := repository.Owner(), repository.Name()
	issue, _, err := repository.Client.Issues.Create(ctx, owner, name, gh.CreateIssueRequest{Title: input.Title, Body: &input.Body})
	if err != nil {
		return "", err
	}
	if _, _, err := repository.Client.SubIssue.Add(ctx, owner, name, input.Parent, gh.SubIssueRequest{SubIssueID: issue.GetID(), ReplaceParent: new(true)}); err != nil {
		return "", err
	}
	for _, id := range blockerIDs {
		if _, _, err := repository.Client.Issues.AddBlockedBy(ctx, owner, name, int64(issue.GetNumber()), gh.IssueDependencyRequest{IssueID: id}); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("Created #%d.", issue.GetNumber()), nil
}

func (e *Engine) stopTaskTool(ctx context.Context, c caller, repository github.Repository, input numberInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	task, err := e.workstreamTask(ctx, repository, c.workstream, input.N)
	if err != nil {
		return "", err
	}
	if task.State != "queued" && task.State != "working" {
		return "", refuse("The task of #%d is %s, so it has no work to stop.", input.N, task.State)
	}
	issue, err := existingIssue(ctx, repository, input.N)
	if err != nil {
		return "", err
	}
	text := fmt.Sprintf("Stopped \"%s\" on request of the Owner", issue.GetTitle())
	if err := e.stopTask(ctx, repository, task, issue, appLogin(repository.AppSlug), "Stopped by the Owner.", text); err != nil {
		return "", err
	}
	return fmt.Sprintf("Stopped the task of #%d.", input.N), nil
}

func (e *Engine) markReady(ctx context.Context, c caller, repository github.Repository, input numberInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	issue, err := repository.Issue(ctx, input.N)
	if err != nil {
		return "", err
	}
	if issue == nil || !e.TrustedAuthor(repository.AppSlug, issue.GetUser().GetLogin()) {
		return "", refuse("#%d is not an issue of a trusted author.", input.N)
	}
	in, err := inWorkstream(ctx, repository, c.workstream, input.N)
	if err != nil {
		return "", err
	}
	if !in {
		return "", refuse("#%d is not in this Workstream.", input.N)
	}
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: input.N})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil && task.State == "needs_human" {
		on, err := e.workstreamAutopilot(ctx, repository, c.workstream)
		if err != nil {
			return "", err
		}
		if on {
			if err := removeNeedsHuman(ctx, repository, task); err != nil {
				return "", err
			}
			return fmt.Sprintf("Removed mobius:needs-human from #%d, so the task continues.", input.N), nil
		}
	}
	if _, _, err := repository.Client.Issues.AddLabelsToIssue(ctx, repository.Owner(), repository.Name(), int(input.N), []string{readyLabel}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Marked #%d ready.", input.N), nil
}

func (e *Engine) replyThread(ctx context.Context, c caller, repository github.Repository, input threadInput) (string, error) {
	if empty(input.Text) {
		return "", refuse("text must not be empty.")
	}
	tasks, err := e.queries.ListLiveTasks(ctx, c.repository)
	if err != nil {
		return "", err
	}
	for _, task := range tasks {
		if task.Workstream != c.workstream || !task.PullRequest.Valid {
			continue
		}
		replied, err := reply(ctx, repository, task.PullRequest.Int64, input.Thread, input.Text)
		if err != nil {
			return "", err
		}
		if replied {
			return fmt.Sprintf("Replied to %d.", input.Thread), nil
		}
	}
	return "", refuse("%d is not a review thread or a comment of a pull request of a live task in this Workstream.", input.Thread)
}

func (e *Engine) commentPullRequest(ctx context.Context, c caller, repository github.Repository, input textInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	if empty(input.Text) {
		return "", refuse("text must not be empty.")
	}
	task, err := e.queries.GetLiveTaskByPullRequest(ctx, store.GetLiveTaskByPullRequestParams{
		Repository:  c.repository,
		PullRequest: sql.NullInt64{Int64: input.N, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && task.Workstream != c.workstream) {
		return "", refuse("#%d is not the pull request of a live task in this Workstream.", input.N)
	}
	if err != nil {
		return "", err
	}
	if _, _, err := repository.Client.Issues.CreateComment(ctx, repository.Owner(), repository.Name(), int(input.N), gh.IssueCommentRequest{Body: input.Text}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Commented on #%d.", input.N), nil
}

func (e *Engine) createWorkstream(ctx context.Context, c caller, repository github.Repository, input workstreamInput) (string, error) {
	if c.role != LeadRole && c.repository != "" {
		return "", refuse("Only the Triager chat or the Lead chat creates a Workstream, after the Owner approves it.")
	}
	if empty(input.Title) || empty(input.Brief) {
		return "", refuse("title and brief must not be empty.")
	}
	e.copyWrite.Lock()
	defer e.copyWrite.Unlock()
	owner, name := repository.Owner(), repository.Name()
	issue, _, err := repository.Client.Issues.Create(ctx, owner, name, gh.CreateIssueRequest{Title: input.Title, Body: &input.Brief})
	if err != nil {
		return "", err
	}
	if issue.Labels, _, err = repository.Client.Issues.AddLabelsToIssue(ctx, owner, name, issue.GetNumber(), []string{workstreamLabel}); err != nil {
		return "", err
	}
	if _, err := e.updateCopy(ctx, repository, issue, nil, false); err != nil {
		log.Printf("copy the new Workstream %s#%d: %v", repository.FullName, issue.GetNumber(), err)
	}
	e.publish(Change{Workstreams: true})
	if c.role == TriagerRole {
		e.publish(Change{Created: &Created{repository.FullName, int64(issue.GetNumber())}})
	}
	return fmt.Sprintf("Created the Workstream #%d.", issue.GetNumber()), nil
}

// openWorkstream checks that the issue number is an open Workstream issue.
func openWorkstream(ctx context.Context, repository github.Repository, number int64) error {
	issue, err := repository.Issue(ctx, number)
	if err != nil {
		return err
	}
	if issue == nil || issue.GetState() != "open" || !hasLabel(issue, workstreamLabel) {
		return refuse("#%d is not an open Workstream.", number)
	}
	return nil
}

// issueOf gives the issue number of repository. A pull request is not an issue.
func issueOf(ctx context.Context, repository github.Repository, number int64) (*gh.Issue, error) {
	issue, err := repository.Issue(ctx, number)
	if err != nil {
		return nil, err
	}
	if issue == nil || issue.IsPullRequest() {
		return nil, refuse("#%d is not an issue of %s.", number, repository.FullName)
	}
	return issue, nil
}

func (e *Engine) moveTask(ctx context.Context, c caller, repository github.Repository, input moveInput) (string, error) {
	if input.N < 1 || input.Workstream < 1 {
		return "", refuse("n and workstream must be 1 or more.")
	}
	issue, err := issueOf(ctx, repository, input.N)
	if err != nil {
		return "", err
	}
	in, err := inWorkstream(ctx, repository, c.workstream, input.N)
	if err != nil {
		return "", err
	}
	if !in {
		return "", refuse("#%d is not in this Workstream.", input.N)
	}
	if input.Workstream == c.workstream {
		return "", refuse("#%d is this Workstream.", input.Workstream)
	}
	if err := openWorkstream(ctx, repository, input.Workstream); err != nil {
		return "", err
	}
	_, err = e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: input.N})
	if err == nil {
		return "", refuse("#%d has a live task. Stop the task first.", input.N)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if _, _, err := repository.Client.SubIssue.Add(ctx, repository.Owner(), repository.Name(), input.Workstream, gh.SubIssueRequest{SubIssueID: issue.GetID(), ReplaceParent: new(true)}); err != nil {
		return "", err
	}
	if err := e.recopyTrees(ctx, repository, c.workstream, input.Workstream); err != nil {
		log.Printf("copy the trees of %s#%d and #%d: %v", repository.FullName, c.workstream, input.Workstream, err)
	}
	return fmt.Sprintf("Moved #%d to the Workstream #%d.", input.N, input.Workstream), nil
}

func (e *Engine) moveIssue(ctx context.Context, _ caller, repository github.Repository, input moveInput) (string, error) {
	if input.N < 1 || input.Workstream < 1 {
		return "", refuse("n and workstream must be 1 or more.")
	}
	issue, err := issueOf(ctx, repository, input.N)
	if err != nil {
		return "", err
	}
	of, err := workstreamOf(ctx, repository, input.N)
	if err != nil {
		return "", err
	}
	if of != 0 {
		return "", refuse("#%d is already in a Workstream.", input.N)
	}
	if err := openWorkstream(ctx, repository, input.Workstream); err != nil {
		return "", err
	}
	owner, name := repository.Owner(), repository.Name()
	if _, _, err := repository.Client.SubIssue.Add(ctx, owner, name, input.Workstream, gh.SubIssueRequest{SubIssueID: issue.GetID(), ReplaceParent: new(true)}); err != nil {
		return "", err
	}
	if hasLabel(issue, noWorkstreamLabel) {
		if _, err := repository.Client.Issues.RemoveLabelForIssue(ctx, owner, name, int(input.N), noWorkstreamLabel); err != nil {
			return "", err
		}
		if _, _, err := repository.Client.Issues.AddLabelsToIssue(ctx, owner, name, int(input.N), []string{readyLabel}); err != nil {
			return "", err
		}
	}
	if err := e.recopyTrees(ctx, repository, input.Workstream); err != nil {
		log.Printf("copy the tree of %s#%d: %v", repository.FullName, input.Workstream, err)
	}
	return fmt.Sprintf("Moved #%d to the Workstream #%d.", input.N, input.Workstream), nil
}

func (e *Engine) messageLead(ctx context.Context, c caller, repository github.Repository, input messageLeadInput) (string, error) {
	kind, sender := "triager", "the Triager"
	if c.role == LeadRole {
		kind, sender = "lead", fmt.Sprintf("the Lead of #%d", c.workstream)
		if input.Workstream == c.workstream {
			return "", refuse("A Lead cannot send a message to its own Workstream.")
		}
	} else if c.repository != "" {
		return "", refuse("Only the Triager chat sends a message to a Lead, after the Owner approves it.")
	}
	if empty(input.Text) {
		return "", refuse("text must not be empty.")
	}
	if err := openWorkstream(ctx, repository, input.Workstream); err != nil {
		return "", err
	}
	text := fmt.Sprintf("Message of %s, approved by the Owner:\n\n%s", sender, input.Text)
	if err := e.addLeadEvent(ctx, repository.FullName, input.Workstream, sql.NullInt64{}, kind, text); err != nil {
		return "", err
	}
	return fmt.Sprintf("Sent the message to the Lead of #%d.", input.Workstream), nil
}
