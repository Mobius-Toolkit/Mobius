package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// approvedRun is the key of the context of the run of an action that the Owner approved. Its value is the id of the action.
type approvedRun struct{}

const (
	interruptedActionError = "Mobius stopped while the action ran. The action can have run. Check the result before you approve again"
	unsavedActionError     = "The action ran, but Mobius could not save the result. Check the result before you approve again"
)

// ErrNoAction is the error of a pending action that does not exist.
var ErrNoAction = errors.New("there is no such action")

// ActionArgument is one argument of a pending action.
type ActionArgument struct {
	Name string
	// Value is the text of a string argument, or the JSON text of another argument.
	Value string
}

// approvedTool is tool for a tool whose call waits for the approval of the Owner. A call checks its arguments, stores a
// pending action with the title of its subject, and returns at once. The check refuses the arguments that the run would refuse without a read of GitHub. The run of the tool starts when the Owner approves.
func approvedTool[In any](e *Engine, c caller, resolve func(caller) (github.Repository, error), name, description string, properties map[string]any, title func(context.Context, github.Repository, In) (string, error), check func(caller, In) error, run func(context.Context, caller, github.Repository, In) (string, error)) mcp.Tool {
	return mcp.Tool{
		Name:        name,
		Description: description,
		Properties:  properties,
		Run: func(ctx context.Context, arguments json.RawMessage) (string, error) {
			if ctx.Value(approvedRun{}) != nil {
				return call(ctx, c, resolve, name, arguments, run)
			}
			c.agent.touch()
			var text string
			repository, err := resolve(c)
			if err == nil {
				var input In
				input, err = decode[In](name, arguments)
				if err == nil {
					err = check(c, input)
				}
				var heading string
				if err == nil {
					heading, err = title(ctx, repository, input)
				}
				if err == nil {
					text, err = e.holdAction(ctx, c, name, heading, arguments)
				}
			}
			if recordErr := e.recordCall(ctx, c.session, name, arguments, text, err); recordErr != nil {
				return "", recordErr
			}
			return text, err
		},
	}
}

func actionRole(role string) string {
	switch role {
	case LeadRole:
		return "lead"
	case TriagerRole:
		return "triager"
	}
	return "coordinator"
}

func actionCaller(action store.PendingAction) caller {
	role := CoordinatorRole
	switch action.Role {
	case "lead":
		role = LeadRole
	case "triager":
		role = TriagerRole
	}
	return caller{role: role, organization: action.Organization, repository: action.Repository, workstream: action.Workstream, issue: action.Issue}
}

// approvedToolOf gives the tool name of the role of c, if its call waits for the approval of the Owner. It builds no other tool.
func (e *Engine) approvedToolOf(c caller, name string) (mcp.Tool, bool) {
	coordinator := c.role == CoordinatorRole
	lead := c.role == LeadRole
	switch {
	case name == "message_lead":
		return e.messageLeadTool(c), true
	case name == "create_workstream":
		return e.createWorkstreamTool(c), true
	case name == "tell_curator" && coordinator:
		return e.tellCuratorTool(c), true
	case name == "move_task" && lead:
		return e.moveTaskTool(c), true
	case name == "edit_brief" && lead:
		return e.editBriefTool(c), true
	}
	return mcp.Tool{}, false
}

func (e *Engine) messageLeadTool(c caller) mcp.Tool {
	description := "Send a message to the Lead of an open Workstream. Call it at once with the exact message. The tool returns at once, and Mobius sends the message only after the Owner approves it. The result arrives later as an Approval message. A new call with the same workstream replaces the waiting action for that Workstream, and the result names the replaced action."
	if c.role == TriagerRole {
		description = "Send a message to the Lead of an open Workstream in the issue repository of the organization. Mobius adds the link to the issue to the message. Call it at once with the target Workstream and the exact message. The tool returns at once, and Mobius sends the message only after the Owner approves it in the Inbox of Mobius. Then Mobius closes the issue with a link to the Workstream. A failed action or a rejection arrives as a comment of Mobius on the issue. A new call with the same workstream replaces the waiting action for that Workstream, and the result names the replaced action."
	}
	resolve := e.callerRepository
	if c.role == TriagerRole {
		resolve = e.triagerWorkstreamRepository
	}
	return approvedTool(e, c, resolve, "message_lead",
		description,
		map[string]any{
			"workstream": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the Workstream issue that the Owner selects."},
			"text":       map[string]any{"type": "string", "minLength": 1, "description": "The message for the Lead."},
		},
		func(ctx context.Context, repository github.Repository, input messageLeadInput) (string, error) {
			name, err := e.workstreamName(ctx, repository.FullName, input.Workstream)
			return "Message to the Lead of " + name, err
		},
		checkMessageLead,
		e.messageLead)
}

func (e *Engine) createWorkstreamTool(c caller) mcp.Tool {
	description := "Create a Workstream: an issue with mobius:workstream. Call it at once with the exact title and Brief. The tool returns at once, and Mobius creates the Workstream only after the Owner approves it. The result arrives later as an Approval message. A new call replaces the waiting action of this tool, and the result names the replaced action."
	properties := workstreamProperties
	switch c.role {
	case CoordinatorRole:
		description += " Add mobius:evergreen or mobius:maintenance in labels only when the Owner asks for that label."
		properties = coordinatorWorkstreamProperties
	case TriagerRole:
		description = "Create a Workstream in the issue repository of the organization: an issue with mobius:workstream. Call it at once with the exact title and Brief. The tool returns at once, and Mobius creates the Workstream only after the Owner approves it in the Inbox of Mobius. Then Mobius closes the issue with a link to the Workstream. A failed action or a rejection arrives as a comment of Mobius on the issue. A new call replaces the waiting action of this tool, and the result names the replaced action."
	}
	resolve := e.callerRepository
	if c.role == TriagerRole {
		resolve = e.triagerWorkstreamRepository
	}
	return approvedTool(e, c, resolve, "create_workstream", description,
		properties,
		func(_ context.Context, _ github.Repository, input workstreamInput) (string, error) {
			return `Create the Workstream "` + input.Title + `"`, nil
		},
		checkWorkstream,
		e.createWorkstream)
}

func (e *Engine) moveTaskTool(c caller) mcp.Tool {
	return approvedTool(e, c, e.callerRepository, "move_task",
		"Make a task of this Workstream a sub-issue of a different open Workstream in the repository of this Workstream. Call it at once. The tool returns at once, and Mobius moves the task only after the Owner approves it. The result arrives later as an Approval message. The task must not be in progress when the Owner approves. A new call with the same repository and n replaces the waiting action for that task, and the result names the replaced action.",
		map[string]any{
			"repository": repositoryProperty,
			"n":          map[string]any{"type": "integer", "minimum": 1, "description": "The number of the task issue."},
			"workstream": map[string]any{"type": "integer", "minimum": 1, "description": "The number of the target Workstream issue in the repository of this Workstream."},
		},
		func(ctx context.Context, repository github.Repository, input moveInput) (string, error) {
			name, err := e.workstreamName(ctx, repository.FullName, input.Workstream)
			return fmt.Sprintf("Move %s#%d to the Workstream %s", input.Repository, input.N, name), err
		},
		e.checkMoveTask,
		e.moveTask)
}

func (e *Engine) editBriefTool(c caller) mcp.Tool {
	return approvedTool(e, c, e.callerRepository, "edit_brief",
		"Change the Brief of this Workstream: set the body of the Workstream issue. Call it at once with the full new Brief and the reason. The tool returns at once, and Mobius changes the Brief only after the Owner approves it. The result arrives later as an Approval message. A new call replaces the waiting action of this tool, and the result names the replaced action.",
		map[string]any{
			"brief":  map[string]any{"type": "string", "minLength": 1, "description": "The full new Brief."},
			"reason": map[string]any{"type": "string", "minLength": 1, "description": "Why the Brief must change."},
		},
		func(ctx context.Context, repository github.Repository, _ editBriefInput) (string, error) {
			name, err := e.workstreamName(ctx, repository.FullName, c.workstream)
			return "Change the Brief of " + name, err
		},
		checkEditBrief,
		e.editBrief)
}

func (e *Engine) tellCuratorTool(c caller) mcp.Tool {
	return approvedTool(e, c, noRepository, "tell_curator",
		"Send a request to the Curator of a repository. The Curator changes the memory file of the repository. Call it at once with the exact request. The tool returns at once, and Mobius sends the request only after the Owner approves it. The result arrives later as an Approval message. A new call with the same repository replaces the waiting action for that repository, and the result names the replaced action.",
		map[string]any{
			"repository": map[string]any{"type": "string", "minLength": 1, "description": "The full name of the repository (owner/name) in the organization of the chat."},
			"text":       map[string]any{"type": "string", "minLength": 1, "description": "The request for the Curator."},
		},
		func(_ context.Context, _ github.Repository, input tellCuratorInput) (string, error) {
			return "Request to the Curator of " + input.Repository, nil
		},
		e.checkTellCurator,
		e.tellCurator)
}

func actionChat(action store.PendingAction) ChatKey {
	return ChatKey{Organization: action.Organization, Repository: action.Repository, Workstream: action.Workstream}
}

// ActionArguments gives the arguments of the JSON object arguments in their order.
func ActionArguments(arguments string) ([]ActionArgument, error) {
	decoder := json.NewDecoder(strings.NewReader(arguments))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var found []ActionArgument
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		text := string(value)
		if strings.HasPrefix(text, `"`) {
			if err := json.Unmarshal(value, &text); err != nil {
				return nil, err
			}
		}
		found = append(found, ActionArgument{Name: name.(string), Value: text})
	}
	return found, nil
}

// holdAction stores the call of the tool of c as a pending action, and gives the Owner its card in the chat and its
// Inbox item. The new action replaces each earlier pending action of the same chat, role, tool, issue and target.
func (e *Engine) holdAction(ctx context.Context, c caller, tool, title string, arguments json.RawMessage) (string, error) {
	listed, err := ActionArguments(string(arguments))
	if err != nil {
		return "", err
	}
	action, err := e.queries.AddPendingAction(ctx, store.AddPendingActionParams{
		Organization: c.organization,
		Repository:   c.repository,
		Workstream:   c.workstream,
		Issue:        c.issue,
		Role:         actionRole(c.role),
		Tool:         tool,
		Arguments:    string(arguments),
		Time:         now(),
	})
	if err != nil {
		return "", err
	}
	replaced, err := e.replaceActions(ctx, action)
	if err != nil {
		return "", err
	}
	var message store.ChatMessage
	if c.role != TriagerRole {
		if message, err = e.addChatMessage(ctx, actionChat(action), actionAuthor, title, "", "", nil); err != nil {
			return "", err
		}
	}
	var text strings.Builder
	for _, argument := range listed {
		fmt.Fprintf(&text, "%s: %s\n", argument.Name, argument.Value)
	}
	itemParams := store.AddInboxItemParams{
		Kind:                 approvalKind,
		Organization:         action.Organization,
		Repository:           action.Repository,
		WorkstreamRepository: action.Repository,
		Workstream:           action.Workstream,
		Title:                title,
		Reason:               "Waits for your approval",
		Text:                 strings.TrimSuffix(text.String(), "\n"),
		Time:                 now(),
	}
	if c.role == TriagerRole {
		itemParams.Repository = action.Repository
		itemParams.Issue = action.Issue
		itemParams.Link = issueLink(action.Repository, action.Issue)
	}
	item, err := e.queries.AddInboxItem(ctx, itemParams)
	if err != nil {
		return "", err
	}
	action, err = e.queries.LinkPendingAction(ctx, store.LinkPendingActionParams{
		ChatMessage: sql.NullInt64{Int64: message.ID, Valid: message.ID != 0},
		InboxItem:   sql.NullInt64{Int64: item.ID, Valid: true},
		ID:          action.ID,
	})
	if err != nil {
		return "", err
	}
	e.publish(Change{Inbox: &item})
	e.publish(Change{Action: &action})
	if c.role != TriagerRole {
		e.publish(Change{Message: &message})
		if err := e.publishUnread(ctx, actionChat(action)); err != nil {
			return "", err
		}
	}
	result := fmt.Sprintf("The action waits for the approval of the Owner. Its ID is %d.", item.ID)
	for _, old := range replaced {
		result += fmt.Sprintf(" It replaces the pending action %d, which does not run.", old.InboxItem.Int64)
	}
	return result, nil
}

// actionTarget gives the target of the arguments of the call of tool. A new call replaces only an earlier call with the
// same target.
func actionTarget(tool, arguments string) (string, error) {
	var input struct {
		Workstream int64  `json:"workstream"`
		Repository string `json:"repository"`
		N          int64  `json:"n"`
	}
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return "", err
	}
	switch tool {
	case "message_lead":
		return fmt.Sprint(input.Workstream), nil
	case "tell_curator":
		return input.Repository, nil
	case "move_task":
		return fmt.Sprintf("%s#%d", input.Repository, input.N), nil
	}
	return "", nil
}

// actionName gives the tool, the Inbox item ID and a short summary of action. The ID is the one that the agent got from
// the tool.
func (e *Engine) actionName(ctx context.Context, action store.PendingAction) (string, error) {
	summary := ""
	var input struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(action.Arguments), &input); err != nil {
		return "", err
	}
	if input.Text != "" {
		summary, _, _ = strings.Cut(strings.TrimSpace(input.Text), "\n")
		if utf8.RuneCountInString(summary) > 80 {
			summary = string([]rune(summary)[:80])
		}
	} else if action.InboxItem.Valid {
		item, err := e.queries.GetInboxItem(ctx, action.InboxItem.Int64)
		if err != nil {
			return "", err
		}
		summary = item.Title
	}
	name := fmt.Sprintf("`%s` %d", action.Tool, action.InboxItem.Int64)
	if summary != "" {
		name += " (" + summary + ")"
	}
	return name, nil
}

// workstreamName gives the number and the title of a Workstream of the local copy, or only the number when the copy has
// no such Workstream.
func (e *Engine) workstreamName(ctx context.Context, repository string, number int64) (string, error) {
	copied, err := e.queries.GetCopiedWorkstream(ctx, store.GetCopiedWorkstreamParams{Repository: repository, Number: number})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Sprintf("#%d", number), nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("#%d %s", number, copied.Title), nil
}

// replaceActions marks each pending action that action replaces as replaced, and dismisses its Inbox item. It gives the
// replaced actions.
func (e *Engine) replaceActions(ctx context.Context, action store.PendingAction) ([]store.PendingAction, error) {
	old, err := e.queries.ListReplaceablePendingActions(ctx, store.ListReplaceablePendingActionsParams{
		Organization: action.Organization,
		Repository:   action.Repository,
		Workstream:   action.Workstream,
		Issue:        action.Issue,
		Role:         action.Role,
		Tool:         action.Tool,
		ID:           action.ID,
	})
	if err != nil {
		return nil, err
	}
	target, err := actionTarget(action.Tool, action.Arguments)
	if err != nil {
		return nil, err
	}
	var done []store.PendingAction
	for _, found := range old {
		foundTarget, err := actionTarget(found.Tool, found.Arguments)
		if err != nil {
			return nil, err
		}
		if foundTarget != target {
			continue
		}
		replaced, err := e.queries.ReplacePendingAction(ctx, store.ReplacePendingActionParams{DecidedAt: sql.NullString{String: now(), Valid: true}, ID: found.ID})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := e.dismissAction(ctx, replaced); err != nil {
			return nil, err
		}
		e.publish(Change{Action: &replaced})
		done = append(done, replaced)
	}
	return done, nil
}

func (e *Engine) dismissAction(ctx context.Context, action store.PendingAction) error {
	if !action.InboxItem.Valid {
		return nil
	}
	return e.Dismiss(ctx, action.InboxItem.Int64)
}

// ApproveAction runs the pending action id with its stored arguments. When the run succeeds, the action is done, and the
// agent gets the result. When the run fails, the action stays pending with the error, and the agent gets the error.
// Of two requests at the same time, only one runs the action. It returns ErrNoAction for an action that does not
// exist, and a refusal for an action that does not wait for the approval.
func (e *Engine) ApproveAction(ctx context.Context, id int64, message string) (store.PendingAction, error) {
	if e.sealed() {
		return store.PendingAction{}, refuse("Mobius restarts for an upgrade. Decide after the restart.")
	}
	pending, err := e.queries.GetPendingAction(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.PendingAction{}, ErrNoAction
	}
	if err != nil {
		return store.PendingAction{}, err
	}
	name, err := e.actionName(ctx, pending)
	if err != nil {
		return store.PendingAction{}, err
	}
	action, err := e.queries.ClaimPendingAction(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.PendingAction{}, refuse("This action does not wait for your approval.")
	}
	if err != nil {
		return store.PendingAction{}, err
	}
	ctx = context.WithoutCancel(ctx)
	result, err := e.runAction(ctx, action)
	if err != nil {
		action, failErr := e.queries.FailPendingAction(ctx, store.FailPendingActionParams{Error: err.Error(), ID: id})
		if failErr != nil {
			return store.PendingAction{}, failErr
		}
		e.publish(Change{Action: &action})
		text := fmt.Sprintf("The Owner approved %s, and it failed: %s. The action still waits for the approval of the Owner.", name, strings.TrimSuffix(action.Error, "."))
		return action, e.postResult(ctx, action, text)
	}
	action, err = e.queries.FinishPendingAction(ctx, store.FinishPendingActionParams{
		Result:    result,
		Message:   message,
		DecidedAt: sql.NullString{String: now(), Valid: true},
		ID:        id,
	})
	if err != nil {
		_, failErr := e.queries.FailPendingAction(ctx, store.FailPendingActionParams{Error: unsavedActionError, ID: id})
		return store.PendingAction{}, errors.Join(err, failErr)
	}
	e.publish(Change{Action: &action})
	text := fmt.Sprintf("The Owner approved %s. The result: %s", name, action.Result)
	return action, errors.Join(e.dismissAction(ctx, action), e.postResult(ctx, action, withMessage(text, message)))
}

// RejectAction rejects the pending action id. No action runs, and the agent gets the message of the Owner. It returns
// ErrNoAction for an action that does not exist, and a refusal for an action that does not wait for the approval or
// that runs now.
func (e *Engine) RejectAction(ctx context.Context, id int64, message string) (store.PendingAction, error) {
	if e.sealed() {
		return store.PendingAction{}, refuse("Mobius restarts for an upgrade. Decide after the restart.")
	}
	pending, err := e.queries.GetPendingAction(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.PendingAction{}, ErrNoAction
	}
	if err != nil {
		return store.PendingAction{}, err
	}
	name, err := e.actionName(ctx, pending)
	if err != nil {
		return store.PendingAction{}, err
	}
	action, err := e.queries.RejectPendingAction(ctx, store.RejectPendingActionParams{
		Message:   message,
		DecidedAt: sql.NullString{String: now(), Valid: true},
		ID:        id,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return store.PendingAction{}, refuse("This action does not wait for your approval.")
	}
	if err != nil {
		return store.PendingAction{}, err
	}
	e.publish(Change{Action: &action})
	text := fmt.Sprintf("The Owner rejected %s.", name)
	return action, errors.Join(e.dismissAction(ctx, action), e.postResult(ctx, action, withMessage(text, message)))
}

// postResult gives the text of the decision on action to its agent. The Triager has no chat: a failed action or a
// rejection is a comment on its issue that starts a Triager turn, and a done action has ended the triage.
func (e *Engine) postResult(ctx context.Context, action store.PendingAction, text string) error {
	if action.Role != actionRole(TriagerRole) {
		return e.postApproval(ctx, actionChat(action), text)
	}
	if action.State == "done" {
		return nil
	}
	repository, err := e.repository(action.Repository)
	if err != nil {
		return err
	}
	return e.commentToTriager(ctx, repository, action.Issue, text)
}

// runAction runs the tool of action with the caller of its chat.
func (e *Engine) runAction(ctx context.Context, action store.PendingAction) (string, error) {
	tool, ok := e.approvedToolOf(actionCaller(action), action.Tool)
	if !ok {
		return "", fmt.Errorf("the role %s has no approved tool %s", action.Role, action.Tool)
	}
	return tool.Run(context.WithValue(ctx, approvedRun{}, action.ID), json.RawMessage(action.Arguments))
}

func withMessage(text, message string) string {
	if strings.TrimSpace(message) == "" {
		return text
	}
	return text + "\n\nThe message of the Owner: " + message
}
