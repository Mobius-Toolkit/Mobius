package engine

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// RoleSetting is a Role with its merged binding.
type RoleSetting struct {
	// Role is the name of the Role in config.toml, for example reviewer.
	Role    string
	Binding config.RoleBinding
	// UI tells for each field if a row of role_settings gives it. Else config.toml gives it.
	UI RoleFields
}

// RoleFields has a flag for each field of a RoleBinding.
type RoleFields struct {
	Harness, Model, Effort, Max bool
}

// mergedRoles gives the Roles of config.toml with the role_settings rows over them.
func (e *Engine) mergedRoles() config.Roles {
	e.rolesMu.RLock()
	defer e.rolesMu.RUnlock()
	return e.roles
}

// roleBinding gives the merged binding of role, a Role of the sessions table.
func (e *Engine) roleBinding(role string) (config.RoleBinding, bool) {
	roles := e.mergedRoles()
	return roleBinding(&roles, role)
}

// namedBinding gives the binding of the Role name in roles, or nil.
func namedBinding(roles *config.Roles, name string) *config.RoleBinding {
	for _, b := range roles.Bindings() {
		if b.Role == name {
			return b.Binding
		}
	}
	return nil
}

// loadRoles merges the rows of role_settings over the Roles of config.toml. It fails when the result is not valid.
func (e *Engine) loadRoles(ctx context.Context) error {
	rows, err := e.queries.ListRoleSettings(ctx)
	if err != nil {
		return err
	}
	merged := e.config.Roles
	changed := make([]string, 0, len(rows))
	for _, row := range rows {
		overlay(namedBinding(&merged, row.Role), row)
		changed = append(changed, row.Role)
	}
	if err := e.checkRoles(&merged, changed...); err != nil {
		return fmt.Errorf("role settings of the UI: %w", err)
	}
	e.rolesMu.Lock()
	e.roles = merged
	e.rolesMu.Unlock()
	return nil
}

// overlay sets the fields of binding that row gives.
func overlay(binding *config.RoleBinding, row store.RoleSetting) {
	if row.Harness.Valid {
		binding.Harness = config.Harness(row.Harness.String)
	}
	if row.Model.Valid {
		binding.Model = row.Model.String
	}
	if row.Effort.Valid {
		binding.Effort = row.Effort.String
	}
	if row.Max.Valid {
		binding.Max = int(row.Max.Int64)
	}
}

// checkRoles checks roles with the rules of config.toml. It also checks that the program of the Harness of each Role in
// changed is on the PATH of the agents.
func (e *Engine) checkRoles(roles *config.Roles, changed ...string) error {
	if err := config.CheckRoles(roles); err != nil {
		return err
	}
	path := runner.AgentPath(e.config.DataDir, e.agents.Path)
	for _, role := range changed {
		program := runner.Program(namedBinding(roles, role).Harness)
		if len(runner.Missing(path, program)) > 0 {
			return fmt.Errorf("roles.%s.harness: %s is not on the PATH of the agents", role, program)
		}
	}
	return nil
}

// Roles gives each Role in the order of config.Roles.Bindings, with the merged binding.
func (e *Engine) Roles(ctx context.Context) ([]RoleSetting, error) {
	e.rolesMu.RLock()
	defer e.rolesMu.RUnlock()
	rows, err := e.queries.ListRoleSettings(ctx)
	if err != nil {
		return nil, err
	}
	var settings []RoleSetting
	for _, b := range e.roles.Bindings() {
		setting := RoleSetting{Role: b.Role, Binding: *b.Binding}
		if index := slices.IndexFunc(rows, func(row store.RoleSetting) bool { return row.Role == b.Role }); index >= 0 {
			row := rows[index]
			setting.UI = RoleFields{Harness: row.Harness.Valid, Model: row.Model.Valid, Effort: row.Effort.Valid, Max: row.Max.Valid}
		}
		settings = append(settings, setting)
	}
	return settings, nil
}

// SetRole sets the binding of the Role role in the UI. The next session of the Role uses it, and a session that runs
// keeps its binding. It refuses a binding that config.toml would refuse, a max below 1, and a Harness whose program is
// not on the PATH of the agents. When the Harness, the model or the effort changes, it also refuses a binding whose
// access check fails. A field that is equal to the field of config.toml is not stored.
func (e *Engine) SetRole(ctx context.Context, role string, binding config.RoleBinding) error {
	e.roleWriteMu.Lock()
	defer e.roleWriteMu.Unlock()
	merged := e.mergedRoles()
	target := namedBinding(&merged, role)
	if target == nil {
		return refuse("The Role %s does not exist.", role)
	}
	if binding.Max < 1 {
		return refuse("roles.%s.max: must be 1 or more", role)
	}
	current := *target
	*target = binding
	if err := e.checkRoles(&merged, role); err != nil {
		return refuse("%s", err)
	}
	if err := e.checkChange(ctx, current, binding); err != nil {
		return err
	}
	e.rolesMu.Lock()
	defer e.rolesMu.Unlock()
	base := namedBinding(&e.config.Roles, role)
	row := store.UpsertRoleSettingParams{Role: role, UpdatedAt: now()}
	if binding.Harness != base.Harness {
		row.Harness = sql.NullString{String: string(binding.Harness), Valid: true}
	}
	if binding.Model != base.Model {
		row.Model = sql.NullString{String: binding.Model, Valid: true}
	}
	if binding.Effort != base.Effort {
		row.Effort = sql.NullString{String: binding.Effort, Valid: true}
	}
	if binding.Max != base.Max {
		row.Max = sql.NullInt64{Int64: int64(binding.Max), Valid: true}
	}
	var err error
	if row.Harness.Valid || row.Model.Valid || row.Effort.Valid || row.Max.Valid {
		err = e.queries.UpsertRoleSetting(ctx, row)
	} else {
		err = e.queries.DeleteRoleSetting(ctx, role)
	}
	if err != nil {
		return err
	}
	e.applyRoles(merged)
	return nil
}

// ResetRole removes the UI values of the Role role, so it uses config.toml again. It runs the same refusals as SetRole.
func (e *Engine) ResetRole(ctx context.Context, role string) error {
	e.roleWriteMu.Lock()
	defer e.roleWriteMu.Unlock()
	merged := e.mergedRoles()
	target := namedBinding(&merged, role)
	if target == nil {
		return refuse("The Role %s does not exist.", role)
	}
	current := *target
	*target = *namedBinding(&e.config.Roles, role)
	if err := e.checkRoles(&merged, role); err != nil {
		return refuse("%s", err)
	}
	if err := e.checkChange(ctx, current, *target); err != nil {
		return err
	}
	e.rolesMu.Lock()
	defer e.rolesMu.Unlock()
	if err := e.queries.DeleteRoleSetting(ctx, role); err != nil {
		return err
	}
	e.applyRoles(merged)
	return nil
}

// applyRoles makes merged the Roles of the engine and wakes the sessions that wait for a slot, because a max can
// change. The caller holds the write lock of rolesMu.
func (e *Engine) applyRoles(merged config.Roles) {
	e.roles = merged
	e.workers.changed.notify()
}
