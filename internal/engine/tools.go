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
	// repository is empty for the Coordinator chat.
	repository string
	workstream int64
	// issue is the issue of the session, or 0.
	issue int64
	agent *Agent
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
				"Start a fix round on the pull request of a task that waits for CI (checks), waits for the Lead approval (approval), or is ready_for_review. The Implementer gets your findings as the open items. For a task in working or reviewed, Mobius keeps your findings, and the next fix round gets them with its other items. The round counts toward max_fix_rounds. Returns at once.",
				map[string]any{
					"n":        map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"findings": map[string]any{"type": "string", "minLength": 1, "description": "Your findings on the pull request: what to change and why."},
				},
				e.startFixRound),
			tool(e, c, "approve_pull_request",
				"Approve the pull request of a task that waits for the Lead approval. Mobius posts the report as a comment on the pull request, removes the draft status, sets the Mobius check to success, and adds the \"ready for review\" Inbox item for the Owner. The item links to the comment.",
				map[string]any{
					"n":      map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"report": map[string]any{"type": "string", "minLength": 1, "description": "Your approval report in two parts: what you checked, and what you verified and how."},
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
				"Ask the people on a task issue, or on the Workstream issue, a question. Use it when you need a decision of the Owner. For a choice between alternatives, give the questions with their options in questions, and the context in text. Mobius posts the text and the questions as a comment, adds mobius:question, and adds an Inbox item for the Owner. The reply arrives later as an event. On a dispatched task, the task frees its Autopilot slot until the Owner replies. Then you get a dispatch event again.",
				map[string]any{
					"n":         map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue, or of the Workstream issue."},
					"text":      map[string]any{"type": "string", "minLength": 1, "description": "The question for the people on the issue. With questions, the context of the questions."},
					"questions": questionsProperty,
				},
				e.ask),
			tool(e, c, "close_question",
				"Close your question on a task issue or on the Workstream issue when the Owner answers it in the chat. Mobius posts the answer as a comment on the issue, removes mobius:question, and dismisses the question Inbox item. On a task that waits for the answer, the task continues, and you get a dispatch event with \"answer of\".",
				map[string]any{
					"n":      map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue, or of the Workstream issue."},
					"answer": map[string]any{"type": "string", "minLength": 1, "description": "The answer of the Owner."},
				},
				e.closeQuestionTool),
			toolOf(e, c, noRepository, "withdraw", withdrawDescription,
				map[string]any{
					"id": map[string]any{"type": "integer", "minimum": 1, "description": "The ID of your open question or pending action."},
				},
				e.withdraw),
			tool(e, c, "decline",
				"Decline a task. Mobius posts the reason as a comment on the issue, removes mobius:working, and ends the task.",
				map[string]any{
					"n":      map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"reason": map[string]any{"type": "string", "minLength": 1, "description": "The reason for the people on the issue."},
				},
				e.decline),
			tool(e, c, "close_task",
				"Close a task issue that has no work left. Mobius posts the reason as a comment on the issue. For a live task, Mobius closes its open pull request and ends the task. Then Mobius removes mobius:ready and closes the issue as not planned. The branch stays.",
				map[string]any{
					"n":      map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"reason": map[string]any{"type": "string", "minLength": 1, "description": "The reason for the people on the issue."},
				},
				e.closeTask),
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
				"Add mobius:ready to an issue of the Workstream. Use it when the Owner tells you to start an issue. For a stopped task, it continues the task. The round counters keep their values, so a task at max_fix_rounds stops again.",
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
			e.createWorkstreamTool(c),
			e.moveTaskTool(c),
			e.messageLeadTool(c),
			e.editBriefTool(c),
			tool(e, c, "hold_event",
				"Hold the event of this turn until the Owner decides. Mobius sends the event again after the end of your next reply to a chat message of the Owner, or after the Owner answers a question on an issue of the Workstream. A later event of the same task issue waits behind it. Call it only in a turn for an event.",
				map[string]any{},
				e.holdEvent),
			tool(e, c, "tell_owner",
				"Tell the Owner something, for example a result. Mobius adds the text to the Lead chat and adds no Inbox item. For a decision of the Owner, call ask.",
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
				"Reply in a review thread or to a conversation comment of the pull request in a fix round. Commit the fix before you reply about it. When your worktree has work that Mobius did not push, Mobius posts the reply after it pushes your commits, so the SHA of a fix commit in the text links to a pushed commit. Else Mobius posts the reply at once. Mobius resolves the thread after the reply.",
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
				"Give the actions for each item of the batch, one entry for each item. Items of trusted users take fix, question, and follow-up. Items of trusted bots take fix, follow-up, and reject. A later valid call replaces an earlier one.",
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
		tools := []mcp.Tool{
			tool(e, c, "edit_memory",
				"Change one part of the memory file of the repository. Give an old text that occurs one time in the file, and the new text. An empty old text adds the new text at the end of the file. An empty new text removes the old text. Give the reason of the change. The tool refuses an empty reason and a result of more than 200 lines.",
				map[string]any{
					"old":    map[string]any{"type": "string", "description": "The text to replace. It occurs one time in the memory file. Empty to add the new text at the end."},
					"new":    map[string]any{"type": "string", "description": "The replacement text. Empty to remove the old text."},
					"reason": map[string]any{"type": "string", "minLength": 1, "description": "Why you make the change. Name the type of change (add, merge, change or remove) and the evidence, for example the Workstream, the issue or the pull request."},
				},
				e.editMemory),
		}
		if c.agent.maintenance == 0 {
			return tools
		}
		return append(tools,
			tool(e, c, "search_issues",
				"Search the open and the closed issues of the repository. Give the words of the problem. For each closed issue, the result has the close reason and the last 3 comments of trusted authors. The result has only issues of trusted authors. It gives the first 20 matches.",
				map[string]any{
					"query": map[string]any{"type": "string", "minLength": 1, "description": "The words to search in the title and the body of the issues."},
				},
				e.searchIssues),
			tool(e, c, "create_issue",
				fmt.Sprintf("Create a task issue below the maintenance Workstream. Mobius adds mobius:triage, so the task starts only after the Owner accepts it. Mobius adds the blockers as native issue dependencies. Search the issues first. You can create a maximum of %d issues in a session.", maintenanceIssues),
				map[string]any{
					"title":      map[string]any{"type": "string", "minLength": 1, "description": "The title of the issue."},
					"body":       map[string]any{"type": "string", "description": "The body of the issue, with the sections Goal, Today (optional), Change, Limits, and Done, in this order."},
					"blocked_by": map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 1}, "description": "The issues that block this issue."},
				},
				e.createMaintenanceIssue))
	case DiagnoserRole:
		return []mcp.Tool{
			tool(e, c, "report_failure",
				"Report one failure that you found in the failed jobs. Call it one time for each different failure. Mobius gives the reason to the fix round of the pull request. When the pull request did not cause the failure, Mobius also saves it in a failure record and compares it with the same failure in other pull requests.",
				map[string]any{
					"reason":                 map[string]any{"type": "string", "minLength": 1, "description": "The cause of the failure."},
					"test":                   map[string]any{"type": "string", "description": "The full name of the failed test. Empty for a failure with no test, for example a lint step or a build step."},
					"location":               map[string]any{"type": "string", "description": "The file:line of the failure. Empty when the log does not show it."},
					"lines":                  map[string]any{"type": "string", "minLength": 1, "description": "The log lines that show the failure. Mobius keeps the last 100 lines."},
					"caused_by_pull_request": map[string]any{"type": "boolean", "description": "True when the change of the pull request caused the failure."},
					"same_record":            map[string]any{"type": "integer", "minimum": 0, "description": "The id of the open failure record that has the same failure. 0 when no record has it."},
				},
				e.reportFailure),
		}
	case TriagerRole:
		return []mcp.Tool{
			e.createWorkstreamTool(c),
			e.messageLeadTool(c),
			tool(e, c, "start_researcher",
				"Start a Researcher that answers a question about the code of the default branch. The Researcher sees only the question. The tool returns at once, and the report arrives later as a comment on the issue.",
				map[string]any{
					"question": map[string]any{"type": "string", "minLength": 1, "description": "The question, with the context that the Researcher needs."},
				},
				e.startResearcher),
		}
	case CoordinatorRole:
		return []mcp.Tool{
			e.createWorkstreamTool(c),
			moveIssueTool(e, c),
			e.messageLeadTool(c),
			tool(e, c, "start_researcher",
				"Start a Researcher that answers a question about the code of the default branch. The Researcher sees only the question. The tool returns at once, and the report arrives later.",
				map[string]any{
					"question": map[string]any{"type": "string", "minLength": 1, "description": "The question, with the context that the Researcher needs."},
				},
				e.startResearcher),
			e.tellCuratorTool(c),
			toolOf(e, c, noRepository, "ask",
				"Ask the Owner a question. Use it when you need a decision of the Owner. For a choice between alternatives, give the questions with their options in questions, and the context in text. Mobius adds an Inbox item for the Owner. The tool returns at once. The answer arrives later as a chat message of the Owner.",
				map[string]any{
					"text":      map[string]any{"type": "string", "minLength": 1, "description": "The question for the Owner. With questions, the context of the questions."},
					"questions": questionsProperty,
				},
				e.coordinatorAsk),
			toolOf(e, c, noRepository, "withdraw", withdrawDescription,
				map[string]any{
					"id": map[string]any{"type": "integer", "minimum": 1, "description": "The ID of your open question or pending action."},
				},
				e.withdraw),
		}
	}
	return nil
}

var questionsProperty = map[string]any{
	"type":        "array",
	"description": "Optional. Questions with options for the Owner to select.",
	"items": map[string]any{
		"type":     "object",
		"required": []string{"text", "options"},
		"properties": map[string]any{
			"text":     map[string]any{"type": "string", "minLength": 1, "description": "The question."},
			"multiple": map[string]any{"type": "boolean", "description": "True lets the Owner select more than one option. The default is false."},
			"options": map[string]any{
				"type":     "array",
				"minItems": 2,
				"items": map[string]any{
					"type":     "object",
					"required": []string{"title"},
					"properties": map[string]any{
						"title":   map[string]any{"type": "string", "minLength": 1, "description": "The short answer."},
						"details": map[string]any{"type": "string", "description": "The explanation of the option."},
					},
				},
			},
		},
	},
}

var workstreamProperties = map[string]any{
	"title": map[string]any{"type": "string", "minLength": 1, "description": "The name of the Workstream."},
	"brief": map[string]any{"type": "string", "minLength": 1, "description": "The Brief: the goal, the scope, and the limits of the Workstream."},
}

var coordinatorWorkstreamProperties = map[string]any{
	"title": workstreamProperties["title"],
	"brief": workstreamProperties["brief"],
	"labels": map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string", "enum": []string{evergreenLabel, maintenanceLabel}},
		"description": "More labels for the Workstream issue. Only mobius:evergreen and mobius:maintenance are allowed.",
	},
}

func moveIssueTool(e *Engine, c caller) mcp.Tool {
	return tool(e, c, "move_issue",
		"Make an issue a sub-issue of an open Workstream. For an issue with mobius:no-workstream, Mobius then adds mobius:ready again.",
		map[string]any{
			"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the issue."},
			"workstream": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the Workstream issue."},
		},
		e.moveIssue)
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
	input, err := decode[In](name, arguments)
	if err != nil {
		return "", err
	}
	return run(ctx, c, repository, input)
}

func decode[In any](name string, arguments json.RawMessage) (In, error) {
	var input In
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, refuse("Invalid arguments for %s: %v.", name, err)
	}
	return input, nil
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

// callerRepository gives the repository of c. The Coordinator chat has no repository, so it uses the one repository of its organization.
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
		return github.Repository{}, refuse("The Coordinator chat needs exactly one repository in %s.", c.organization)
	}
	return found[0], nil
}

type noInput struct{}

type numberInput struct {
	N int64 `json:"n"`
}

type approveInput struct {
	N      int64  `json:"n"`
	Report string `json:"report"`
}

type textInput struct {
	N    int64  `json:"n"`
	Text string `json:"text"`
}

type askInput struct {
	N         int64      `json:"n"`
	Text      string     `json:"text"`
	Questions []Question `json:"questions"`
}

// Question is a question of ask with its options. Mobius stores it as JSON in inbox_items.questions.
type Question struct {
	Text     string           `json:"text"`
	Multiple bool             `json:"multiple"`
	Options  []QuestionOption `json:"options"`
}

// QuestionOption is an option of a Question.
type QuestionOption struct {
	Title   string `json:"title"`
	Details string `json:"details"`
}

type tellInput struct {
	Text string `json:"text"`
}

type threadInput struct {
	Thread int64  `json:"thread"`
	Text   string `json:"text"`
}

type maintenanceIssueInput struct {
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	BlockedBy []int64 `json:"blocked_by"`
}

type queryInput struct {
	Query string `json:"query"`
}

type issueInput struct {
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	Parent    int64   `json:"parent"`
	BlockedBy []int64 `json:"blocked_by"`
}

type workstreamInput struct {
	Title  string   `json:"title"`
	Brief  string   `json:"brief"`
	Labels []string `json:"labels"`
}

type moveInput struct {
	N          int64 `json:"n"`
	Workstream int64 `json:"workstream"`
}

type editBriefInput struct {
	Brief  string `json:"brief"`
	Reason string `json:"reason"`
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

type answerInput struct {
	N      int64  `json:"n"`
	Answer string `json:"answer"`
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
	if err := checkIssue(input); err != nil {
		return "", err
	}
	if input.Parent != c.workstream {
		in, err := e.inWorkstream(ctx, repository, c.workstream, input.Parent)
		if err != nil {
			return "", err
		}
		if !in {
			return "", refuse("#%d is not in this Workstream.", input.Parent)
		}
	}
	number, err := e.addIssue(ctx, repository, c.workstream, input, nil)
	if err != nil {
		return "", err
	}
	if err := e.recopyTrees(ctx, repository, c.workstream); err != nil {
		log.Printf("copy the tree of %s#%d: %v", repository.FullName, c.workstream, err)
	}
	return fmt.Sprintf("Created #%d.", number), nil
}

// createMaintenanceIssue creates an issue with mobius:triage below the maintenance Workstream of the Curator c. It
// refuses an issue after the maximum of maintenanceIssues issues in the session.
func (e *Engine) createMaintenanceIssue(ctx context.Context, c caller, repository github.Repository, input maintenanceIssueInput) (string, error) {
	issue := issueInput{Title: input.Title, Body: input.Body, Parent: c.agent.maintenance, BlockedBy: input.BlockedBy}
	if err := checkIssue(issue); err != nil {
		return "", err
	}
	c.agent.issuesMu.Lock()
	defer c.agent.issuesMu.Unlock()
	if c.agent.issues == maintenanceIssues {
		return "", refuse("You created %d issues in this session. This is the maximum.", maintenanceIssues)
	}
	number, err := e.addIssue(ctx, repository, c.agent.maintenance, issue, []string{triageLabel})
	if err != nil {
		return "", err
	}
	c.agent.issues++
	return fmt.Sprintf("Created #%d.", number), nil
}

func checkIssue(input issueInput) error {
	if empty(input.Title) {
		return refuse("title must not be empty.")
	}
	if input.Parent < 1 || slices.ContainsFunc(input.BlockedBy, func(number int64) bool { return number < 1 }) {
		return refuse("parent and each blocked_by must be 1 or more.")
	}
	return nil
}

// addIssue creates an issue with labels below the issue input.Parent, with the blockers input.BlockedBy, and the link to
// the Lead chat of the Workstream, and gives its number. When GitHub created the issue but a later call failed, it gives the number with the error.
func (e *Engine) addIssue(ctx context.Context, repository github.Repository, workstream int64, input issueInput, labels []string) (int, error) {
	var blockerIDs []int64
	for _, number := range input.BlockedBy {
		blocker, err := repository.Issue(ctx, number)
		if err != nil {
			return 0, err
		}
		if blocker == nil || blocker.IsPullRequest() {
			return 0, refuse("#%d is not an issue of the repository.", number)
		}
		if slices.Contains(blockerIDs, blocker.GetID()) {
			return 0, refuse("#%d is two times in blocked_by.", number)
		}
		blockerIDs = append(blockerIDs, blocker.GetID())
	}
	owner, name := repository.Owner(), repository.Name()
	body := e.withChatLink(repository, workstream, input.Body)
	issue, _, err := repository.Client.Issues.Create(ctx, owner, name, gh.CreateIssueRequest{Title: input.Title, Body: &body, Labels: labels})
	if err != nil {
		return 0, err
	}
	if _, _, err := repository.Client.SubIssue.Add(ctx, owner, name, input.Parent, gh.SubIssueRequest{SubIssueID: issue.GetID(), ReplaceParent: new(true)}); err != nil {
		return issue.GetNumber(), fmt.Errorf("GitHub created #%d, but the add of #%d below #%d failed. Do not create the issue again: %w", issue.GetNumber(), issue.GetNumber(), input.Parent, err)
	}
	for i, id := range blockerIDs {
		if _, _, err := repository.Client.Issues.AddBlockedBy(ctx, owner, name, int64(issue.GetNumber()), gh.IssueDependencyRequest{IssueID: id}); err != nil {
			return issue.GetNumber(), fmt.Errorf("GitHub created #%d, but the add of the blocker #%d failed. Do not create the issue again: %w", issue.GetNumber(), input.BlockedBy[i], err)
		}
	}
	return issue.GetNumber(), nil
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
	in, err := e.inWorkstream(ctx, repository, c.workstream, input.N)
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
		if err := removeNeedsHuman(ctx, repository, task); err != nil {
			return "", err
		}
		return fmt.Sprintf("Removed mobius:needs-human from #%d, so the task continues.", input.N), nil
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
		if task.WorkstreamRepository != c.repository || task.Workstream != c.workstream || !task.PullRequest.Valid {
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
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (task.WorkstreamRepository != c.repository || task.Workstream != c.workstream)) {
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

func checkWorkstream(c caller, input workstreamInput) error {
	if empty(input.Title) || empty(input.Brief) {
		return refuse("title and brief must not be empty.")
	}
	if len(input.Labels) > 0 && c.role != CoordinatorRole {
		return refuse("Only the Coordinator adds labels to a Workstream.")
	}
	for _, label := range input.Labels {
		if label != evergreenLabel && label != maintenanceLabel {
			return refuse("The label %q is not allowed. Use %s or %s.", label, evergreenLabel, maintenanceLabel)
		}
	}
	return nil
}

func (e *Engine) createWorkstream(ctx context.Context, c caller, repository github.Repository, input workstreamInput) (string, error) {
	if err := checkWorkstream(c, input); err != nil {
		return "", err
	}
	e.copyWrite.Lock()
	defer e.copyWrite.Unlock()
	owner, name := repository.Owner(), repository.Name()
	action, approved := ctx.Value(approvedRun{}).(int64)
	var issue *gh.Issue
	if approved {
		var err error
		if issue, err = e.createdIssue(ctx, repository, action); err != nil {
			return "", err
		}
	}
	if issue == nil {
		var err error
		if issue, _, err = repository.Client.Issues.Create(ctx, owner, name, gh.CreateIssueRequest{Title: input.Title, Body: &input.Brief}); err != nil {
			return "", err
		}
		if approved {
			if err := e.queries.SetPendingActionCreatedIssue(ctx, store.SetPendingActionCreatedIssueParams{CreatedIssue: sql.NullInt64{Int64: int64(issue.GetNumber()), Valid: true}, ID: action}); err != nil {
				return "", fmt.Errorf("GitHub created #%d, but Mobius could not save its number. Check the issues before you approve again: %w", issue.GetNumber(), err)
			}
		}
	}
	var err error
	if body := e.withChatLink(repository, int64(issue.GetNumber()), input.Brief); body != issue.GetBody() {
		updated, _, err := repository.Client.Issues.Update(ctx, owner, name, issue.GetNumber(), gh.UpdateIssueRequest{Body: &body})
		if err != nil {
			return "", fmt.Errorf("GitHub created #%d, but the update of its body failed. Approve the action again: %w", issue.GetNumber(), err)
		}
		issue = updated
	}
	if issue.Labels, _, err = repository.Client.Issues.AddLabelsToIssue(ctx, owner, name, issue.GetNumber(), append([]string{workstreamLabel}, input.Labels...)); err != nil {
		return "", fmt.Errorf("GitHub created #%d, but the add of the Workstream labels failed. Do not create the Workstream again: %w", issue.GetNumber(), err)
	}
	if _, err := e.updateCopy(ctx, repository, issue, nil, false, e.labelChangeCount(repository.FullName)); err != nil {
		log.Printf("copy the new Workstream %s#%d: %v", repository.FullName, issue.GetNumber(), err)
	}
	e.publish(Change{Workstreams: true})
	if c.role == CoordinatorRole {
		e.publish(Change{Created: &Created{repository.FullName, int64(issue.GetNumber())}})
	}
	if c.role == TriagerRole {
		if err := e.endTriage(ctx, c, repository, int64(issue.GetNumber())); err != nil {
			return "", fmt.Errorf("GitHub created #%d, but the end of the triage failed. Do not create the Workstream again: %w", issue.GetNumber(), err)
		}
	}
	return fmt.Sprintf("Created the Workstream #%d.", issue.GetNumber()), nil
}

// createdIssue gives the issue that an earlier run of the action created, or nil when the action created none.
func (e *Engine) createdIssue(ctx context.Context, repository github.Repository, action int64) (*gh.Issue, error) {
	row, err := e.queries.GetPendingAction(ctx, action)
	if err != nil || !row.CreatedIssue.Valid {
		return nil, err
	}
	issue, err := repository.Issue(ctx, row.CreatedIssue.Int64)
	if err == nil && issue == nil {
		return nil, fmt.Errorf("GitHub has no issue #%d, which this action created", row.CreatedIssue.Int64)
	}
	return issue, err
}

// triageEndText starts the comment of the Mobius App that ends the triage of an issue.
const triageEndText = "The triage ends."

// endTriage comments on the issue of the Triager c with the link to the Workstream and closes the issue as completed.
func (e *Engine) endTriage(ctx context.Context, c caller, repository github.Repository, workstream int64) error {
	number := c.issue
	text := fmt.Sprintf("%s The Workstream is %s", triageEndText, issueLink(repository.FullName, workstream))
	if err := e.addOrKeepComment(ctx, repository, number, text); err != nil {
		return err
	}
	_, err := repository.CloseIssue(ctx, number, "completed")
	return err
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

func checkMoveTask(c caller, input moveInput) error {
	if input.N < 1 || input.Workstream < 1 {
		return refuse("n and workstream must be 1 or more.")
	}
	if input.Workstream == c.workstream {
		return refuse("#%d is this Workstream.", input.Workstream)
	}
	return nil
}

func (e *Engine) moveTask(ctx context.Context, c caller, repository github.Repository, input moveInput) (string, error) {
	if err := checkMoveTask(c, input); err != nil {
		return "", err
	}
	issue, err := issueOf(ctx, repository, input.N)
	if err != nil {
		return "", err
	}
	in, err := e.inWorkstream(ctx, repository, c.workstream, input.N)
	if err != nil {
		return "", err
	}
	if !in {
		return "", refuse("#%d is not in this Workstream.", input.N)
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
	_, of, err := e.workstreamOf(ctx, repository, input.N)
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

func checkEditBrief(_ caller, input editBriefInput) error {
	if empty(input.Brief) || empty(input.Reason) {
		return refuse("brief and reason must not be empty.")
	}
	return nil
}

func (e *Engine) editBrief(ctx context.Context, c caller, repository github.Repository, input editBriefInput) (string, error) {
	if err := checkEditBrief(c, input); err != nil {
		return "", err
	}
	body := e.withChatLink(repository, c.workstream, input.Brief)
	issue, _, err := repository.Client.Issues.Update(ctx, repository.Owner(), repository.Name(), int(c.workstream), gh.UpdateIssueRequest{Body: &body})
	if err != nil {
		return "", err
	}
	events, err := repository.IssueEvents(ctx, c.workstream)
	if err == nil {
		_, err = e.updateCopy(ctx, repository, issue, events, false, e.labelChangeCount(repository.FullName))
	}
	if err != nil {
		log.Printf("copy the new Brief of %s#%d: %v", repository.FullName, c.workstream, err)
	}
	e.publish(Change{Workstreams: true})
	return fmt.Sprintf("Changed the Brief of #%d.", c.workstream), nil
}

func checkMessageLead(c caller, input messageLeadInput) error {
	if c.role == LeadRole && input.Workstream == c.workstream {
		return refuse("A Lead cannot send a message to its own Workstream.")
	}
	if empty(input.Text) {
		return refuse("text must not be empty.")
	}
	return nil
}

func (e *Engine) messageLead(ctx context.Context, c caller, repository github.Repository, input messageLeadInput) (string, error) {
	if err := checkMessageLead(c, input); err != nil {
		return "", err
	}
	kind, sender := "coordinator", "the Coordinator"
	switch c.role {
	case LeadRole:
		kind, sender = "lead", fmt.Sprintf("the Lead of #%d", c.workstream)
	case TriagerRole:
		kind, sender = "triager", "the Triager"
	}
	open, err := e.queries.HasCopiedWorkstream(ctx, store.HasCopiedWorkstreamParams{Repository: repository.FullName, Number: input.Workstream})
	if err != nil {
		return "", err
	}
	if !open {
		return "", refuse("#%d is not an open Workstream.", input.Workstream)
	}
	text := fmt.Sprintf("Message of %s, approved by the Owner:\n\n%s", sender, input.Text)
	if c.role == TriagerRole {
		text += "\n\n" + issueLink(repository.FullName, c.issue)
	}
	sent := false
	if c.role == TriagerRole {
		sent, err = e.queries.HasLeadEvent(ctx, store.HasLeadEventParams{Repository: repository.FullName, Workstream: input.Workstream, Kind: kind, Payload: text})
		if err != nil {
			return "", err
		}
	}
	if !sent {
		if err := e.addLeadEvent(ctx, repository.FullName, input.Workstream, sql.NullInt64{}, kind, text); err != nil {
			return "", err
		}
	}
	if c.role == TriagerRole {
		if err := e.endTriage(ctx, c, repository, input.Workstream); err != nil {
			return "", fmt.Errorf("the Lead of #%d got the message, but the end of the triage failed. Do not send the message again: %w", input.Workstream, err)
		}
	}
	return fmt.Sprintf("Sent the message to the Lead of #%d.", input.Workstream), nil
}

// withChatLink gives body with the link to the Lead chat of the Workstream at the end, one time. It removes the link
// lines of the same Workstream and the blank lines at the end of body first. It gives body with no change when the config
// has no ui_url.
func (e *Engine) withChatLink(repository github.Repository, workstream int64, body string) string {
	if e.config.UIURL == "" {
		return body
	}
	link := fmt.Sprintf("[Open the Lead chat in Mobius](%s/workstreams/%s/%s/%d)", e.config.UIURL, repository.Owner(), repository.Name(), workstream)
	lines := slices.DeleteFunc(strings.Split(body, "\n"), func(line string) bool { return strings.TrimSpace(line) == link })
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\r\n") + "\n\n" + link
}
