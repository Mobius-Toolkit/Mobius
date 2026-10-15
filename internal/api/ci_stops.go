package api

import (
	"context"
	"fmt"
)

// CIStop is the stop of the dispatch of a repository because of a hard CI break on its default branch.
type CIStop struct {
	// Repository is the full name of the repository, as "owner/name"
	Repository string `gork:"repository"`
	// Branch is the default branch of the repository
	Branch string `gork:"branch"`
	// Workflow is the name of the workflow that fails
	Workflow string `gork:"workflow"`
	// RunUrl is the GitHub URL of the failed run
	RunURL string `gork:"runUrl"`
	// Issue is the number of the task issue of the break
	Issue int64 `gork:"issue"`
	// IssueUrl is the GitHub URL of the task issue
	IssueURL string `gork:"issueUrl"`
}

// ListCIStopsRequest is the request of ListCIStops.
type ListCIStopsRequest struct{}

// ListCIStopsResponse is the response of ListCIStops.
type ListCIStopsResponse struct {
	Body Envelope[[]CIStop]
}

// ListCIStops returns one item for each open hard CI break that has a task issue, in the order of the repositories
// and then of the break ids.
func (h *handlers) ListCIStops(ctx context.Context, _ ListCIStopsRequest) (*ListCIStopsResponse, error) {
	stops, err := h.ciStops(ctx)
	if err != nil {
		return nil, err
	}
	return &ListCIStopsResponse{Body: Envelope[[]CIStop]{Data: stops}}, nil
}

func (h *handlers) ciStops(ctx context.Context) ([]CIStop, error) {
	rows, err := h.engine.CIStops(ctx)
	if err != nil {
		return nil, err
	}
	branches := map[string]string{}
	for _, repository := range h.github.Repositories() {
		branches[repository.FullName] = repository.DefaultBranch
	}
	stops := make([]CIStop, 0, len(rows))
	for _, row := range rows {
		issue := max(row.Issue, -row.Issue)
		stops = append(stops, CIStop{
			Repository: row.Repository,
			Branch:     branches[row.Repository],
			Workflow:   row.WorkflowName,
			RunURL:     row.RunUrl,
			Issue:      issue,
			IssueURL:   fmt.Sprintf("https://github.com/%s/issues/%d", row.Repository, issue),
		})
	}
	return stops, nil
}
