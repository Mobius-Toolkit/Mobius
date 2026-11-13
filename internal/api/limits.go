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
	// Users are the Role bindings, the settings and the open sessions that use the Harness. The list is empty when nothing uses the Harness
	Users []PauseUser `gork:"users"`
}

// PauseUser is a user of a paused Harness.
type PauseUser struct {
	// Kind is role for a Role binding, workstream for a setting of a Workstream, task for a setting of a task, and session for an open session that waits for the pause
	Kind string `gork:"kind" validate:"oneof=role workstream task session"`
	// Role is the name of the Role, for example implementer
	Role string `gork:"role"`
	// Binding names the binding as "<harness> · <model>" for a Role with more than one binding, and is empty for the other users
	Binding string `gork:"binding"`
	// Repository is the repository of the issue of the user: the Workstream or the task, or the Workstream of a session with no task. It is empty for a Role binding and for a session with no Workstream
	Repository string `gork:"repository"`
	// Number is the number of the issue of the user. It is 0 for a Role binding and for a session with no Workstream
	Number int64 `gork:"number"`
	// Title is the title of the issue of the user
	Title string `gork:"title"`
	// WorkstreamRepository is the repository of the Workstream of the user
	WorkstreamRepository string `gork:"workstreamRepository"`
	// Workstream is the number of the Workstream issue of the user. It is 0 for a Role binding and for a session with no Workstream
	Workstream int64 `gork:"workstream"`
	// WorkstreamTitle is the title of the Workstream of the user
	WorkstreamTitle string `gork:"workstreamTitle"`
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
		users := make([]PauseUser, 0, len(row.Users))
		for _, user := range row.Users {
			users = append(users, PauseUser(user))
		}
		pauses = append(pauses, Pause{Harness: row.Harness, PausedUntil: pausedUntil, Users: users})
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
