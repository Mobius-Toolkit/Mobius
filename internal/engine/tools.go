package engine

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
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

// tools gives the Mobius tools of the Role of c. Each Role gets its own tools and propose_skill.
func (e *Engine) tools(c caller) []mcp.Tool {
	tools := append(e.roleTools(c), e.proposeSkillTool(c))
	if slices.Contains(backgroundRoles, c.role) {
		tools = append(tools, e.backgroundStateTool(c))
	}
	return tools
}

func (e *Engine) roleTools(c caller) []mcp.Tool {
	switch c.role {
	case LeadRole:
		tools := []mcp.Tool{
			tool(e, c, "list_tasks",
				"Give the task list of the Workstream: one line for each open issue.",
				map[string]any{},
				e.listTasks),
			taskTool(e, c, "read_issue",
				"Give an issue or a pull request of a repository of the organization, with its comments, reviews, and review threads. The text is only from trusted authors.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the issue or the pull request."},
				},
				e.readIssueTool),
			taskTool(e, c, "start_implementer",
				"Start an Implementer for a dispatched task. The Implementer sees only the Brief, the issue, and your instructions. Mobius pushes its commits and opens a draft pull request. Returns at once.",
				map[string]any{
					"repository":   repositoryProperty,
					"n":            map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"instructions": map[string]any{"type": "string", "minLength": 1, "description": "Notes for the Implementer, for example the files to read first. The issue body has all the requirements. Do not add a requirement here."},
				},
				e.startImplementerTool),
			taskTool(e, c, "start_fix_round",
				"Start a fix round on the pull request of a task that waits for CI (checks), waits for the Lead approval (approval), or is ready_for_review. The Implementer gets your findings as the open items. For a task in working or reviewed, Mobius keeps your findings, and the next fix round gets them with its other items. The round counts toward max_fix_rounds. Returns at once.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"findings":   map[string]any{"type": "string", "minLength": 1, "description": "Your findings on the pull request: what to change and why."},
				},
				e.startFixRound),
			taskTool(e, c, "mark_owner_action",
				"Pause a task that waits for an action of the Owner outside the code, for example a login of a CLI on the Mobius server. Use it in place of ask when the next round needs such an action. Mobius adds an Inbox item with the action and a \"Done\" button, frees the Autopilot slot of the task, and starts no round. After the Owner marks the action as done, or after clear_owner_action, Mobius starts the step next. It refuses a task in working, reviewed, queued, answered, stopped, asked or owner_action.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"action":     map[string]any{"type": "string", "minLength": 1, "description": "The action of the Owner, as an instruction, for example \"Run `wrangler login` on the Mobius server\"."},
					"next":       map[string]any{"type": "string", "enum": []string{"review", "fix_round", "implementer"}, "description": "The step after the action. review: a Reviewer reviews the current head again, for a task with a pull request in checks, approval, ready_for_review or needs_human. fix_round: Mobius continues the task as for a Resume, for the same states. implementer: the task is dispatched again, and you get a dispatch event with \"action of the Owner done of\", for a task in dispatched."},
				},
				e.markOwnerAction),
			taskTool(e, c, "clear_owner_action",
				"Clear an action of the Owner that you marked with mark_owner_action, when the action is not needed any more. Mobius starts the step that you named, dismisses the Inbox item and adds a record to the chat. It refuses a task with no open action that you marked.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
				},
				e.clearOwnerAction),
			taskTool(e, c, "approve_pull_request",
				"Approve the pull request of a task that waits for the Lead approval. Mobius posts the report as a comment on the pull request, sets the Mobius check to success, and adds the \"ready for review\" Inbox item for the Owner. The item links to the comment. It removes the draft status, but the pull request stays a draft while a blocking deployment todo is open.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"report":     map[string]any{"type": "string", "minLength": 1, "description": "Your approval report in two parts: what you checked, and what you verified and how."},
				},
				e.approvePullRequest),
			taskTool(e, c, "update_branch",
				"Update the branch of the pull request of a task with the default branch. Mobius merges the default branch into the branch and reads the pull request again at the next poll. It refuses a task with no open pull request and a task with a running Worker.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
				},
				e.updateBranchTool),
			taskTool(e, c, "rerun_failed_jobs",
				"Run the failed CI jobs of the newest failed workflow runs of the head of the pull request of a task again. It works for each attempt of a run. When GitHub refuses the re-run of the failed jobs and the attempt has no failed job, for example when GitHub never created a job, the tool runs the full workflow run again. The result names each run that started again, and each run that GitHub refused, with the reason of GitHub. It refuses a task with no open pull request.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
				},
				e.rerunFailedJobsTool),
			taskTool(e, c, "stop_task",
				"Stop the work on a task of this Workstream that is queued or working. Call it only when the Owner tells you to stop that task. The pull request and the branch stay.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
				},
				e.stopTaskTool),
			taskTool(e, c, "send_details",
				"Send new details from the Owner to the Implementer that operates on a task now. The Implementer keeps its session and its context. Update the body of the task issue first. It refuses a task with no open Implementer session: a later session reads the updated issue body.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"text":       map[string]any{"type": "string", "minLength": 1, "description": "The new details."},
				},
				e.sendDetails),
			taskTool(e, c, "start_researcher",
				"Start a Researcher that answers a question about the code of the default branch of a repository of the organization. The Researcher sees only the Brief and the question. The tool returns the id of the Researcher at once, and the report arrives later.",
				map[string]any{
					"repository": codeRepositoryProperty,
					"question":   map[string]any{"type": "string", "minLength": 1, "description": "The question, with the context that the Researcher needs."},
				},
				e.startRepositoryResearcher),
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
			tool(e, c, "start_operator",
				"Start an Operator that does work outside code for this Workstream, with the MCP servers and the web tools of its session. The Operator sees only the Brief, its memory file, and your text. It changes no code and no repository. The tool returns the id of the Operator at once, and the report arrives later as an Operator message.",
				map[string]any{
					"text": map[string]any{"type": "string", "minLength": 1, "description": "The work for the Operator, with the context that it needs."},
				},
				e.startOperator),
			taskTool(e, c, "ask",
				"Ask the people on a task issue, or on the Workstream issue, a question. For a task issue with no live task, ask on the Workstream issue, and name the task issue in the text. Use it when you need a decision of the Owner. For a choice between alternatives, give the questions with their options in questions, and the context in text. Mobius posts the text and the questions as a comment, adds mobius:question, and adds an Inbox item for the Owner. A new question on an issue replaces the open question of that issue in the Inbox. Thus put all open questions of one issue into one call. The reply arrives later as an event. On a dispatched task, the task frees its Autopilot slot until the Owner replies. Then you get a dispatch event again.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue, or of the Workstream issue."},
					"text":       map[string]any{"type": "string", "minLength": 1, "description": "The question for the people on the issue. With questions, the context of the questions."},
					"questions":  questionsProperty,
				},
				e.ask),
			taskTool(e, c, "close_question",
				"Close your question on a task issue or on the Workstream issue when the Owner answers it in the chat. An answer of the Owner on GitHub closes the question, so call no close_question then. Mobius posts the answer as a comment on the issue, removes mobius:question, and dismisses the question Inbox item. On a task that waits for the answer, the task continues, and you get a dispatch event with \"answer of\".",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue, or of the Workstream issue."},
					"answer":     map[string]any{"type": "string", "minLength": 1, "description": "The answer of the Owner."},
				},
				e.closeQuestionTool),
			toolOf(e, c, noRepository, "withdraw", withdrawDescription,
				map[string]any{
					"id": map[string]any{"type": "integer", "minimum": 1, "description": "The ID of your open question or pending action."},
				},
				e.withdraw),
			taskTool(e, c, "decline",
				"Decline a task. Mobius posts the reason as a comment on the issue, removes mobius:working, and ends the task.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"reason":     map[string]any{"type": "string", "minLength": 1, "description": "The reason for the people on the issue."},
				},
				e.decline),
			taskTool(e, c, "close_task",
				"Close a task issue that has no work left. Mobius posts the reason as a comment on the issue. For a live task, Mobius closes its open pull request and ends the task. Then Mobius removes mobius:ready and closes the issue as not planned. The branch stays.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"reason":     map[string]any{"type": "string", "minLength": 1, "description": "The reason for the people on the issue."},
				},
				e.closeTask),
			taskTool(e, c, "create_issue",
				"Create an issue in a repository of the organization, below an issue of the Workstream. Mobius adds the blockers as native issue dependencies.",
				map[string]any{
					"repository": map[string]any{"type": "string", "minLength": 1, "description": "The full name of the repository (owner/name) of the new issue."},
					"title":      map[string]any{"type": "string", "minLength": 1, "description": "The title of the issue."},
					"body":       map[string]any{"type": "string", "description": "The body of the issue, with the sections Goal, Today (optional), Change, Limits, and Done, in this order."},
					"parent":     map[string]any{"type": "string", "minLength": 1, "description": "The Workstream issue or an issue below it, as owner/name#N. The issue can be in any repository of the organization."},
					"blocked_by": map[string]any{"type": "array", "items": map[string]any{"type": "string", "minLength": 1}, "description": "The issues that block this issue, each as owner/name#N. They can be in another repository or in another Workstream."},
					"implementer": map[string]any{
						"type":        "object",
						"description": "Optional. The Implementer setting of the new task. Give it only when the Owner asks for it. Mobius refuses a setting that the Roles page refuses, and then creates no issue.",
						"required":    []string{"harness", "model", "effort"},
						"properties":  implementerSettingProperties,
					},
				},
				e.createIssue),
			taskTool(e, c, "set_implementer_binding",
				"Set or clear the Implementer setting (Harness, model and effort) of a task issue, or of the Workstream issue. Use it only when the Owner asks for it. For the Workstream issue, the setting applies to each task of the Workstream with no setting of its own. An empty harness, model and effort clear the setting. The next Implementer session uses the setting, and a session that runs keeps its binding. Mobius refuses a setting that the Roles page refuses. It acts at once.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue, or of the Workstream issue."},
					"harness":    implementerSettingProperties["harness"],
					"model":      implementerSettingProperties["model"],
					"effort":     implementerSettingProperties["effort"],
				},
				e.setImplementerBinding),
			taskTool(e, c, "edit_issue_body",
				"Set the body of a task issue of the Workstream. Mobius stores the body in the database and writes it to GitHub with the link line of the Lead chat.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
					"body":       map[string]any{"type": "string", "description": "The full new body of the task issue, with no link line of the Lead chat."},
				},
				e.editIssueBody),
			taskTool(e, c, "update_blockers",
				"Set the open blockers of an existing issue of the Workstream. Mobius adds each issue of blocked_by that is not a blocker yet, and removes each open blocker that is not in blocked_by. It refuses a blocker that closes a cycle of blockers. Read the current blockers with list_tasks first. An empty blocked_by removes each open blocker. A second call with the same list changes nothing.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the blocked issue."},
					"blocked_by": map[string]any{"type": "array", "items": map[string]any{"type": "string", "minLength": 1}, "description": "The full list of the issues that block it, each as owner/name#N. They can be in another repository or in another Workstream."},
				},
				e.updateBlockers),
			taskTool(e, c, "mark_ready",
				"Add mobius:ready to an issue of the Workstream. Use it when the Owner tells you to start an issue. For a stopped task, it continues the task. The round counters keep their values, so a task at max_fix_rounds stops again.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the issue."},
				},
				e.markReady),
			taskTool(e, c, "reply_thread",
				"Reply in a review thread or to a conversation comment of the pull request of a task, for example with the link to a follow-up issue.",
				map[string]any{
					"repository": repositoryProperty,
					"thread":     map[string]any{"type": "integer", "minimum": 1, "description": "The number of the thread or the comment in the event."},
					"text":       map[string]any{"type": "string", "minLength": 1, "description": "A follow-up link, an answer, or a reason to reject. Do not write an acknowledgement."},
				},
				e.replyThread),
			taskTool(e, c, "comment_pull_request",
				"Post a comment on the pull request of a task, for example to propose that a human closes a stale pull request.",
				map[string]any{
					"repository": repositoryProperty,
					"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the pull request."},
					"text":       map[string]any{"type": "string", "minLength": 1, "description": "The comment."},
				},
				e.commentPullRequest),
			e.createWorkstreamTool(c),
			e.moveTaskTool(c),
			e.messageLeadTool(c),
			e.editBriefTool(c),
			e.createRecurringTaskTool(c),
			e.updateRecurringTaskTool(c),
			e.deleteRecurringTaskTool(c),
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
			tool(e, c, "report_finished",
				"Report that the Workstream is finished, so the Owner gets the button to close it. Call it only for a non-evergreen Workstream when all its tasks are closed or it has no task, and each item of the Done list of the Brief is on the main branch. It refuses an evergreen Workstream.",
				map[string]any{},
				e.reportFinished),
		}
		tools = append(tools, e.rejectSkillProposalTool(c))
		if c.agent.skills {
			tools = append(tools, e.readSkillUsageTool(c))
		}
		return tools
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
			tool(e, c, "submit_report",
				"Give the report of the pull request: the cause, the test results, the measurements, the changed screens, the upgrade step, and each item of the Done list with the change or the test that meets it. Each call replaces the earlier report, so give the full report each time. Mobius writes the report in the description of the pull request at the end of the turn. The report has at most 20,000 characters.",
				map[string]any{
					"report": map[string]any{"type": "string", "minLength": 1, "description": "The full report, as Markdown."},
				},
				e.submitReport),
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
			tool(e, c, "report_deployment_needs",
				"Give the deployment needs of the full diff: each change that a person must do in the deployment. Call it one time in each review, also with an empty list. A later valid call replaces an earlier one. Never give the value of an env var, a config value or a secret.",
				map[string]any{
					"needs": map[string]any{
						"type":        "array",
						"description": "One item for each deployment need. It can be empty.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"kind":        map[string]any{"type": "string", "enum": deploymentNeedKinds, "description": "The kind of the need."},
								"name":        map[string]any{"type": "string", "minLength": 1, "description": "The name of the env var, the config key or the secret, or a short name of the manual step."},
								"path":        map[string]any{"type": "string", "minLength": 1, "description": "The file that adds the need, relative to the repository root."},
								"line":        map[string]any{"type": "integer", "minimum": 1, "description": "The line in the new version of the file."},
								"reason":      map[string]any{"type": "string", "minLength": 1, "description": "Why the deployment needs it."},
								"mandatory":   map[string]any{"type": "boolean", "description": "True when the deployment fails or the application does not start without it. False when the application works without it."},
								"after_merge": map[string]any{"type": "boolean", "description": "True when a person can do this step only after the merge. Such a need does not block the merge."},
							},
							"required":             []string{"kind", "name", "path", "line", "reason", "mandatory", "after_merge"},
							"additionalProperties": false,
						},
					},
				},
				e.reportDeploymentNeeds),
			tool(e, c, "report_owner_action",
				"Give the action that a person must do outside the code before the next round can pass, for example to run a login of a CLI on the Mobius server. Mobius pauses the task after the review and adds an Inbox item for the Owner. Call it only when such an action exists. A later call replaces an earlier one. It writes nothing to GitHub.",
				map[string]any{
					"action": map[string]any{"type": "string", "minLength": 1, "description": "The action of the Owner, as an instruction."},
				},
				e.reportOwnerAction),
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
				"Change one part of one memory file of the repository. Give the file: shared, or the name of a role. Give an old text that occurs one time in the file, and the new text. An empty old text adds the new text at the end of the file. An empty new text removes the old text. Give the reason of the change. The tool refuses an empty reason, an unknown file, a result of more than 200 lines, and a result above the byte limit of the file (16,384 bytes for shared, 4,096 bytes for a role file). When a file is already above its byte limit, the tool accepts a result only when it has fewer bytes than the current text.",
				map[string]any{
					"file":   map[string]any{"type": "string", "enum": memoryFileNames, "description": "The memory file: shared, or the name of a role."},
					"old":    map[string]any{"type": "string", "description": "The text to replace. It occurs one time in the memory file. Empty to add the new text at the end."},
					"new":    map[string]any{"type": "string", "description": "The replacement text. Empty to remove the old text."},
					"reason": map[string]any{"type": "string", "minLength": 1, "description": "Why you make the change. Name the type of change (add, merge, change or remove) and the evidence, for example the Workstream, the issue or the pull request."},
				},
				e.editMemory),
			tool(e, c, "report_gap",
				"Report a gap of a Mobius tool or prompt to the Owner. Give the gap, the evidence, and the proposed change. Mobius posts the report in the Coordinator chat.",
				map[string]any{
					"text": map[string]any{"type": "string", "minLength": 1, "description": "The gap of a Mobius tool or prompt, the evidence, and the proposed change."},
				},
				e.reportGap),
		}
		if c.agent.maintenance.number == 0 {
			return tools
		}
		return append(tools,
			tool(e, c, "search_issues",
				"Search the open and the closed issues of the repository. Give the words of the problem. For each closed issue, the result has the close reason and the last 3 comments of trusted authors, but no engine notice of the Mobius App. The result has only issues of trusted authors. It gives the first 20 matches.",
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
			taskTool(e, c, "start_researcher",
				"Start a Researcher that answers a question about the code of the default branch of a repository. The Researcher sees only the question. The tool returns at once, and the report arrives later.",
				map[string]any{
					"repository": codeRepositoryProperty,
					"question":   map[string]any{"type": "string", "minLength": 1, "description": "The question, with the context that the Researcher needs."},
				},
				e.startRepositoryResearcher),
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

var coordinatorWorkstreamLabels = []string{evergreenLabel, maintenanceLabel, skillsLabel}

var coordinatorWorkstreamProperties = map[string]any{
	"title": workstreamProperties["title"],
	"brief": workstreamProperties["brief"],
	"labels": map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string", "enum": coordinatorWorkstreamLabels},
		"description": "More labels for the Workstream issue. Only mobius:evergreen, mobius:maintenance and mobius:skills are allowed.",
	},
}

func moveIssueTool(e *Engine, c caller) mcp.Tool {
	return tool(e, c, "move_issue",
		"Make an issue a sub-issue of an open Workstream in the issue repository. For an issue with mobius:no-workstream, Mobius then adds mobius:ready again.",
		map[string]any{
			"repository": map[string]any{"type": "string", "minLength": 1, "description": "The full name of the repository of the issue (owner/name) in the organization of the chat."},
			"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the issue."},
			"workstream": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the Workstream issue in the issue repository."},
		},
		e.moveIssue)
}

// tool gives the Mobius tool name of c. The tool decodes the arguments into In for run, and adds the call
// with its result or its error to the Transcript of the session.
func tool[In any](e *Engine, c caller, name, description string, properties map[string]any, run func(context.Context, caller, github.Repository, In) (string, error)) mcp.Tool {
	return toolOf(e, c, e.callerRepository, name, description, properties, run)
}

var repositoryProperty = map[string]any{"type": "string", "minLength": 1, "description": "The full name of the repository (owner/name) of the issue."}

var codeRepositoryProperty = map[string]any{"type": "string", "minLength": 1, "description": "The full name of the repository of the code (owner/name) in the organization of the chat."}

// repositoryInput is the repository input of a tool that acts on an issue in any repository of the organization.
type repositoryInput struct {
	Repository string `json:"repository"`
}

func (i repositoryInput) repositoryName() string {
	return i.Repository
}

// taskTool is tool for a Lead tool whose input names the repository of its issue. The run gets that repository. The tool
// refuses a repository that the Mobius App cannot read and a repository of another organization, before the run.
func taskTool[In interface{ repositoryName() string }](e *Engine, c caller, name, description string, properties map[string]any, run func(context.Context, caller, github.Repository, In) (string, error)) mcp.Tool {
	return toolOf(e, c, noRepository, name, description, properties,
		func(ctx context.Context, c caller, _ github.Repository, input In) (string, error) {
			repository, err := e.organizationRepository(c.organization, input.repositoryName())
			if err != nil {
				return "", err
			}
			return run(ctx, c, repository, input)
		})
}

// noRepository is the resolve of a tool that takes the repository as an input. The run of the tool gets the zero
// Repository.
func noRepository(context.Context, caller) (github.Repository, error) {
	return github.Repository{}, nil
}

// toolOf is tool for a tool whose run gets the repository that resolve gives.
func toolOf[In any](e *Engine, c caller, resolve func(context.Context, caller) (github.Repository, error), name, description string, properties map[string]any, run func(context.Context, caller, github.Repository, In) (string, error)) mcp.Tool {
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

func call[In any](ctx context.Context, c caller, resolve func(context.Context, caller) (github.Repository, error), name string, arguments json.RawMessage, run func(context.Context, caller, github.Repository, In) (string, error)) (string, error) {
	repository, err := resolve(ctx, c)
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

// organizationRepository gives the repository name of the organization. It refuses a repository that the Mobius App
// cannot read and a repository of another organization.
func (e *Engine) organizationRepository(organization, name string) (github.Repository, error) {
	if empty(name) {
		return github.Repository{}, refuse("repository must not be empty.")
	}
	repository, err := e.repository(name)
	if err != nil {
		return github.Repository{}, err
	}
	if repository.Owner() != organization {
		return github.Repository{}, refuse("%s is not in the organization %s.", name, organization)
	}
	return repository, nil
}

// The sources of the issue repository of an organization.
const (
	issueRepositoryConfig  = "config"
	issueRepositoryChoice  = "choice"
	issueRepositoryDefault = "default"
	issueRepositoryNone    = "none"
)

// findIssueRepository gives the repository of the Workstream issues of the organization and where it comes from. In this
// order, it is the issue_repository of the config, the choice of the Owner, the one repository of the organization that
// the Mobius App sees, and the one repository of the organization that holds all its open Workstreams.
func (e *Engine) findIssueRepository(ctx context.Context, organization string) (github.Repository, string, error) {
	if name := e.config.Organizations[organization].IssueRepository; name != "" {
		repository, err := e.organizationRepository(organization, name)
		return repository, issueRepositoryConfig, err
	}
	name, err := e.queries.GetIssueRepositoryChoice(ctx, organization)
	if err == nil {
		repository, err := e.organizationRepository(organization, name)
		return repository, issueRepositoryChoice, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return github.Repository{}, "", err
	}
	var found []github.Repository
	for _, repository := range e.github.Repositories() {
		if repository.Owner() == organization {
			found = append(found, repository)
		}
	}
	if len(found) == 1 {
		return found[0], issueRepositoryDefault, nil
	}
	copied, err := e.queries.ListRepositoriesWithOpenWorkstreams(ctx)
	if err != nil {
		return github.Repository{}, "", err
	}
	copied = slices.DeleteFunc(copied, func(name string) bool { return owner(name) != organization })
	if len(copied) == 1 {
		repository, err := e.organizationRepository(organization, copied[0])
		return repository, issueRepositoryDefault, err
	}
	return github.Repository{}, "", refuse("Mobius needs the issue repository of %s. Select it on the Checkup page \"Workstreams\", or set organizations.%s.issue_repository in config.toml.", organization, organization)
}

// workstreamRepository gives the repository of the Workstream issues of the organization.
func (e *Engine) workstreamRepository(ctx context.Context, organization string) (github.Repository, error) {
	repository, _, err := e.findIssueRepository(ctx, organization)
	return repository, err
}

// triagerWorkstreamRepository gives the repository of the Workstreams of the organization of the Triager c. The issue of
// c stays in c.repository.
func (e *Engine) triagerWorkstreamRepository(ctx context.Context, c caller) (github.Repository, error) {
	return e.workstreamRepository(ctx, c.organization)
}

// callerRepository gives the repository of c. The Coordinator chat has no repository, so it uses the issue repository
// of its organization.
func (e *Engine) callerRepository(ctx context.Context, c caller) (github.Repository, error) {
	if c.repository != "" {
		return e.repository(c.repository)
	}
	return e.workstreamRepository(ctx, c.organization)
}

// ErrIssueRepositoryInConfig is the error of a choice of the issue repository for an organization that has the
// issue_repository key in the config.
var ErrIssueRepositoryInConfig = errors.New("config.toml sets the issue repository of this organization")

// SetIssueRepository stores the choice of the Owner for the issue repository of the organization. It gives
// ErrIssueRepositoryInConfig when the config has the key, and a refusal when repository is not a repository of the
// organization that the Mobius App sees.
func (e *Engine) SetIssueRepository(ctx context.Context, organization, repository string) (string, error) {
	if e.config.Organizations[organization].IssueRepository != "" {
		return "", fmt.Errorf("%w with organizations.%s.issue_repository. Change the key in config.toml to change the issue repository", ErrIssueRepositoryInConfig, organization)
	}
	found, err := e.organizationRepository(organization, repository)
	if err != nil {
		return "", err
	}
	err = e.queries.SetIssueRepositoryChoice(ctx, store.SetIssueRepositoryChoiceParams{Organization: organization, Repository: found.FullName})
	if err != nil {
		return "", err
	}
	if err := e.syncMissingWorkstreams(ctx, false); err != nil {
		log.Printf("check the missing Workstreams: %v", err)
	}
	return found.FullName, nil
}

type noInput struct{}

type taskInput struct {
	repositoryInput
	N int64 `json:"n"`
}

type approveInput struct {
	repositoryInput
	N      int64  `json:"n"`
	Report string `json:"report"`
}

type textInput struct {
	repositoryInput
	N    int64  `json:"n"`
	Text string `json:"text"`
}

type askInput struct {
	N         int64      `json:"n"`
	Text      string     `json:"text"`
	Questions []Question `json:"questions"`
}

type taskAskInput struct {
	repositoryInput
	askInput
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

type taskThreadInput struct {
	repositoryInput
	threadInput
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
	Title     string
	Body      string
	Parent    int64
	BlockedBy []int64
}

type leadIssueInput struct {
	repositoryInput
	Title       string                   `json:"title"`
	Body        string                   `json:"body"`
	Parent      string                   `json:"parent"`
	BlockedBy   []string                 `json:"blocked_by"`
	Implementer *implementerSettingInput `json:"implementer"`
}

// implementerSettingProperties are the properties of an Implementer setting in the input of a Lead tool.
var implementerSettingProperties = map[string]any{
	"harness": map[string]any{"type": "string", "description": "The Harness."},
	"model":   map[string]any{"type": "string", "description": "The model of the Harness."},
	"effort":  map[string]any{"type": "string", "description": "The effort. Empty means that the Harness uses its own default."},
}

type implementerSettingInput struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

type implementerBindingInput struct {
	repositoryInput
	N int64 `json:"n"`
	implementerSettingInput
}

type blockersInput struct {
	repositoryInput
	N         int64    `json:"n"`
	BlockedBy []string `json:"blocked_by"`
}

type editIssueBodyInput struct {
	repositoryInput
	N    int64  `json:"n"`
	Body string `json:"body"`
}

type workstreamInput struct {
	Title  string   `json:"title"`
	Brief  string   `json:"brief"`
	Labels []string `json:"labels"`
}

type moveInput struct {
	repositoryInput
	N          int64 `json:"n"`
	Workstream int64 `json:"workstream"`
}

type moveIssueInput struct {
	Repository string `json:"repository"`
	N          int64  `json:"n"`
	Workstream int64  `json:"workstream"`
}

type editBriefInput struct {
	Brief  string `json:"brief"`
	Reason string `json:"reason"`
}

type createRecurringInput struct {
	Cron string `json:"cron"`
	Text string `json:"text"`
}

type updateRecurringInput struct {
	ID   int64  `json:"id"`
	Cron string `json:"cron"`
	Text string `json:"text"`
}

type deleteRecurringInput struct {
	ID int64 `json:"id"`
}

type messageLeadInput struct {
	Workstream int64  `json:"workstream"`
	Text       string `json:"text"`
}

type implementerInput struct {
	repositoryInput
	N            int64  `json:"n"`
	Instructions string `json:"instructions"`
}

type findingsInput struct {
	repositoryInput
	N        int64  `json:"n"`
	Findings string `json:"findings"`
}

type questionInput struct {
	Question string `json:"question"`
}

type repositoryQuestionInput struct {
	repositoryInput
	questionInput
}

type answerInput struct {
	repositoryInput
	N      int64  `json:"n"`
	Answer string `json:"answer"`
}

type declineInput struct {
	repositoryInput
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

type markOwnerActionInput struct {
	repositoryInput
	N      int64  `json:"n"`
	Action string `json:"action"`
	Next   string `json:"next"`
}

type ownerActionInput struct {
	Action string `json:"action"`
}

type deploymentNeedsInput struct {
	Needs []deploymentNeedInput `json:"needs"`
}

type deploymentNeedInput struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Line       int64  `json:"line"`
	Reason     string `json:"reason"`
	Mandatory  bool   `json:"mandatory"`
	AfterMerge bool   `json:"after_merge"`
}

type verdictsInput struct {
	Items []itemVerdicts `json:"items"`
}

func empty(text string) bool {
	return strings.TrimSpace(text) == ""
}

func (e *Engine) listTasks(ctx context.Context, c caller, repository github.Repository, _ noInput) (string, error) {
	lines, err := e.taskLines(ctx, repository, c.workstream)
	return taskText(repository.FullName, lines), err
}

func (e *Engine) readIssueTool(ctx context.Context, _ caller, repository github.Repository, input taskInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	return e.readIssue(ctx, repository, input.N)
}

// createIssue creates an issue in repository below the issue input.Parent, which is the Workstream issue of c or an issue
// below it, in any repository of the organization.
func (e *Engine) createIssue(ctx context.Context, c caller, repository github.Repository, input leadIssueInput) (string, error) {
	if empty(input.Title) {
		return "", refuse("title must not be empty.")
	}
	parent, err := e.issueAtOf(c, input.Parent)
	if err != nil {
		return "", err
	}
	blockers := make([]issueAt, len(input.BlockedBy))
	for i, reference := range input.BlockedBy {
		if blockers[i], err = e.issueAtOf(c, reference); err != nil {
			return "", err
		}
	}
	workstream, err := e.repository(c.repository)
	if err != nil {
		return "", err
	}
	var parentAdd *parentToAdd
	if parent.repository.FullName != workstream.FullName || parent.number != c.workstream {
		found, err := e.queries.GetTreeIssue(ctx, store.GetTreeIssueParams{Repository: parent.repository.FullName, Number: parent.number})
		switch {
		case err == nil:
			if !found.Workstream.Valid || !strings.EqualFold(found.WorkstreamRepository.String, workstream.FullName) || found.WorkstreamNumber.Int64 != c.workstream {
				return "", refuse("%s#%d is not in this Workstream.", parent.repository.FullName, parent.number)
			}
			parentAdd = &parentToAdd{id: found.ID}
		case errors.Is(err, sql.ErrNoRows):
			in, err := e.inWorkstream(ctx, workstream.FullName, c.workstream, parent.repository, parent.number)
			if err != nil {
				return "", err
			}
			if !in {
				return "", refuse("%s#%d is not in this Workstream.", parent.repository.FullName, parent.number)
			}
			parentIssue, err := parent.repository.Issue(ctx, parent.number)
			if err != nil {
				return "", err
			}
			if parentIssue == nil {
				return "", refuse("%s#%d is not in this Workstream.", parent.repository.FullName, parent.number)
			}
			parentAdd = &parentToAdd{at: parent, issue: parentIssue}
		default:
			return "", err
		}
	}
	resolved, err := e.resolveBlockers(ctx, blockers)
	if err != nil {
		return "", err
	}
	setting := input.Implementer
	if setting != nil && *setting == (implementerSettingInput{}) {
		setting = nil
	}
	if setting != nil {
		spec := Spec{Role: ImplementerRole, Repository: repository.FullName, WorkstreamRepository: workstream.FullName, Workstream: c.workstream}
		if err := e.checkNewTaskBinding(ctx, spec, *setting); err != nil {
			return "", err
		}
	}
	mark, err := e.mark(ctx, c, repository.FullName)
	if err != nil {
		return "", err
	}
	key := leadIssueKey{repository.FullName, parent.repository.FullName, parent.number, input.Title}
	var claimed *leadIssue
	for {
		claimed, err = c.agent.claimLeadIssue(ctx, key)
		if err != nil {
			return "", err
		}
		if claimed.issueID == 0 {
			break
		}
		row, err := e.queries.GetIssueByID(ctx, claimed.issueID)
		if err == nil {
			if row.Number.Valid {
				return fmt.Sprintf("Created %s#%d.", repository.FullName, row.Number.Int64), nil
			}
			return fmt.Sprintf("Mobius creates the issue %q on GitHub later. A Lead event gives its number.", input.Title), nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		c.agent.clearLeadIssue(key, claimed)
	}
	issueID, createID, err := e.addIssueToCreate(ctx, repository, issueRef{workstream.FullName, c.workstream}, input.Title, input.Body, mark, parentAdd, resolved, nil, setting, false, nil)
	if err != nil {
		c.agent.finishLeadIssue(key, claimed, 0)
		return "", err
	}
	if err := e.sendIssueCreateNow(ctx, repository, issueID, createID); err != nil {
		c.agent.finishLeadIssue(key, claimed, issueID)
		return "", err
	}
	row, err := e.queries.GetIssueByID(ctx, issueID)
	if errors.Is(err, sql.ErrNoRows) {
		c.agent.finishLeadIssue(key, claimed, 0)
		return "", refuse("GitHub refused the write.")
	}
	if err != nil {
		c.agent.finishLeadIssue(key, claimed, issueID)
		return "", err
	}
	c.agent.finishLeadIssue(key, claimed, issueID)
	if !row.Number.Valid {
		return fmt.Sprintf("Mobius creates the issue %q on GitHub later. A Lead event gives its number.", input.Title), nil
	}
	text := fmt.Sprintf("Created %s#%d.", repository.FullName, row.Number.Int64)
	if setting != nil {
		text += fmt.Sprintf(" Its Implementer setting is %s.", settingText(setting.Harness, setting.Model, setting.Effort))
	}
	return text, nil
}

// leadIssueKey identifies the issue that a create_issue call of a Lead session creates.
type leadIssueKey struct {
	repository       string
	parentRepository string
	parent           int64
	title            string
}

// leadIssue is the issue that a call of create_issue creates. done closes when the call ends. issueID is 0 when the
// call added no issue row.
type leadIssue struct {
	done    chan struct{}
	issueID int64
}

// beforeLeadIssueWait is a hook of the tests. claimLeadIssue calls it before it waits for the call that creates the issue.
var beforeLeadIssueWait func()

// claimLeadIssue gives the issue of key that the session created. When no call creates it, the caller gets a leadIssue
// with issueID 0, and it must call finishLeadIssue. When a call creates it, claimLeadIssue waits for that call. When
// that call created no issue, the caller creates it.
func (a *Agent) claimLeadIssue(ctx context.Context, key leadIssueKey) (*leadIssue, error) {
	for {
		a.issuesMu.Lock()
		first, found := a.leadIssues[key]
		if !found {
			if a.leadIssues == nil {
				a.leadIssues = map[leadIssueKey]*leadIssue{}
			}
			first = &leadIssue{done: make(chan struct{})}
			a.leadIssues[key] = first
			a.issuesMu.Unlock()
			return first, nil
		}
		a.issuesMu.Unlock()
		if beforeLeadIssueWait != nil {
			beforeLeadIssueWait()
		}
		select {
		case <-first.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if first.issueID != 0 {
			return first, nil
		}
	}
}

// finishLeadIssue ends the call that created issue. It keeps the issue when the call added a row, and forgets it when
// issueID is 0.
func (a *Agent) finishLeadIssue(key leadIssueKey, issue *leadIssue, issueID int64) {
	a.issuesMu.Lock()
	if issueID != 0 {
		issue.issueID = issueID
	} else {
		delete(a.leadIssues, key)
	}
	a.issuesMu.Unlock()
	close(issue.done)
}

func (a *Agent) clearLeadIssue(key leadIssueKey, issue *leadIssue) {
	a.issuesMu.Lock()
	if a.leadIssues[key] == issue {
		delete(a.leadIssues, key)
	}
	a.issuesMu.Unlock()
}

// checkNewTaskBinding checks the Implementer setting of a task that does not exist yet. spec is the spec of an Implementer
// session of the task, with no issue.
func (e *Engine) checkNewTaskBinding(ctx context.Context, spec Spec, setting implementerSettingInput) error {
	e.roleWriteMu.Lock()
	defer e.roleWriteMu.Unlock()
	return e.checkIssueBinding(ctx, spec, config.Harness(setting.Harness), setting.Model, setting.Effort)
}

func settingText(harness, model, effort string) string {
	return strings.Join(slices.DeleteFunc([]string{harness, model, effort}, func(part string) bool { return part == "" }), " ")
}

// setImplementerBinding sets, or with an empty setting clears, the Implementer setting of the task input.N or of the
// Workstream issue of c. It refuses an issue that is not in the Workstream of c.
func (e *Engine) setImplementerBinding(ctx context.Context, c caller, repository github.Repository, input implementerBindingInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	workstream, err := e.repository(c.repository)
	if err != nil {
		return "", err
	}
	isWorkstream := repository.FullName == workstream.FullName && input.N == c.workstream
	if !isWorkstream {
		in, err := e.inWorkstream(ctx, workstream.FullName, c.workstream, repository, input.N)
		if err != nil {
			return "", err
		}
		if !in {
			return "", refuse("%s#%d is not a task of this Workstream and not the Workstream issue.", repository.FullName, input.N)
		}
	}
	owner := fmt.Sprintf("%s#%d", repository.FullName, input.N)
	if isWorkstream {
		owner = "the Workstream " + owner
	}
	if input.implementerSettingInput == (implementerSettingInput{}) {
		if err := e.ClearIssueBinding(ctx, "implementer", repository.FullName, input.N); err != nil {
			return "", err
		}
		next, level, err := e.NextBinding(ctx, "implementer", repository.FullName, input.N)
		if err != nil {
			return "", fmt.Errorf("the Implementer setting of %s is cleared, but the read of the next binding failed: %w", owner, err)
		}
		return fmt.Sprintf("Cleared the Implementer setting of %s. The next Implementer session uses %s (level: %s).", owner, settingText(string(next.Harness), next.Model, next.Effort), level), nil
	}
	if err := e.SetIssueBinding(ctx, "implementer", repository.FullName, input.N, config.Harness(input.Harness), input.Model, input.Effort); err != nil {
		return "", err
	}
	return fmt.Sprintf("Set the Implementer setting of %s to %s.", owner, settingText(input.Harness, input.Model, input.Effort)), nil
}

// issueAt is the issue number of repository.
type issueAt struct {
	repository github.Repository
	number     int64
}

var issueReference = regexp.MustCompile(`^([^/\s#]+/[^/\s#]+)#([1-9][0-9]*)$`)

// issueAtOf gives the issue that reference (owner/name#N) names. It refuses a repository that the Mobius App cannot read
// and a repository of another organization.
func (e *Engine) issueAtOf(c caller, reference string) (issueAt, error) {
	match := issueReference.FindStringSubmatch(reference)
	if match == nil {
		return issueAt{}, refuse("%q is not an issue reference in the form owner/name#N.", reference)
	}
	number, err := strconv.ParseInt(match[2], 10, 64)
	if err != nil {
		return issueAt{}, refuse("%q is not an issue reference in the form owner/name#N.", reference)
	}
	repository, err := e.organizationRepository(c.organization, match[1])
	if err != nil {
		return issueAt{}, err
	}
	return issueAt{repository, number}, nil
}

// createMaintenanceIssue creates an issue with mobius:triage below the maintenance Workstream of the Curator c. It
// refuses an issue after the maximum of maintenanceIssues issues in the session.
func (e *Engine) createMaintenanceIssue(ctx context.Context, c caller, repository github.Repository, input maintenanceIssueInput) (string, error) {
	issue := issueInput{Title: input.Title, Body: input.Body, Parent: c.agent.maintenance.number, BlockedBy: input.BlockedBy}
	if err := checkIssue(issue); err != nil {
		return "", err
	}
	c.agent.issuesMu.Lock()
	defer c.agent.issuesMu.Unlock()
	if c.agent.issues == maintenanceIssues {
		return "", refuse("You created %d issues in this session. This is the maximum.", maintenanceIssues)
	}
	if slices.Contains(c.agent.issueTitles, input.Title) {
		return "", refuse("You already created an issue with this title in this session. Do not call create_issue again for it.")
	}
	blockers := make([]issueAt, len(input.BlockedBy))
	for i, number := range input.BlockedBy {
		blockers[i] = issueAt{repository, number}
	}
	resolved, err := e.resolveBlockers(ctx, blockers)
	if err != nil {
		return "", err
	}
	mark, err := e.mark(ctx, c, repository.FullName)
	if err != nil {
		return "", err
	}
	workstream := issueRef{c.agent.maintenance.repository.FullName, c.agent.maintenance.number}
	issueID, createID, err := e.addIssueToCreate(ctx, repository, workstream, input.Title, input.Body, mark, nil, resolved, []string{triageLabel}, nil, true, nil)
	if err != nil {
		return "", err
	}
	if err := e.sendIssueCreateNow(ctx, repository, issueID, createID); err != nil {
		return "", err
	}
	row, err := e.queries.GetIssueByID(ctx, issueID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", refuse("GitHub refused the write.")
	}
	if err != nil {
		return "", err
	}
	c.agent.issues++
	c.agent.issueTitles = append(c.agent.issueTitles, input.Title)
	if !row.Number.Valid {
		return fmt.Sprintf("Mobius creates the issue %q on GitHub later.", input.Title), nil
	}
	return fmt.Sprintf("Created %s.", eventRef(c.agent.maintenance.repository.FullName, repository.FullName, row.Number.Int64)), nil
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

type resolvedBlocker struct {
	at        issueAt
	reference string
	id        int64
	issue     *gh.Issue
}

// editIssueBody sets the body of the task issue input.N in repository in the database and sends the body with the link
// line of the Lead chat to GitHub.
func (e *Engine) editIssueBody(ctx context.Context, c caller, repository github.Repository, input editIssueBodyInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	workstreamIssue, err := e.queries.HasWorkstream(ctx, store.HasWorkstreamParams{Repository: repository.FullName, Number: input.N})
	if err != nil {
		return "", err
	}
	if workstreamIssue {
		return "", refuse("%s#%d is a Workstream issue.", repository.FullName, input.N)
	}
	row, err := e.queries.GetTreeIssue(ctx, store.GetTreeIssueParams{Repository: repository.FullName, Number: input.N})
	if errors.Is(err, sql.ErrNoRows) {
		issue, err := repository.Issue(ctx, input.N)
		if err != nil {
			return "", err
		}
		if issue == nil || issue.IsPullRequest() {
			return "", refuse("%s#%d is not an issue.", repository.FullName, input.N)
		}
		if hasLabel(issue, workstreamLabel) {
			return "", refuse("%s#%d is a Workstream issue.", repository.FullName, input.N)
		}
		return "", refuse("%s#%d is not in this Workstream.", repository.FullName, input.N)
	}
	if err != nil {
		return "", err
	}
	if !row.Number.Valid {
		return "", refuse("%s#%d has no number.", repository.FullName, input.N)
	}
	if !row.Workstream.Valid || !strings.EqualFold(row.WorkstreamRepository.String, c.repository) || row.WorkstreamNumber.Int64 != c.workstream {
		return "", refuse("%s#%d is not in this Workstream.", repository.FullName, input.N)
	}
	id, err := e.setIssueBody(ctx, repository, row.ID, input.Body)
	if err != nil {
		return "", err
	}
	return e.sentOrLater(ctx, id,
		fmt.Sprintf("Changed the body of %s#%d.", repository.FullName, input.N),
		fmt.Sprintf("Mobius sends the body of %s#%d to GitHub later.", repository.FullName, input.N),
	)
}

// setIssueBody stores body on the issue row id and adds an outgoing body change, then sends it at once.
func (e *Engine) setIssueBody(ctx context.Context, repository github.Repository, id int64, body string) (int64, error) {
	e.copyWrite.Lock()
	e.labelChangeMu.Lock()
	var changeID int64
	var issueRepo string
	var issueNumber int64
	err := e.inTx(ctx, func(q *store.Queries) error {
		row, err := q.GetIssueByID(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return refuse("The issue is not in this Workstream.")
		}
		if err != nil {
			return err
		}
		if !row.Number.Valid {
			return refuse("The issue has no number.")
		}
		if !row.WorkstreamRepository.Valid || !row.WorkstreamNumber.Valid {
			return refuse("%s#%d is not in this Workstream.", row.Repository, row.Number.Int64)
		}
		issueRepo = row.Repository
		issueNumber = row.Number.Int64
		clean := e.WithoutChatLink(row.WorkstreamRepository.String, row.WorkstreamNumber.Int64, body)
		if _, err := q.SetIssueBody(ctx, store.SetIssueBodyParams{Body: clean, ID: id}); err != nil {
			return err
		}
		changeID, err = addIssueChange(ctx, q, repository.FullName, row.Number.Int64, id, bodyField)
		return err
	})
	if err == nil {
		repo := strings.ToLower(issueRepo)
		e.bodyChanges[repo]++
		e.bodyChangedAt[lowerRef(issueRepo, issueNumber)] = e.bodyChanges[repo]
	}
	e.labelChangeMu.Unlock()
	e.copyWrite.Unlock()
	if err != nil {
		return 0, err
	}
	e.publish(Change{Workstreams: true})
	if err := e.sendNow(ctx, repository, changeID); err != nil {
		return 0, err
	}
	return changeID, nil
}

// updateBlockers makes input.BlockedBy the open blockers of the issue input.N, which is an issue of the Workstream of c.
// It replaces the open blockers in blockers and adds an outgoing blockers change, then sends it at once. A closed
// blocker stays.
func (e *Engine) updateBlockers(ctx context.Context, c caller, repository github.Repository, input blockersInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	blockers := make([]issueAt, len(input.BlockedBy))
	for i, reference := range input.BlockedBy {
		var err error
		if blockers[i], err = e.issueAtOf(c, reference); err != nil {
			return "", err
		}
		if blockers[i].repository.FullName == repository.FullName && blockers[i].number == input.N {
			return "", refuse("%s#%d cannot block itself.", repository.FullName, input.N)
		}
	}
	row, err := e.queries.GetTreeIssue(ctx, store.GetTreeIssueParams{Repository: repository.FullName, Number: input.N})
	if errors.Is(err, sql.ErrNoRows) {
		issue, err := repository.Issue(ctx, input.N)
		if err != nil {
			return "", err
		}
		if issue == nil || issue.IsPullRequest() {
			return "", refuse("%s#%d is not an issue.", repository.FullName, input.N)
		}
		return "", refuse("%s#%d is not in this Workstream.", repository.FullName, input.N)
	}
	if err != nil {
		return "", err
	}
	if !row.Number.Valid || !row.Workstream.Valid || !strings.EqualFold(row.WorkstreamRepository.String, c.repository) || row.WorkstreamNumber.Int64 != c.workstream {
		return "", refuse("%s#%d is not in this Workstream.", repository.FullName, input.N)
	}
	resolved, err := e.resolveBlockers(ctx, blockers)
	if err != nil {
		return "", err
	}
	changeID, text, err := e.setIssueBlockers(ctx, repository, row.ID, input.N, resolved)
	if err != nil {
		return "", err
	}
	if changeID == 0 {
		return text, nil
	}
	return e.sentOrLater(ctx, changeID, text, text)
}

func (e *Engine) resolveBlockers(ctx context.Context, blockers []issueAt) ([]resolvedBlocker, error) {
	seen := map[issueRef]bool{}
	resolved := make([]resolvedBlocker, len(blockers))
	for i, blocker := range blockers {
		ref := lowerRef(blocker.repository.FullName, blocker.number)
		reference := fmt.Sprintf("%s#%d", blocker.repository.FullName, blocker.number)
		found, err := e.queries.GetTreeIssue(ctx, store.GetTreeIssueParams{Repository: blocker.repository.FullName, Number: blocker.number})
		if errors.Is(err, sql.ErrNoRows) {
			ghIssue, err := blocker.repository.Issue(ctx, blocker.number)
			if err != nil {
				return nil, err
			}
			if ghIssue == nil || ghIssue.IsPullRequest() {
				return nil, refuse("%s is not an issue.", reference)
			}
			if seen[ref] {
				return nil, refuse("%s is two times in blocked_by.", reference)
			}
			seen[ref] = true
			resolved[i] = resolvedBlocker{at: blocker, reference: reference, issue: ghIssue}
			continue
		}
		if err != nil {
			return nil, err
		}
		if seen[ref] {
			return nil, refuse("%s is two times in blocked_by.", reference)
		}
		seen[ref] = true
		resolved[i] = resolvedBlocker{at: blocker, reference: reference, id: found.ID}
	}
	return resolved, nil
}

// setIssueBlockers replaces the open blockers of issueID in blockers with resolved, adds an outgoing blockers change,
// and sends it at once.
func (e *Engine) setIssueBlockers(ctx context.Context, repository github.Repository, issueID, number int64, blockers []resolvedBlocker) (int64, string, error) {
	e.copyWrite.Lock()
	e.labelChangeMu.Lock()
	var changeID int64
	var added, removed []string
	err := e.inTx(ctx, func(q *store.Queries) error {
		row, err := q.GetIssueByID(ctx, issueID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !row.Workstream.Valid) {
			return refuse("%s#%d is not in this Workstream.", repository.FullName, number)
		}
		if err != nil {
			return err
		}
		current, err := q.ListBlockersOfIssue(ctx, issueID)
		if err != nil {
			return err
		}
		currentIDs := map[int64]bool{}
		for _, found := range current {
			currentIDs[found.ID] = true
		}
		wantedIDs := map[int64]bool{}
		for i, blocker := range blockers {
			if blocker.id == 0 {
				metadata, err := githubMetadata(blocker.issue, 0)
				if err != nil {
					return err
				}
				id, err := q.AddCopiedBlockerIssue(ctx, store.AddCopiedBlockerIssueParams{
					Repository: blocker.at.repository.FullName,
					Number:     blocker.at.number,
					Title:      blocker.issue.GetTitle(),
					Body:       blocker.issue.GetBody(),
					State:      blocker.issue.GetState(),
					Author:     blocker.issue.GetUser().GetLogin(),
					Kind:       issueKind(labelNames(blocker.issue)),
					Github:     metadata,
				})
				if err != nil {
					return err
				}
				blocker.id = id
				blockers[i].id = id
			}
			wantedIDs[blocker.id] = true
			if currentIDs[blocker.id] {
				continue
			}
			through, closes, err := e.blockerPath(ctx, q, blocker.at, issueAt{repository, number})
			if err != nil {
				return err
			}
			if closes {
				text := fmt.Sprintf("%s cannot block %s#%d: %s is already blocked by %s#%d", blocker.reference, repository.FullName, number, blocker.reference, repository.FullName, number)
				if len(through) > 0 {
					text += " through " + strings.Join(through, " and ")
				}
				return refuse("%s.", text)
			}
			added = append(added, blocker.reference)
		}
		for _, found := range current {
			if found.State == "open" && !wantedIDs[found.ID] {
				removed = append(removed, fmt.Sprintf("%s#%d", found.Repository, found.Number.Int64))
			}
		}
		if len(added) == 0 && len(removed) == 0 {
			return nil
		}
		if err := q.DeleteCopiedOpenBlockersOfIssue(ctx, issueID); err != nil {
			return err
		}
		for _, blocker := range blockers {
			if err := q.AddCopiedBlocker(ctx, store.AddCopiedBlockerParams{Issue: issueID, Blocker: blocker.id}); err != nil {
				return err
			}
		}
		changeID, err = addIssueChange(ctx, q, repository.FullName, number, issueID, blockersField)
		return err
	})
	if err == nil && changeID != 0 {
		repo := strings.ToLower(repository.FullName)
		e.blockerChanges[repo]++
		e.blockerChangedAt[lowerRef(repository.FullName, number)] = e.blockerChanges[repo]
	}
	e.labelChangeMu.Unlock()
	e.copyWrite.Unlock()
	if err != nil {
		return 0, "", err
	}
	change := blockerChange(added, removed)
	if changeID == 0 {
		return 0, fmt.Sprintf("The blockers of %s#%d already match.", repository.FullName, number), nil
	}
	e.publish(Change{Workstreams: true})
	if err := e.sendNow(ctx, repository, changeID); err != nil {
		return 0, "", err
	}
	return changeID, fmt.Sprintf("%s as blockers of %s#%d.", change, repository.FullName, number), nil
}

// blockerPath walks the open blockers of from in blockers, and the open blockers of those, until it reaches to. It tells
// if it reached to, and gives the references of the issues between from and to in path order. It does not walk past a
// blocker in a repository that the App cannot read.
func (e *Engine) blockerPath(ctx context.Context, q *store.Queries, from, to issueAt) (through []string, reached bool, err error) {
	keyOf := func(issue issueAt) issueRef { return lowerRef(issue.repository.FullName, issue.number) }
	parents := map[issueRef]issueAt{}
	visited := map[issueRef]bool{keyOf(from): true}
	queue := []issueAt{from}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		found, err := q.ListOpenBlockersOfIssue(ctx, store.ListOpenBlockersOfIssueParams{
			Repository: node.repository.FullName,
			Number:     node.number,
		})
		if err != nil {
			return nil, false, err
		}
		for _, blocker := range found {
			repository, readable := e.namedRepository(blocker.Repository, node.repository)
			if !readable {
				continue
			}
			next := issueAt{repository, blocker.Number}
			if keyOf(next) == keyOf(to) {
				for ; keyOf(node) != keyOf(from); node = parents[keyOf(node)] {
					through = append([]string{fmt.Sprintf("%s#%d", node.repository.FullName, node.number)}, through...)
				}
				return through, true, nil
			}
			if visited[keyOf(next)] {
				continue
			}
			visited[keyOf(next)] = true
			parents[keyOf(next)] = node
			queue = append(queue, next)
		}
	}
	return nil, false, nil
}

// blockerChange tells the changes of setIssueBlockers, for example "Added a#1 and removed b#2". It gives "" for no change.
func blockerChange(added, removed []string) string {
	var parts []string
	if len(added) > 0 {
		parts = append(parts, "Added "+strings.Join(added, " and "))
	}
	if len(removed) > 0 {
		verb := "removed "
		if len(parts) == 0 {
			verb = "Removed "
		}
		parts = append(parts, verb+strings.Join(removed, " and "))
	}
	return strings.Join(parts, " and ")
}

func (e *Engine) stopTaskTool(ctx context.Context, c caller, repository github.Repository, input taskInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	e.stopMu.Lock()
	defer e.stopMu.Unlock()
	task, err := e.workstreamTask(ctx, repository, c.repository, c.workstream, input.N)
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
	if hasLabel(issue, readyLabel) {
		// The dispatch continues a stopped task that has mobius:ready, so the label must go before the task stops.
		if err := repository.RemoveLabel(ctx, input.N, readyLabel); err != nil {
			return "", err
		}
	}
	if err := e.stopTask(ctx, repository, task, "Stopped by the Owner."); err != nil {
		return "", err
	}
	return fmt.Sprintf("Stopped the task of #%d.", input.N), nil
}

var (
	updateBranchInterval = 2 * time.Second
	updateBranchTimeout  = 30 * time.Second
)

func (e *Engine) updateBranchTool(ctx context.Context, c caller, repository github.Repository, input taskInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	task, err := e.workstreamTask(ctx, repository, c.repository, c.workstream, input.N)
	if err != nil {
		return "", err
	}
	if !task.PullRequest.Valid {
		return "", refuse("The task of %s has no open pull request.", taskEventRef(task, input.N))
	}
	if task.State == "working" {
		return "", refuse("The task of %s is working.", taskEventRef(task, input.N))
	}
	pullRequest, err := repository.PullRequest(ctx, task.PullRequest.Int64)
	if err != nil {
		return "", err
	}
	if pullRequest.GetState() != "open" {
		return "", refuse("The task of %s has no open pull request.", taskEventRef(task, input.N))
	}
	oldHead := pullRequest.GetHead().GetSHA()
	behind, err := repository.CommitsBehind(ctx, repository.DefaultBranch, oldHead)
	if err != nil {
		return "", err
	}
	if behind == 0 {
		return fmt.Sprintf("The branch of pull request %s is already up to date with %s, head %.8s.", taskEventRef(task, task.PullRequest.Int64), repository.DefaultBranch, oldHead), nil
	}
	err = repository.UpdatePullRequestBranch(ctx, task.PullRequest.Int64, oldHead)
	var refused *gh.ErrorResponse
	if errors.As(err, &refused) && refused.Response.StatusCode >= 400 && refused.Response.StatusCode < 500 {
		return "", refuse("%s", refused.Message)
	}
	if err != nil {
		return "", err
	}
	deadline := time.Now().Add(updateBranchTimeout)
	var newHead string
	for {
		pr, err := repository.PullRequest(ctx, task.PullRequest.Int64)
		if err != nil {
			return "", err
		}
		if pr.GetHead().GetSHA() != oldHead {
			newHead = pr.GetHead().GetSHA()
			break
		}
		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(min(updateBranchInterval, time.Until(deadline))):
		}
	}
	e.markPushed(repository, task.PullRequest.Int64)
	if newHead != "" {
		return fmt.Sprintf("Mobius updated the branch of pull request %s: new head %.8s, old head %.8s. The new head gets a new CI run, and a new review round only when an Implementer pushes.", taskEventRef(task, task.PullRequest.Int64), newHead, oldHead), nil
	}
	return fmt.Sprintf("GitHub accepted the update of pull request %s, but the head is still %.8s after 30 seconds. The next poll reads the pull request again.", taskEventRef(task, task.PullRequest.Int64), oldHead), nil
}

func (e *Engine) rerunFailedJobsTool(ctx context.Context, c caller, repository github.Repository, input taskInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	task, err := e.workstreamTask(ctx, repository, c.repository, c.workstream, input.N)
	if err != nil {
		return "", err
	}
	if !task.PullRequest.Valid {
		return "", refuse("The task of %s has no open pull request.", taskEventRef(task, input.N))
	}
	pullRequest, err := repository.PullRequest(ctx, task.PullRequest.Int64)
	if err != nil {
		return "", err
	}
	if pullRequest.GetState() != "open" {
		return "", refuse("The task of %s has no open pull request.", taskEventRef(task, input.N))
	}
	head := pullRequest.GetHead().GetSHA()
	workflows, err := repository.WorkflowRuns(ctx, head)
	if err != nil {
		return "", err
	}
	failed := slices.DeleteFunc(newestWorkflowRuns(workflows), func(run *gh.WorkflowRun) bool {
		return !failedWorkflowRun(run)
	})
	if len(failed) == 0 {
		return fmt.Sprintf("The head %.8s of pull request %s has no failed workflow run.", head, taskEventRef(task, task.PullRequest.Int64)), nil
	}
	slices.SortFunc(failed, func(a, b *gh.WorkflowRun) int {
		return cmp.Compare(a.GetID(), b.GetID())
	})
	var lines []string
	var startedIDs []string
	var startedNames []string
	storeRerun := func(err error) error {
		if len(startedIDs) == 0 {
			return err
		}
		storeErr := e.queries.SetTaskRerunHead(ctx, store.SetTaskRerunHeadParams{
			RerunHead: sql.NullString{String: head, Valid: true},
			RerunRuns: sql.NullString{String: strings.Join(startedIDs, ","), Valid: true},
			ID:        task.ID,
		})
		if err != nil {
			return errors.Join(fmt.Errorf("%w (already started again: %s)", err, strings.Join(startedNames, ", ")), storeErr)
		}
		if storeErr != nil {
			return fmt.Errorf("%w (already started again: %s)", storeErr, strings.Join(startedNames, ", "))
		}
		return nil
	}
	for _, run := range failed {
		attempt := max(run.GetRunAttempt(), 1)
		jobs, err := repository.RunAttemptJobs(ctx, run.GetID(), int64(attempt))
		if err != nil && !refusedByGitHub(err) {
			return "", storeRerun(err)
		}
		var running []string
		for _, job := range jobs {
			if job.GetStatus() != "completed" {
				running = append(running, job.GetName())
			}
		}
		if len(running) > 0 {
			lines = append(lines, fmt.Sprintf("%s (run %d, attempt %d): a job still runs: %s", run.GetName(), run.GetID(), attempt, strings.Join(running, ", ")))
			continue
		}
		var refused *gh.ErrorResponse
		started := "started again"
		err = repository.RerunFailedJobs(ctx, run.GetID())
		if errors.As(err, &refused) && refused.Response.StatusCode >= 400 && refused.Response.StatusCode < 500 {
			if slices.ContainsFunc(jobs, failedJob) {
				lines = append(lines, fmt.Sprintf("%s (run %d, attempt %d): GitHub refused: %s", run.GetName(), run.GetID(), attempt, refused.Message))
				continue
			}
			failedJobsMessage := refused.Message
			err = repository.RerunWorkflowRun(ctx, run.GetID())
			if errors.As(err, &refused) && refused.Response.StatusCode >= 400 && refused.Response.StatusCode < 500 {
				lines = append(lines, fmt.Sprintf("%s (run %d, attempt %d): GitHub refused the re-run of the failed jobs: %s. GitHub refused the full re-run: %s", run.GetName(), run.GetID(), attempt, failedJobsMessage, refused.Message))
				continue
			}
			started = "started again (full run)"
		}
		if err != nil {
			return "", storeRerun(err)
		}
		startedIDs = append(startedIDs, strconv.FormatInt(run.GetID(), 10))
		startedNames = append(startedNames, run.GetName())
		lines = append(lines, fmt.Sprintf("%s (run %d, attempt %d): %s", run.GetName(), run.GetID(), attempt, started))
	}
	if err := storeRerun(nil); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

func failedJob(job *gh.WorkflowJob) bool {
	switch job.GetConclusion() {
	case "failure", "cancelled", "timed_out":
		return true
	}
	return false
}

func (e *Engine) markReady(ctx context.Context, c caller, repository github.Repository, input taskInput) (string, error) {
	if input.N < 1 {
		return "", refuse("n must be 1 or more.")
	}
	if err := e.startOffRefused(ctx); err != nil {
		return "", err
	}
	if err := e.repairsOffRefused(ctx, repository.FullName, input.N); err != nil {
		return "", err
	}
	issue, err := repository.Issue(ctx, input.N)
	if err != nil {
		return "", err
	}
	if issue == nil || !e.TrustedAuthor(repository.AppSlug, issue.GetUser().GetLogin()) {
		return "", refuse("%s#%d is not an issue of a trusted author.", repository.FullName, input.N)
	}
	in, err := e.inWorkstream(ctx, c.repository, c.workstream, repository, input.N)
	if err != nil {
		return "", err
	}
	if !in {
		return "", refuse("%s#%d is not in this Workstream.", repository.FullName, input.N)
	}
	task, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: input.N})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil && task.State == "needs_human" {
		if err := removeNeedsHuman(ctx, repository, task); err != nil {
			return "", err
		}
		return fmt.Sprintf("Removed mobius:needs-human from %s#%d, so the task continues.", repository.FullName, input.N), nil
	}
	if _, _, err := repository.Client.Issues.AddLabelsToIssue(ctx, repository.Owner(), repository.Name(), int(input.N), []string{readyLabel}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Marked %s#%d ready.", repository.FullName, input.N), nil
}

func (e *Engine) replyThread(ctx context.Context, c caller, repository github.Repository, input taskThreadInput) (string, error) {
	if empty(input.Text) {
		return "", refuse("text must not be empty.")
	}
	text, err := e.withMark(ctx, c, repository.FullName, input.Text)
	if err != nil {
		return "", err
	}
	tasks, err := e.queries.ListLiveTasks(ctx, repository.FullName)
	if err != nil {
		return "", err
	}
	for _, task := range tasks {
		if task.WorkstreamRepository != c.repository || task.Workstream != c.workstream || !task.PullRequest.Valid {
			continue
		}
		change, replied, err := e.reply(ctx, repository, task.PullRequest.Int64, input.Thread, text)
		if err != nil {
			return "", err
		}
		if replied {
			return e.sentOrLater(ctx, change, fmt.Sprintf("Replied to %d.", input.Thread), fmt.Sprintf("Mobius posts the reply to %d later.", input.Thread))
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
		Repository:  repository.FullName,
		PullRequest: sql.NullInt64{Int64: input.N, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (task.WorkstreamRepository != c.repository || task.Workstream != c.workstream)) {
		return "", refuse("#%d is not the pull request of a live task in this Workstream.", input.N)
	}
	if err != nil {
		return "", err
	}
	text, err := e.withMark(ctx, c, repository.FullName, input.Text)
	if err != nil {
		return "", err
	}
	id, err := e.addOutgoingChange(ctx, e.queries, repository.FullName, input.N, 0, commentField, text)
	if err != nil {
		return "", err
	}
	if err := e.sendNow(ctx, repository, id); err != nil {
		return "", err
	}
	return e.sentOrLater(ctx, id, fmt.Sprintf("Commented on #%d.", input.N), fmt.Sprintf("Mobius posts the comment on #%d later.", input.N))
}

func checkWorkstream(c caller, input workstreamInput) error {
	if empty(input.Title) || empty(input.Brief) {
		return refuse("title and brief must not be empty.")
	}
	if len(input.Labels) > 0 && c.role != CoordinatorRole {
		return refuse("Only the Coordinator adds labels to a Workstream.")
	}
	for _, label := range input.Labels {
		if !slices.Contains(coordinatorWorkstreamLabels, label) {
			return refuse("The label %q is not allowed. The allowed labels are %s.", label, strings.Join(coordinatorWorkstreamLabels, ", "))
		}
	}
	return nil
}

func (e *Engine) createWorkstream(ctx context.Context, c caller, repository github.Repository, input workstreamInput) (string, error) {
	if err := checkWorkstream(c, input); err != nil {
		return "", err
	}
	action, approved := ctx.Value(approvedRun{}).(int64)
	workstream, change, err := e.addWorkstreamToCreate(ctx, repository, input, action, approved)
	if err != nil {
		return "", err
	}
	if err := e.sendNow(ctx, repository, change); err != nil {
		return "", err
	}
	row, err := e.queries.GetWorkstreamToCreate(ctx, workstream)
	if err == nil && !row.Number.Valid && approved {
		// The send that fills the number later posts the second Approval message. When it filled the number in the meantime, no message is due.
		var waits int64
		if waits, err = e.queries.SetPendingActionAwaitsNumber(ctx, action); err == nil && waits == 0 {
			row, err = e.queries.GetWorkstreamToCreate(ctx, workstream)
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", refuse("GitHub refused the write.")
	}
	if err != nil {
		return "", err
	}
	if !row.Number.Valid {
		return e.sentOrLater(ctx, change, "", "Mobius creates the Workstream on GitHub later. The number comes after the send.")
	}
	return fmt.Sprintf("Created the Workstream #%d.", row.Number.Int64), nil
}

// triageEndText starts the comment of the Mobius App that ends the triage of an issue.
const triageEndText = "The triage ends."

// endTriage comments on the issue of the Triager c with the link to the Workstream in workstreamRepository and closes
// the issue as completed. since is the time of the approved action. A comment change with the same link line from
// since on stays the only one, because a Triager reply can come between two runs of the step. An earlier comment with
// the same link line belongs to an earlier triage of the issue.
func (e *Engine) endTriage(ctx context.Context, c caller, workstreamRepository github.Repository, workstream int64, since string) error {
	repository, err := e.repository(c.repository)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("%s The Workstream is %s", triageEndText, issueLink(workstreamRepository.FullName, workstream))
	posted, err := e.queries.HasCommentChangeWithText(ctx, store.HasCommentChangeWithTextParams{Repository: repository.FullName, Number: c.issue, Part: line + "\n", Since: since})
	if err != nil {
		return err
	}
	if !posted {
		text, err := e.withMark(ctx, c, repository.FullName, line)
		if err != nil {
			return err
		}
		if err := e.postComment(ctx, repository, c.issue, text); err != nil {
			return err
		}
	}
	_, err = repository.CloseIssue(ctx, c.issue, "completed")
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

func (e *Engine) checkMoveTask(c caller, input moveInput) error {
	if _, err := e.organizationRepository(c.organization, input.Repository); err != nil {
		return err
	}
	if input.N < 1 || input.Workstream < 1 {
		return refuse("n and workstream must be 1 or more.")
	}
	if input.Workstream == c.workstream {
		return refuse("#%d is this Workstream.", input.Workstream)
	}
	return nil
}

func (e *Engine) moveTask(ctx context.Context, c caller, workstream github.Repository, input moveInput) (string, error) {
	if err := e.checkMoveTask(c, input); err != nil {
		return "", err
	}
	repository, err := e.organizationRepository(c.organization, input.Repository)
	if err != nil {
		return "", err
	}
	issue, err := issueOf(ctx, repository, input.N)
	if err != nil {
		return "", err
	}
	in, err := e.inWorkstream(ctx, workstream.FullName, c.workstream, repository, input.N)
	if err != nil {
		return "", err
	}
	if !in {
		return "", refuse("%s#%d is not in this Workstream.", repository.FullName, input.N)
	}
	if err := openWorkstream(ctx, workstream, input.Workstream); err != nil {
		return "", err
	}
	_, err = e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: repository.FullName, Issue: input.N})
	if err == nil {
		return "", refuse("%s#%d has a live task. Stop the task first.", repository.FullName, input.N)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	name, err := e.workstreamName(ctx, workstream.FullName, input.Workstream)
	if err != nil {
		return "", err
	}
	if _, _, err := workstream.Client.SubIssue.Add(ctx, workstream.Owner(), workstream.Name(), input.Workstream, gh.SubIssueRequest{SubIssueID: issue.GetID(), ReplaceParent: new(true)}); err != nil {
		return "", err
	}
	if err := e.recopyTrees(ctx, workstream, c.workstream, input.Workstream); err != nil {
		log.Printf("copy the trees of %s#%d and #%d: %v", workstream.FullName, c.workstream, input.Workstream, err)
	}
	return fmt.Sprintf("Moved %s#%d to the Workstream %s.", repository.FullName, input.N, name), nil
}

func (e *Engine) moveIssue(ctx context.Context, c caller, workstreams github.Repository, input moveIssueInput) (string, error) {
	repository, err := e.organizationRepository(c.organization, input.Repository)
	if err != nil {
		return "", err
	}
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
	if err := openWorkstream(ctx, workstreams, input.Workstream); err != nil {
		return "", err
	}
	target, err := e.workstreamName(ctx, workstreams.FullName, input.Workstream)
	if err != nil {
		return "", err
	}
	if _, _, err := workstreams.Client.SubIssue.Add(ctx, workstreams.Owner(), workstreams.Name(), input.Workstream, gh.SubIssueRequest{SubIssueID: issue.GetID(), ReplaceParent: new(true)}); err != nil {
		return "", err
	}
	owner, name := repository.Owner(), repository.Name()
	if hasLabel(issue, noWorkstreamLabel) {
		if _, err := repository.Client.Issues.RemoveLabelForIssue(ctx, owner, name, int(input.N), noWorkstreamLabel); err != nil {
			return "", err
		}
		if _, _, err := repository.Client.Issues.AddLabelsToIssue(ctx, owner, name, int(input.N), []string{readyLabel}); err != nil {
			return "", err
		}
	}
	if err := e.recopyTrees(ctx, workstreams, input.Workstream); err != nil {
		log.Printf("copy the tree of %s#%d: %v", workstreams.FullName, input.Workstream, err)
	}
	return fmt.Sprintf("Moved #%d to the Workstream %s.", input.N, target), nil
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
	name, err := e.workstreamName(ctx, repository.FullName, c.workstream)
	if err != nil {
		return "", err
	}
	if action, approved := ctx.Value(approvedRun{}).(int64); approved {
		old, err := e.currentBrief(ctx, repository, c.workstream)
		if err != nil {
			return "", err
		}
		if err := e.queries.SetPendingActionOldBrief(ctx, store.SetPendingActionOldBriefParams{OldBrief: old, ID: action}); err != nil {
			return "", err
		}
	}
	id, err := e.setBrief(ctx, repository, c.workstream, input.Brief)
	if err != nil {
		return "", err
	}
	if id == 0 {
		return "", refuse("The issue is not an open Workstream.")
	}
	return e.sentOrLater(ctx, id, fmt.Sprintf("Changed the Brief of %s.", name), fmt.Sprintf("Changed the Brief of %s. Mobius writes it to GitHub later.", name))
}

// recurringTimes gives the next 3 times of the cron expression in the local time zone, with the zone name. It refuses
// an expression that has no next time.
func (e *Engine) recurringTimes(expression string) (string, error) {
	times, err := e.NextTimes(expression, 3)
	if err != nil {
		return "", err
	}
	if len(times) == 0 {
		return "", refuse("the cron expression %q has no next time.", expression)
	}
	texts := make([]string, len(times))
	for i, t := range times {
		texts[i] = t.Format("Mon 2 Jan 15:04 MST")
	}
	return strings.Join(texts, ", "), nil
}

func checkCreateRecurring(_ caller, input createRecurringInput) error {
	return checkRecurringTask(input.Cron, input.Text)
}

func checkUpdateRecurring(_ caller, input updateRecurringInput) error {
	if input.ID < 1 {
		return refuse("id must be 1 or more.")
	}
	return checkRecurringTask(input.Cron, input.Text)
}

func checkDeleteRecurring(_ caller, input deleteRecurringInput) error {
	if input.ID < 1 {
		return refuse("id must be 1 or more.")
	}
	return nil
}

func (e *Engine) createRecurringTask(ctx context.Context, c caller, repository github.Repository, input createRecurringInput) (string, error) {
	task, err := e.AddRecurringTask(ctx, repository.FullName, c.workstream, input.Cron, input.Text)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Added the recurring task %d. Next time: %s.", task.ID, task.Next.UTC().Format(timeFormat)), nil
}

func (e *Engine) updateRecurringTask(ctx context.Context, c caller, repository github.Repository, input updateRecurringInput) (string, error) {
	task, err := e.UpdateRecurringTask(ctx, repository.FullName, c.workstream, input.ID, input.Cron, input.Text)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Changed the recurring task %d. Next time: %s.", task.ID, task.Next.UTC().Format(timeFormat)), nil
}

func (e *Engine) deleteRecurringTask(ctx context.Context, c caller, repository github.Repository, input deleteRecurringInput) (string, error) {
	if err := e.DeleteRecurringTask(ctx, repository.FullName, c.workstream, input.ID); err != nil {
		return "", err
	}
	return fmt.Sprintf("Deleted the recurring task %d.", input.ID), nil
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
	name, err := e.workstreamName(ctx, repository.FullName, input.Workstream)
	if err != nil {
		return "", err
	}
	text := fmt.Sprintf("Message of %s, approved by the Owner:\n\n%s", sender, input.Text)
	if c.role == TriagerRole {
		text += "\n\n" + issueLink(c.repository, c.issue)
	}
	sent := false
	if c.role == TriagerRole {
		sent, err = e.queries.HasLeadEvent(ctx, store.HasLeadEventParams{Repository: repository.FullName, Workstream: input.Workstream, Kind: kind, Payload: text})
		if err != nil {
			return "", err
		}
	}
	if !sent {
		if err := e.addLeadEvent(ctx, repository.FullName, input.Workstream, "", sql.NullInt64{}, kind, text); err != nil {
			return "", err
		}
	}
	if c.role == TriagerRole {
		id, _ := ctx.Value(approvedRun{}).(int64)
		action, err := e.queries.GetPendingAction(ctx, id)
		if err != nil {
			return "", err
		}
		if err := e.endTriage(ctx, c, repository, input.Workstream, action.Time); err != nil {
			return "", fmt.Errorf("the Lead of #%d got the message, but the end of the triage failed. Do not send the message again: %w", input.Workstream, err)
		}
	}
	return fmt.Sprintf("Sent the message to the Lead of %s.", name), nil
}

// withChatLink gives body with the link to the Lead chat of the Workstream at the end, one time. It removes the link
// lines of the same Workstream and the blank lines at the end of body first. It gives body with no change when the config
// has no ui_url.
func (e *Engine) withChatLink(repository github.Repository, workstream int64, body string) string {
	if e.config.UIURL == "" {
		return body
	}
	return e.WithoutChatLink(repository.FullName, workstream, body) + "\n\n" + e.chatLink(repository.FullName, workstream)
}

// WithoutChatLink gives body with no link line to the Lead chat of the Workstream and no white space at the end.
func (e *Engine) WithoutChatLink(repository string, workstream int64, body string) string {
	if e.config.UIURL != "" {
		link := e.chatLink(repository, workstream)
		lines := slices.DeleteFunc(strings.Split(body, "\n"), func(line string) bool { return strings.TrimSpace(line) == link })
		body = strings.Join(lines, "\n")
	}
	return strings.TrimRight(body, " \t\r\n")
}

func (e *Engine) chatLink(repository string, workstream int64) string {
	return fmt.Sprintf("[Open the Lead chat in Mobius](%s)", e.chatURL(repository, workstream))
}

func (e *Engine) chatURL(repository string, workstream int64) string {
	return fmt.Sprintf("%s/workstreams/%s/%d", e.config.UIURL, repository, workstream)
}

// mark gives the mark line of the role of c for a text in the repository with the name repository: the Role, and its issue or, for the Lead, its
// Workstream with the title. The Coordinator and the Curator have no issue. The Workstream is a link to the Lead chat
// when the config has ui_url.
func (e *Engine) mark(ctx context.Context, c caller, repository string) (string, error) {
	role := roleTitle(c.role)
	if c.role == LeadRole {
		ref := eventRef(repository, c.repository, c.workstream)
		if e.config.UIURL != "" {
			ref = fmt.Sprintf("[%s](%s)", ref, e.chatURL(c.repository, c.workstream))
		}
		copied, err := e.queries.GetCopiedWorkstream(ctx, store.GetCopiedWorkstreamParams{Repository: c.repository, Number: c.workstream})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		return strings.TrimSpace(fmt.Sprintf("— %s of %s %s", role, ref, copied.Title)), nil
	}
	if c.issue == 0 {
		return "— " + role, nil
	}
	return fmt.Sprintf("— %s of %s", role, eventRef(repository, c.repository, c.issue)), nil
}

// withMark gives body with the mark of c at the end, one time.
func (e *Engine) withMark(ctx context.Context, c caller, repository, body string) (string, error) {
	mark, err := e.mark(ctx, c, repository)
	if err != nil {
		return "", err
	}
	return runner.WithMark(body, mark), nil
}

// currentBrief reads the Brief of the Workstream from workstreams.
func (e *Engine) currentBrief(ctx context.Context, repository github.Repository, workstream int64) (string, error) {
	row, err := e.queries.GetWorkstream(ctx, store.GetWorkstreamParams{Repository: repository.FullName, Number: workstream})
	if errors.Is(err, sql.ErrNoRows) {
		return "", refuse("The Workstream issue does not exist.")
	}
	if err != nil {
		return "", err
	}
	return row.Body, nil
}

func (e *Engine) reportFinished(ctx context.Context, c caller, repository github.Repository, _ noInput) (string, error) {
	copied, err := e.queries.GetCopiedWorkstream(ctx, store.GetCopiedWorkstreamParams{Repository: repository.FullName, Number: c.workstream})
	if errors.Is(err, sql.ErrNoRows) {
		return "", refuse("The issue is not an open Workstream.")
	}
	if err != nil {
		return "", err
	}
	if copied.Evergreen {
		return "", refuse("An evergreen Workstream has no end.")
	}
	updated, err := e.queries.SetWorkstreamFinishedAt(ctx, store.SetWorkstreamFinishedAtParams{
		FinishedAt: sql.NullString{String: now(), Valid: true},
		Repository: repository.FullName,
		Number:     c.workstream,
	})
	if err != nil {
		return "", err
	}
	if updated == 0 {
		return "", refuse("The issue is not an open Workstream.")
	}
	e.publish(Change{Workstreams: true})
	return "Reported the Workstream as finished.", nil
}
