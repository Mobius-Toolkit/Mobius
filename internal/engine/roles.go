package engine

import (
	"context"
	"database/sql"
	"errors"
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
	// More are the Implementer bindings after the first binding, in the order of their id. It is empty for each other Role.
	More []ImplementerBinding
}

// ImplementerBinding is a row of implementer_bindings.
type ImplementerBinding struct {
	ID      int64
	Binding config.RoleBinding
}

// ErrNoImplementerBinding is the error of a change or a removal of a binding that the Implementer does not have.
var ErrNoImplementerBinding = errors.New("the Implementer has no such binding")

// RoleFields has a flag for each field of a RoleBinding.
type RoleFields struct {
	Harness, Model, Effort, Context, Max bool
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
	moreRows, err := e.queries.ListImplementerBindings(ctx)
	if err != nil {
		return err
	}
	more := make([]ImplementerBinding, 0, len(moreRows))
	for _, row := range moreRows {
		binding := config.RoleBinding{Harness: config.Harness(row.Harness), Model: row.Model, Effort: row.Effort, Context: int(row.Context), Max: int(row.Max)}
		if err := e.checkImplementer(binding); err != nil {
			return fmt.Errorf("role settings of the UI: %w", err)
		}
		more = append(more, ImplementerBinding{ID: row.ID, Binding: binding})
	}
	e.rolesMu.Lock()
	e.roles = merged
	e.moreBindings = more
	e.rolesMu.Unlock()
	return nil
}

// checkImplementer checks binding as the binding of the Implementer with the rules of checkRoles.
func (e *Engine) checkImplementer(binding config.RoleBinding) error {
	roles := e.config.Roles
	roles.Implementer = binding
	return e.checkRoles(&roles, "implementer")
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
	if row.Context.Valid {
		binding.Context = int(row.Context.Int64)
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
			setting.UI = RoleFields{Harness: row.Harness.Valid, Model: row.Model.Valid, Effort: row.Effort.Valid, Context: row.Context.Valid, Max: row.Max.Valid}
		}
		if b.Role == "implementer" {
			setting.More = slices.Clone(e.moreBindings)
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
	if err := e.storeRole(ctx, e.queries, role, binding); err != nil {
		return err
	}
	e.applyRoles(merged)
	return nil
}

// storeRole writes the fields of binding that differ from config.toml to the role_settings row of role with q. It
// deletes the row when no field differs.
func (e *Engine) storeRole(ctx context.Context, q *store.Queries, role string, binding config.RoleBinding) error {
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
	if binding.Context != base.Context {
		row.Context = sql.NullInt64{Int64: int64(binding.Context), Valid: true}
	}
	if binding.Max != base.Max {
		row.Max = sql.NullInt64{Int64: int64(binding.Max), Valid: true}
	}
	if row.Harness.Valid || row.Model.Valid || row.Effort.Valid || row.Context.Valid || row.Max.Valid {
		return q.UpsertRoleSetting(ctx, row)
	}
	return q.DeleteRoleSetting(ctx, role)
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
// change. It also moves each waiting session whose Harness is not the new Harness of its Role, and closes each kept
// Implementer session that follows the Role and has another binding. The caller holds the write lock of rolesMu.
func (e *Engine) applyRoles(merged config.Roles) {
	e.roles = merged
	e.moveWaiters(&merged)
	e.closeStaleKept(merged.Implementer)
	e.publish(Change{Pauses: true})
	e.workers.changed.notify()
}

// checkImplementerChange gives the refusals of SetRole for binding as a binding of the Implementer that replaces
// current: a max below 1, a binding that config.toml would refuse, a Harness whose program is not on the PATH of the
// agents, and a failed access check when the Harness, the model or the effort changes.
func (e *Engine) checkImplementerChange(ctx context.Context, current, binding config.RoleBinding) error {
	if binding.Max < 1 {
		return refuse("roles.implementer.max: must be 1 or more")
	}
	if err := e.checkImplementer(binding); err != nil {
		return refuse("%s", err)
	}
	return e.checkChange(ctx, current, binding)
}

// AddImplementerBinding adds binding at the end of the list of the Implementer bindings and gives its id. It runs the
// refusals of SetRole.
func (e *Engine) AddImplementerBinding(ctx context.Context, binding config.RoleBinding) (int64, error) {
	e.roleWriteMu.Lock()
	defer e.roleWriteMu.Unlock()
	if err := e.checkImplementerChange(ctx, config.RoleBinding{}, binding); err != nil {
		return 0, err
	}
	id, err := e.queries.AddImplementerBinding(ctx, store.AddImplementerBindingParams{
		Harness: string(binding.Harness), Model: binding.Model, Effort: binding.Effort, Context: int64(binding.Context), Max: int64(binding.Max), UpdatedAt: now(),
	})
	if err != nil {
		return 0, err
	}
	e.rolesMu.Lock()
	defer e.rolesMu.Unlock()
	e.moreBindings = append(slices.Clone(e.moreBindings), ImplementerBinding{ID: id, Binding: binding})
	return id, nil
}

// SetImplementerBinding changes the binding with the id id, which is not the first binding. It runs the refusals of
// SetRole, and gives ErrNoImplementerBinding for an unknown id.
func (e *Engine) SetImplementerBinding(ctx context.Context, id int64, binding config.RoleBinding) error {
	e.roleWriteMu.Lock()
	defer e.roleWriteMu.Unlock()
	more := e.implementerBindings()
	index := slices.IndexFunc(more, func(b ImplementerBinding) bool { return b.ID == id })
	if index < 0 {
		return ErrNoImplementerBinding
	}
	if err := e.checkImplementerChange(ctx, more[index].Binding, binding); err != nil {
		return err
	}
	err := e.queries.UpdateImplementerBinding(ctx, store.UpdateImplementerBindingParams{
		ID: id, Harness: string(binding.Harness), Model: binding.Model, Effort: binding.Effort, Context: int64(binding.Context), Max: int64(binding.Max), UpdatedAt: now(),
	})
	if err != nil {
		return err
	}
	changed := slices.Clone(more)
	changed[index].Binding = binding
	e.rolesMu.Lock()
	defer e.rolesMu.Unlock()
	e.moreBindings = changed
	return nil
}

// RemoveImplementerBinding removes the binding with the id id. The id 0 is the first binding: the second binding takes
// its place in the role_settings row of the Implementer, with the refusals of SetRole. It refuses the removal of the
// first binding when it is the only binding, and gives ErrNoImplementerBinding for an unknown id.
func (e *Engine) RemoveImplementerBinding(ctx context.Context, id int64) error {
	e.roleWriteMu.Lock()
	defer e.roleWriteMu.Unlock()
	more := e.implementerBindings()
	if id == 0 {
		return e.removeFirstImplementerBinding(ctx, more)
	}
	index := slices.IndexFunc(more, func(b ImplementerBinding) bool { return b.ID == id })
	if index < 0 {
		return ErrNoImplementerBinding
	}
	if err := e.queries.DeleteImplementerBinding(ctx, id); err != nil {
		return err
	}
	e.rolesMu.Lock()
	defer e.rolesMu.Unlock()
	e.moreBindings = slices.Delete(slices.Clone(more), index, index+1)
	return nil
}

func (e *Engine) removeFirstImplementerBinding(ctx context.Context, more []ImplementerBinding) error {
	if len(more) == 0 {
		return refuse("The Implementer has only one binding. Add another binding before you remove it.")
	}
	merged := e.mergedRoles()
	second := more[0]
	if err := e.checkImplementerChange(ctx, merged.Implementer, second.Binding); err != nil {
		return err
	}
	merged.Implementer = second.Binding
	e.rolesMu.Lock()
	defer e.rolesMu.Unlock()
	err := e.inTx(ctx, func(q *store.Queries) error {
		if err := e.storeRole(ctx, q, "implementer", second.Binding); err != nil {
			return err
		}
		return q.DeleteImplementerBinding(ctx, second.ID)
	})
	if err != nil {
		return err
	}
	e.moreBindings = slices.Clone(more[1:])
	e.applyRoles(merged)
	return nil
}

func (e *Engine) implementerBindings() []ImplementerBinding {
	e.rolesMu.RLock()
	defer e.rolesMu.RUnlock()
	return e.moreBindings
}
