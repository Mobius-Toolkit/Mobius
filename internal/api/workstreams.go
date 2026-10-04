package api

import (
	"context"
	"net/http"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
)

// ListWorkstreamsRequest is the request of ListWorkstreams.
type ListWorkstreamsRequest struct{}

// Workstream is an open Workstream of a managed repository.
type Workstream struct {
	// Repository is the repository of the Workstream issue, as "owner/name"
	Repository string `gork:"repository"`
	// Number is the number of the Workstream issue
	Number int64 `gork:"number"`
	// Title is the title of the Workstream issue
	Title string `gork:"title"`
	// Brief is the body of the Workstream issue: the goal, the scope and the limits of the Workstream
	Brief string `gork:"brief"`
	// Autopilot is true when a trusted user added mobius:autopilot last
	Autopilot bool `gork:"autopilot"`
	// AllTasksClosed is true when the Workstream issue has sub-issues and each one is closed
	AllTasksClosed bool `gork:"allTasksClosed"`
}

// ListWorkstreamsResponse is the response of ListWorkstreams.
type ListWorkstreamsResponse struct {
	Body Envelope[[]Workstream]
}

// ListWorkstreams returns the open Workstreams from the local copy of GitHub, by repository and the newest first.
// The copy can be one poll interval old.
func (h *handlers) ListWorkstreams(ctx context.Context, _ ListWorkstreamsRequest) (*ListWorkstreamsResponse, error) {
	rows, err := h.queries.ListCopiedWorkstreams(ctx)
	if err != nil {
		return nil, err
	}
	workstreams := make([]Workstream, 0, len(rows))
	for _, row := range rows {
		workstreams = append(workstreams, Workstream{
			Repository:     row.Repository,
			Number:         row.Number,
			Title:          row.Title,
			Brief:          row.Body,
			Autopilot:      row.Autopilot,
			AllTasksClosed: row.AllTasksClosed,
		})
	}
	return &ListWorkstreamsResponse{Body: Envelope[[]Workstream]{Data: workstreams}}, nil
}

// SetAutopilotRequest is the request of SetAutopilot.
type SetAutopilotRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
		// Number is the number of the Workstream issue
		Number int64 `gork:"number"`
	}
	Body struct {
		// On turns Autopilot on when true, and off when false
		On bool `gork:"on"`
	}
}

// SetAutopilot turns Autopilot of the Workstream on or off. Mobius changes mobius:autopilot with the user token of the
// Owner, so the Owner must authorize the Mobius App first. It returns 409 with the steps when the Owner did not.
func (h *handlers) SetAutopilot(ctx context.Context, req SetAutopilotRequest) error {
	err := h.engine.SetAutopilot(ctx, req.Path.Owner+"/"+req.Path.Name, req.Path.Number, req.Body.On)
	if engine.Refused(err) {
		return api.NewHTTPError(http.StatusConflict, err.Error())
	}
	return err
}

// CompleteWorkstreamRequest is the request of CompleteWorkstream.
type CompleteWorkstreamRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
		// Number is the number of the Workstream issue
		Number int64 `gork:"number"`
	}
}

// CompleteWorkstream closes the Workstream issue as completed. Then Mobius ends the work of the Workstream, closes the
// open pull requests of its tasks, and closes the open issues below it. It returns 409 when the issue is not an open
// Workstream, or when the Workstream has no task or an open task.
func (h *handlers) CompleteWorkstream(ctx context.Context, req CompleteWorkstreamRequest) error {
	err := h.engine.CompleteWorkstream(ctx, req.Path.Owner+"/"+req.Path.Name, req.Path.Number)
	if engine.Refused(err) {
		return api.NewHTTPError(http.StatusConflict, err.Error())
	}
	return err
}
