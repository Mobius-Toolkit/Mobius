package api

import (
	"context"
	"time"
)

// Pause is the usage-limit pause of a Harness.
type Pause struct {
	// Harness is the name of the paused Harness
	Harness string `gork:"harness"`
	// PausedUntil is the end of the pause
	PausedUntil time.Time `gork:"pausedUntil"`
}

// ListPausesRequest is the request of ListPauses.
type ListPausesRequest struct{}

// ListPausesResponse is the response of ListPauses.
type ListPausesResponse struct {
	Body Envelope[[]Pause]
}

// ListPauses returns the pause of each Harness that has one.
func (h *handlers) ListPauses(ctx context.Context, _ ListPausesRequest) (*ListPausesResponse, error) {
	pauses, err := h.pauses(ctx)
	if err != nil {
		return nil, err
	}
	return &ListPausesResponse{Body: Envelope[[]Pause]{Data: pauses}}, nil
}

func (h *handlers) pauses(ctx context.Context) ([]Pause, error) {
	rows, err := h.engine.Pauses(ctx)
	if err != nil {
		return nil, err
	}
	pauses := make([]Pause, 0, len(rows))
	for _, row := range rows {
		pausedUntil, err := time.Parse(time.RFC3339Nano, row.PausedUntil)
		if err != nil {
			return nil, err
		}
		pauses = append(pauses, Pause{Harness: row.Harness, PausedUntil: pausedUntil})
	}
	return pauses, nil
}

// ResumeRequest is the request of Resume.
type ResumeRequest struct {
	Path struct {
		// Harness is the name of the paused Harness
		Harness string `gork:"harness"`
	}
}

// Resume ends the pause of a Harness now, so the sessions that wait for it send their prompts again. A Harness with
// no pause is no error.
func (h *handlers) Resume(ctx context.Context, req ResumeRequest) error {
	return h.engine.Resume(ctx, req.Path.Harness)
}
