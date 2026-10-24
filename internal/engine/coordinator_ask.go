package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// coordinatorAsk replaces the open question item of the Coordinator chat of the organization with a new question item.
// The answer of the Owner arrives later as a chat message (AnswerInboxItem).
func (e *Engine) coordinatorAsk(ctx context.Context, c caller, _ github.Repository, input askInput) (string, error) {
	if err := refuseQuestions(input.Text, input.Questions); err != nil {
		return "", err
	}
	questions, err := questionsJSON(input.Questions)
	if err != nil {
		return "", err
	}
	items, err := e.Inbox(ctx)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if isCoordinatorQuestion(item) && item.Organization == c.organization {
			if err := e.Dismiss(ctx, item.ID); err != nil {
				return "", err
			}
		}
	}
	title := strings.TrimSpace(input.Text)
	if len(input.Questions) > 0 {
		title = strings.TrimSpace(input.Questions[0].Text)
	}
	title, _, _ = strings.Cut(title, "\n")
	item, err := e.addQuestion(ctx, store.AddInboxItemParams{
		Kind:         questionKind,
		Organization: c.organization,
		Title:        title,
		Reason:       "The Coordinator asks a question",
		Text:         input.Text,
		Questions:    questions,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("The question waits for the Owner. Its ID is %d. The answer arrives later as a chat message.", item.ID), nil
}

func isCoordinatorQuestion(item store.InboxItem) bool {
	return item.Kind == questionKind && item.Repository == "" && item.Issue == 0
}
