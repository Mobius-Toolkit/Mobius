package api

import (
	"context"
	"net/http"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
)

// IssueSetting is the Harness, the model and the effort that the Owner sets for the sessions of a Role in a task or a
// Workstream.
type IssueSetting struct {
	// Harness is the agent program
	Harness string `gork:"harness"`
	// Model is the model
	Model string `gork:"model"`
	// Effort is the effort level. It is empty for a Harness with no effort levels.
	Effort string `gork:"effort"`
}

// NextSetting is the binding of the next session of a Role in a Workstream.
type NextSetting struct {
	// Harness is the agent program
	Harness string `gork:"harness"`
	// Model is the model
	Model string `gork:"model"`
	// Effort is the effort level, or empty for a Harness with no effort levels
	Effort string `gork:"effort"`
	// Own is true when the Workstream has its own setting, and false when the binding comes from the Role
	Own bool `gork:"own"`
}

func nextSetting(setting engine.NextSetting) NextSetting {
	return NextSetting(setting)
}

// IssuePath is the path of a request about an issue.
type IssuePath struct {
	// Owner is the owner of the repository
	Owner string `gork:"owner"`
	// Name is the name of the repository
	Name string `gork:"name"`
	// Number is the number of the issue
	Number int64 `gork:"number"`
}

// SetTaskImplementerRequest is the request of SetTaskImplementer.
type SetTaskImplementerRequest struct {
	Path IssuePath
	Body IssueSetting
}

// ClearTaskImplementerRequest is the request of ClearTaskImplementer.
type ClearTaskImplementerRequest struct {
	Path IssuePath
}

// SetTaskImplementer sets the Harness, the model and the effort of the Implementer of a task. The next Implementer
// round of the task uses them. It returns 422 with the reason when the values fail a check.
func (h *handlers) SetTaskImplementer(ctx context.Context, req SetTaskImplementerRequest) error {
	return h.set(ctx, "implementer", req.Path, req.Body)
}

// ClearTaskImplementer removes the Implementer setting of a task. It returns 204 also when the task has no setting.
func (h *handlers) ClearTaskImplementer(ctx context.Context, req ClearTaskImplementerRequest) error {
	return h.clear(ctx, "implementer", req.Path)
}

// SetWorkstreamImplementerRequest is the request of SetWorkstreamImplementer.
type SetWorkstreamImplementerRequest struct {
	Path IssuePath
	Body IssueSetting
}

// ClearWorkstreamImplementerRequest is the request of ClearWorkstreamImplementer.
type ClearWorkstreamImplementerRequest struct {
	Path IssuePath
}

// SetWorkstreamImplementer sets the Harness, the model and the effort of the Implementer of each task of a Workstream
// that has no setting of its own. It returns 409 when the issue is not an open Workstream, and 422 with the reason
// when the values fail a check.
func (h *handlers) SetWorkstreamImplementer(ctx context.Context, req SetWorkstreamImplementerRequest) error {
	return h.setWorkstream(ctx, "implementer", req.Path, req.Body)
}

// ClearWorkstreamImplementer removes the Implementer setting of a Workstream. It returns 409 when the issue is not an
// open Workstream.
func (h *handlers) ClearWorkstreamImplementer(ctx context.Context, req ClearWorkstreamImplementerRequest) error {
	return h.clearWorkstream(ctx, "implementer", req.Path)
}

// SetWorkstreamLeadRequest is the request of SetWorkstreamLead.
type SetWorkstreamLeadRequest struct {
	Path IssuePath
	Body IssueSetting
}

// ClearWorkstreamLeadRequest is the request of ClearWorkstreamLead.
type ClearWorkstreamLeadRequest struct {
	Path IssuePath
}

// SetWorkstreamLead sets the Harness, the model and the effort of the Lead of a Workstream. The next Lead session uses
// them. It returns 409 when the issue is not an open Workstream, and 422 with the reason when the values fail a check.
func (h *handlers) SetWorkstreamLead(ctx context.Context, req SetWorkstreamLeadRequest) error {
	return h.setWorkstream(ctx, "lead", req.Path, req.Body)
}

// ClearWorkstreamLead removes the Lead setting of a Workstream. It returns 409 when the issue is not an open Workstream.
func (h *handlers) ClearWorkstreamLead(ctx context.Context, req ClearWorkstreamLeadRequest) error {
	return h.clearWorkstream(ctx, "lead", req.Path)
}

// SetWorkstreamOperatorRequest is the request of SetWorkstreamOperator.
type SetWorkstreamOperatorRequest struct {
	Path IssuePath
	Body IssueSetting
}

// ClearWorkstreamOperatorRequest is the request of ClearWorkstreamOperator.
type ClearWorkstreamOperatorRequest struct {
	Path IssuePath
}

// SetWorkstreamOperator sets the Harness, the model and the effort of the Operator of a Workstream. The next Operator
// session uses them. It returns 409 when the issue is not an open Workstream, and 422 with the reason when the values
// fail a check.
func (h *handlers) SetWorkstreamOperator(ctx context.Context, req SetWorkstreamOperatorRequest) error {
	return h.setWorkstream(ctx, "operator", req.Path, req.Body)
}

// ClearWorkstreamOperator removes the Operator setting of a Workstream. It returns 409 when the issue is not an open
// Workstream.
func (h *handlers) ClearWorkstreamOperator(ctx context.Context, req ClearWorkstreamOperatorRequest) error {
	return h.clearWorkstream(ctx, "operator", req.Path)
}

func (h *handlers) set(ctx context.Context, role string, path IssuePath, setting IssueSetting) error {
	err := h.engine.SetSetting(ctx, role, path.Owner+"/"+path.Name, path.Number, config.Harness(setting.Harness), setting.Model, setting.Effort)
	return roleError(err)
}

func (h *handlers) clear(ctx context.Context, role string, path IssuePath) error {
	return roleError(h.engine.ClearSetting(ctx, role, path.Owner+"/"+path.Name, path.Number))
}

func (h *handlers) setWorkstream(ctx context.Context, role string, path IssuePath, setting IssueSetting) error {
	if err := h.openWorkstream(ctx, path); err != nil {
		return err
	}
	return h.set(ctx, role, path, setting)
}

func (h *handlers) clearWorkstream(ctx context.Context, role string, path IssuePath) error {
	if err := h.openWorkstream(ctx, path); err != nil {
		return err
	}
	return h.clear(ctx, role, path)
}

func (h *handlers) openWorkstream(ctx context.Context, path IssuePath) error {
	err := h.engine.CheckOpenWorkstream(ctx, path.Owner+"/"+path.Name, path.Number)
	if engine.Refused(err) {
		return api.NewHTTPError(http.StatusConflict, err.Error())
	}
	return err
}
