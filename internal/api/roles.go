package api

import (
	"context"
	"net/http"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
)

// RoleFields tells for each field of a Role if the UI gives it. false means config.toml.
type RoleFields struct {
	// Harness is true when the UI gives the Harness
	Harness bool `gork:"harness"`
	// Model is true when the UI gives the model
	Model bool `gork:"model"`
	// Effort is true when the UI gives the effort level
	Effort bool `gork:"effort"`
	// Max is true when the UI gives the session limit
	Max bool `gork:"max"`
}

// Role is the binding of a Role to a Harness, with the source of each value.
type Role struct {
	// Role is the name of the Role
	Role string `gork:"role"`
	// Harness is the agent program of the Role
	Harness string `gork:"harness"`
	// Model is the model of the Role
	Model string `gork:"model"`
	// Effort is the effort level of the Role, or empty for a Harness with no effort levels
	Effort string `gork:"effort"`
	// Max is the number of sessions of the Role that run at the same time
	Max int `gork:"max"`
	// UI tells which values come from the UI
	UI RoleFields `gork:"ui"`
}

// ListRolesRequest is the request of ListRoles.
type ListRolesRequest struct{}

// ListRolesResponse is the response of ListRoles.
type ListRolesResponse struct {
	Body Envelope[[]Role]
}

// ListRoles returns each Role with its values, in the order of config.toml.
func (h *handlers) ListRoles(ctx context.Context, _ ListRolesRequest) (*ListRolesResponse, error) {
	settings, err := h.engine.Roles(ctx)
	if err != nil {
		return nil, err
	}
	roles := make([]Role, 0, len(settings))
	for _, setting := range settings {
		roles = append(roles, role(setting))
	}
	return &ListRolesResponse{Body: Envelope[[]Role]{Data: roles}}, nil
}

func role(setting engine.RoleSetting) Role {
	return Role{
		Role: setting.Role, Harness: string(setting.Binding.Harness), Model: setting.Binding.Model, Effort: setting.Binding.Effort,
		Max: setting.Binding.Max, UI: RoleFields(setting.UI),
	}
}

// SetRoleRequest is the request of SetRole.
type SetRoleRequest struct {
	Path struct {
		// Role is the name of the Role
		Role string `gork:"role" validate:"oneof=lead coordinator triager implementer researcher reviewer judge curator diagnoser"`
	}
	Body struct {
		// Harness is the agent program of the Role
		Harness string `gork:"harness"`
		// Model is the model of the Role
		Model string `gork:"model"`
		// Effort is the effort level of the Role. It is empty for a Harness with no effort levels.
		Effort string `gork:"effort"`
		// Max is the number of sessions of the Role that run at the same time
		Max int `gork:"max"`
	}
}

// ResetRoleRequest is the request of ResetRole.
type ResetRoleRequest struct {
	Path struct {
		// Role is the name of the Role
		Role string `gork:"role" validate:"oneof=lead coordinator triager implementer researcher reviewer judge curator diagnoser"`
	}
}

// RoleResponse is the response of SetRole and ResetRole.
type RoleResponse struct {
	Body Envelope[Role]
}

// SetRole sets the values of a Role. The next session of the Role uses them. It returns 422 with the reason when the
// values fail a check.
func (h *handlers) SetRole(ctx context.Context, req SetRoleRequest) (*RoleResponse, error) {
	binding := config.RoleBinding{Harness: config.Harness(req.Body.Harness), Model: req.Body.Model, Effort: req.Body.Effort, Max: req.Body.Max}
	if err := h.engine.SetRole(ctx, req.Path.Role, binding); err != nil {
		return nil, roleError(err)
	}
	return h.role(ctx, req.Path.Role)
}

// ResetRole removes the UI values of a Role, so it uses config.toml again. It returns 422 with the reason when the
// values of config.toml fail a check with the UI values of the other Roles.
func (h *handlers) ResetRole(ctx context.Context, req ResetRoleRequest) (*RoleResponse, error) {
	if err := h.engine.ResetRole(ctx, req.Path.Role); err != nil {
		return nil, roleError(err)
	}
	return h.role(ctx, req.Path.Role)
}

func (h *handlers) role(ctx context.Context, name string) (*RoleResponse, error) {
	settings, err := h.engine.Roles(ctx)
	if err != nil {
		return nil, err
	}
	for _, setting := range settings {
		if setting.Role == name {
			return &RoleResponse{Body: Envelope[Role]{Data: role(setting)}}, nil
		}
	}
	return nil, api.NewHTTPError(http.StatusNotFound, "The Role "+name+" does not exist.")
}

func roleError(err error) error {
	if engine.Refused(err) {
		return api.NewHTTPError(http.StatusUnprocessableEntity, err.Error())
	}
	return err
}
