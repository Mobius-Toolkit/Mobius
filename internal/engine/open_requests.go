package engine

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

type openRequest struct {
	id    int64
	text  string
	title string
	time  string
}

// openRequests gives the section of a first prompt with the open requests of the chat key, in the same match as
// withdraw and, for a Lead, its open questions on GitHub.
func (e *Engine) openRequests(ctx context.Context, key ChatKey) (string, error) {
	role := actionRole(CoordinatorRole)
	var questions []store.InboxItem
	var err error
	if key.Workstream == 0 {
		questions, err = e.queries.ListOpenCoordinatorQuestions(ctx, key.Organization)
	} else {
		role = actionRole(LeadRole)
		questions, err = e.queries.ListOpenLeadQuestions(ctx, store.ListOpenLeadQuestionsParams{WorkstreamRepository: key.Repository, Workstream: key.Workstream})
	}
	if err != nil {
		return "", err
	}
	var requests []openRequest
	for _, item := range questions {
		text := "question"
		if item.Issue != 0 {
			name := fmt.Sprintf("#%d", item.Issue)
			if item.Repository != key.Repository {
				name = item.Repository + name
			}
			text += " on " + name
		}
		requests = append(requests, openRequest{item.ID, text, item.Title, item.Time})
	}
	actions, err := e.queries.ListOpenActionRequests(ctx, store.ListOpenActionRequestsParams{Role: role, Organization: key.Organization, Repository: key.Repository, Workstream: key.Workstream})
	if err != nil {
		return "", err
	}
	for _, action := range actions {
		requests = append(requests, openRequest{action.ID, "action " + action.Tool, action.Title, action.Time})
	}
	var section strings.Builder
	section.WriteString("# Open requests\n\n")
	if len(requests) == 0 {
		section.WriteString("You have no open request.\n\n")
	}
	slices.SortFunc(requests, func(a, b openRequest) int { return cmp.Compare(a.id, b.id) })
	for _, request := range requests {
		t, err := time.Parse(time.RFC3339Nano, request.time)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&section, "- %d %s (%s): %s\n", request.id, request.text, t.UTC().Format(timeFormat), request.title)
	}
	if len(requests) > 0 {
		section.WriteString("\n")
	}
	return section.String(), nil
}
