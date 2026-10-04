package api

import (
	"context"
	"time"
)

// ListWorkstreamsRequest is the request of ListWorkstreams.
type ListWorkstreamsRequest struct{}

// Workstream is a Workstream that has rows in the database.
type Workstream struct {
	// Repository is the repository of the Workstream issue, as "owner/name"
	Repository string `gork:"repository"`
	// Number is the number of the Workstream issue
	Number int64 `gork:"number"`
	// Tasks is the number of tasks of the Workstream
	Tasks int64 `gork:"tasks"`
	// OpenTasks is the number of tasks that are not ended or stopped
	OpenTasks int64 `gork:"openTasks"`
	// LastActivity is the time of the latest row of the Workstream
	LastActivity time.Time `gork:"lastActivity"`
}

// ListWorkstreamsResponse is the response of ListWorkstreams.
type ListWorkstreamsResponse struct {
	Body []Workstream
}

// ListWorkstreams returns the Workstreams of the database, the most recently active first.
func (h *handlers) ListWorkstreams(ctx context.Context, _ ListWorkstreamsRequest) (*ListWorkstreamsResponse, error) {
	rows, err := h.queries.ListWorkstreams(ctx)
	if err != nil {
		return nil, err
	}
	workstreams := make([]Workstream, 0, len(rows))
	for _, row := range rows {
		lastActivity, err := time.Parse(time.RFC3339Nano, row.LastActivity)
		if err != nil {
			return nil, err
		}
		workstreams = append(workstreams, Workstream{
			Repository:   row.Repository,
			Number:       row.Workstream,
			Tasks:        row.Tasks,
			OpenTasks:    row.OpenTasks,
			LastActivity: lastActivity,
		})
	}
	return &ListWorkstreamsResponse{Body: workstreams}, nil
}
