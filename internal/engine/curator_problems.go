package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// retryPrefix is the first line of each retry prompt.
var retryPrefix, _, _ = strings.Cut(retryPrompt, "\n")

// sessionProblems gives the items that show what went wrong in the sessions of repository after since, as prompt
// sections. The sections of the kinds with no item are not there.
func (e *Engine) sessionProblems(ctx context.Context, repository string, since time.Time) (string, error) {
	after := since.Format(time.RFC3339Nano)
	owner, err := e.ownerMessages(ctx, repository, after)
	if err != nil {
		return "", err
	}
	cannotDos, reviews, fixRounds, err := e.toolCallItems(ctx, repository, after)
	if err != nil {
		return "", err
	}
	hung, err := e.hungSessions(ctx, repository, after)
	if err != nil {
		return "", err
	}
	retries, err := e.retryPrompts(ctx, repository, after)
	if err != nil {
		return "", err
	}
	return itemSection("Messages of the Owner in the Lead chats", "Each item has the last reply of the Lead before the message of the Owner, and the message.", owner) +
		itemSection("Results of cannot_do", "Each item has the reason of an Implementer that could not do its task.", cannotDos) +
		itemSection("Hung sessions", "Each item is a session that Mobius stopped after the last retry.", hung) +
		itemSection("Retry prompts after a hang", "Each item is a prompt that Mobius sent to a session with no activity.", retries) +
		itemSection("Fix rounds that repeat", "Each item is a task with two or more fix rounds. It has the findings of the Lead and of the Reviewer.", fixRounds) +
		itemSection("Review findings", "Each item is a review of the Reviewer.", reviews), nil
}

func (e *Engine) ownerMessages(ctx context.Context, repository, since string) ([]string, error) {
	messages, err := e.queries.ListOwnerMessagesSince(ctx, store.ListOwnerMessagesSinceParams{Repository: repository, Since: since})
	if err != nil {
		return nil, err
	}
	var items []string
	for _, message := range messages {
		text := "Owner:\n" + clip(message.Text)
		if message.LeadText != "" {
			text = "Lead:\n" + clip(message.LeadText) + "\n\n" + text
		}
		items = append(items, problemItem(fmt.Sprintf("Workstream %d, %s", message.Workstream, message.Time), text))
	}
	return items, nil
}

// toolCallItems gives the items of the cannot_do calls, of the submit_review calls, and of the tasks with repeated fix
// rounds.
func (e *Engine) toolCallItems(ctx context.Context, repository, since string) (cannotDos, reviews, fixRounds []string, err error) {
	calls, err := e.queries.ListToolCallsSince(ctx, store.ListToolCallsSinceParams{Repository: repository, Since: since})
	if err != nil {
		return nil, nil, nil, err
	}
	leadFindings := map[int64][]string{}
	reviewFindings := map[int64][]string{}
	for _, call := range calls {
		var row struct {
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(call.Json), &row); err != nil {
			return nil, nil, nil, err
		}
		heading := fmt.Sprintf("%s, %s", whose(call.Role, call.Issue), call.Time)
		switch row.Tool {
		case "cannot_do":
			var input reasonInput
			if err := json.Unmarshal(row.Arguments, &input); err != nil {
				return nil, nil, nil, err
			}
			cannotDos = append(cannotDos, problemItem(heading, clip(input.Reason)))
		case "submit_review":
			var input reviewInput
			if err := json.Unmarshal(row.Arguments, &input); err != nil {
				return nil, nil, nil, err
			}
			var text strings.Builder
			text.WriteString(input.Body)
			for _, comment := range input.Comments {
				fmt.Fprintf(&text, "\n- %s:%d: %s", comment.Path, comment.Line, comment.Body)
			}
			reviews = append(reviews, problemItem(heading, clip(text.String())))
			if call.Issue.Valid {
				reviewFindings[call.Issue.Int64] = append(reviewFindings[call.Issue.Int64], "Review of the Reviewer, "+call.Time+":\n"+clip(text.String()))
			}
		case "start_fix_round":
			var input findingsInput
			if err := json.Unmarshal(row.Arguments, &input); err != nil {
				return nil, nil, nil, err
			}
			leadFindings[input.N] = append(leadFindings[input.N], "Findings of the Lead, "+call.Time+":\n"+clip(input.Findings))
		}
	}
	tasks, err := e.queries.ListRepeatedFixRoundTasksSince(ctx, store.ListRepeatedFixRoundTasksSinceParams{Repository: repository, FixRounds: curatorRepeatedFixRounds, Since: since})
	if err != nil {
		return nil, nil, nil, err
	}
	for _, task := range tasks {
		findings := slices.Concat(leadFindings[task.Issue], reviewFindings[task.Issue])
		fixRounds = append(fixRounds, problemItem(fmt.Sprintf("Issue #%d, %d fix rounds", task.Issue, task.FixRounds), strings.Join(findings, "\n\n")))
	}
	return cannotDos, reviews, fixRounds, nil
}

func (e *Engine) hungSessions(ctx context.Context, repository, since string) ([]string, error) {
	sessions, err := e.queries.ListHungSessionsSince(ctx, store.ListHungSessionsSinceParams{Repository: repository, Since: since})
	if err != nil {
		return nil, err
	}
	var items []string
	for _, session := range sessions {
		items = append(items, problemItem(fmt.Sprintf("Session %d, %s", session.ID, whose(session.Role, session.Issue)), "Ended at "+session.EndedAt.String+"."))
	}
	return items, nil
}

func (e *Engine) retryPrompts(ctx context.Context, repository, since string) ([]string, error) {
	prompts, err := e.queries.ListRetryPromptsSince(ctx, store.ListRetryPromptsSinceParams{Repository: repository, Since: since, Prefix: retryPrefix})
	if err != nil {
		return nil, err
	}
	var items []string
	for _, prompt := range prompts {
		items = append(items, problemItem(fmt.Sprintf("%s, %s", whose(prompt.Role, prompt.Issue), prompt.Time), clip(prompt.Text)))
	}
	return items, nil
}

// whose names the role of a session, and its issue when it has one.
func whose(role string, issue sql.NullInt64) string {
	if !issue.Valid {
		return role
	}
	return fmt.Sprintf("%s, issue #%d", role, issue.Int64)
}

// clip cuts text to curatorMaxText characters and marks the cut.
func clip(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= curatorMaxText {
		return text
	}
	return string(runes[:curatorMaxText]) + curatorCut
}

func problemItem(heading, text string) string {
	return fmt.Sprintf("## %s\n\n%s\n\n", heading, text)
}

// itemSection gives the items as a prompt section, oldest first. It keeps the newest curatorMaxItems items and tells
// how many older items it leaves out.
func itemSection(title, intro string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	left := max(0, len(items)-curatorMaxItems)
	var section strings.Builder
	fmt.Fprintf(&section, "# %s\n\n%s\n\n", title, intro)
	if left > 0 {
		fmt.Fprintf(&section, "Mobius left out the %d oldest items.\n\n", left)
	}
	for _, text := range items[left:] {
		section.WriteString(text)
	}
	return section.String()
}
