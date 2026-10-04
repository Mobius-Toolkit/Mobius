package api

import "context"

// ResumeRequest is the request of Resume.
type ResumeRequest struct {
	Path struct {
		// ID is the id of the usage-limit Inbox item of the pause
		ID int64 `gork:"id"`
	}
}

// Resume ends the pause of a Harness now, so the sessions that wait for it send their prompts again. It also closes the Inbox item.
func (h *handlers) Resume(ctx context.Context, req ResumeRequest) error {
	return h.engine.Resume(ctx, req.Path.ID)
}
