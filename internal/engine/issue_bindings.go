package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// The levels of a session binding, from the lowest to the highest.
const (
	LevelTask       = "task"
	LevelWorkstream = "workstream"
	LevelRole       = "role"
)

// issueBindingRole gives the name of the Role in the issue_bindings table for the session Role role, or "" when the
// sessions of role have no setting for an issue.
func issueBindingRole(role string) string {
	switch role {
	case ImplementerRole:
		return "implementer"
	case LeadRole:
		return "lead"
	}
	return ""
}

// sessionBinding gives the binding of the session of spec, and its level. The first setting of the task, the Workstream
// or the Role wins. A setting gives the Harness, the model and the effort over the Role binding in roles. The caller
// can hold the lock of rolesMu, so sessionBinding does not read e.roles.
func (e *Engine) sessionBinding(ctx context.Context, roles *config.Roles, spec Spec) (config.RoleBinding, string, error) {
	binding, ok := roleBinding(roles, spec.Role)
	if !ok {
		return config.RoleBinding{}, "", fmt.Errorf("the Role %s has no Role binding", spec.Role)
	}
	role := issueBindingRole(spec.Role)
	if role == "" {
		return binding, LevelRole, nil
	}
	type place struct {
		repository string
		issue      int64
		level      string
	}
	var places []place
	if spec.Role == ImplementerRole && spec.Issue.Valid {
		places = append(places, place{spec.Repository, spec.Issue.Int64, LevelTask})
	}
	places = append(places, place{spec.WorkstreamRepository, spec.Workstream, LevelWorkstream})
	for _, p := range places {
		row, err := e.queries.GetIssueBinding(ctx, store.GetIssueBindingParams{Repository: p.repository, Issue: p.issue, Role: role})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return config.RoleBinding{}, "", err
		}
		return withSetting(binding, config.Harness(row.Harness), row.Model, row.Effort), p.level, nil
	}
	return binding, LevelRole, nil
}

// withSetting gives binding with the Harness, the model and the effort of a setting. The Context of the Role stays when
// the Harness has a context limit.
func withSetting(binding config.RoleBinding, harness config.Harness, model, effort string) config.RoleBinding {
	binding.Harness, binding.Model, binding.Effort = harness, model, effort
	if !harness.HasContextLimit() {
		binding.Context = 0
	}
	return binding
}

// issueSpec gives the spec of the session of role that decides the binding for the issue of repository. The issue is a
// Workstream issue or a task issue in the local copy.
func (e *Engine) issueSpec(ctx context.Context, role, repository string, issue int64) (Spec, error) {
	var sessionRole string
	switch role {
	case "implementer":
		sessionRole = ImplementerRole
	case "lead":
		sessionRole = LeadRole
	default:
		return Spec{}, refuse("The Role %s has no setting for an issue.", role)
	}
	workstream, err := e.queries.HasCopiedWorkstreamAnyCase(ctx, store.HasCopiedWorkstreamAnyCaseParams{Repository: repository, Number: issue})
	if err != nil {
		return Spec{}, err
	}
	if workstream {
		return Spec{Role: sessionRole, Repository: repository, WorkstreamRepository: repository, Workstream: issue}, nil
	}
	if sessionRole == LeadRole {
		return Spec{}, refuse("%s#%d is not a Workstream issue.", repository, issue)
	}
	tree, err := e.queries.GetCopiedIssueTree(ctx, store.GetCopiedIssueTreeParams{Number: issue, Repository: repository})
	if errors.Is(err, sql.ErrNoRows) {
		return Spec{}, refuse("%s#%d is in no Workstream.", repository, issue)
	}
	if err != nil {
		return Spec{}, err
	}
	return Spec{
		Role:                 sessionRole,
		Repository:           repository,
		WorkstreamRepository: tree.Repository,
		Workstream:           tree.Workstream,
		Issue:                sql.NullInt64{Int64: issue, Valid: true},
	}, nil
}

// IssueBinding gives the setting of role for the task issue or the Workstream issue, and tells if it exists.
// role is implementer or lead.
func (e *Engine) IssueBinding(ctx context.Context, role, repository string, issue int64) (store.IssueBinding, bool, error) {
	row, err := e.queries.GetIssueBinding(ctx, store.GetIssueBindingParams{Repository: repository, Issue: issue, Role: role})
	if errors.Is(err, sql.ErrNoRows) {
		return store.IssueBinding{}, false, nil
	}
	return row, err == nil, err
}

// NextBinding gives the binding that the next session of role gets for the task issue or the Workstream issue, and its
// level. For a Workstream issue, the Implementer binding is the one of a task with no setting of its own.
func (e *Engine) NextBinding(ctx context.Context, role, repository string, issue int64) (config.RoleBinding, string, error) {
	spec, err := e.issueSpec(ctx, role, repository, issue)
	if err != nil {
		return config.RoleBinding{}, "", err
	}
	roles := e.mergedRoles()
	return e.sessionBinding(ctx, &roles, spec)
}

// SetIssueBinding sets the Harness, the model and the effort of role for the task issue or the Workstream issue. role
// is implementer or lead, and only a Workstream issue has a lead setting. The next session uses it, and a session that
// runs keeps its binding. It refuses a binding that the Roles page would refuse, and a binding whose access check
// fails.
func (e *Engine) SetIssueBinding(ctx context.Context, role, repository string, issue int64, harness config.Harness, model, effort string) error {
	e.roleWriteMu.Lock()
	defer e.roleWriteMu.Unlock()
	spec, err := e.issueSpec(ctx, role, repository, issue)
	if err != nil {
		return err
	}
	if err := e.checkIssueBinding(ctx, spec, harness, model, effort); err != nil {
		return err
	}
	return e.putIssueBinding(ctx, role, repository, issue, harness, model, effort)
}

// checkIssueBinding refuses a setting for the session of spec that the Roles page would refuse, and a setting whose
// access check fails. The caller holds roleWriteMu.
func (e *Engine) checkIssueBinding(ctx context.Context, spec Spec, harness config.Harness, model, effort string) error {
	roles := e.mergedRoles()
	current, _, err := e.sessionBinding(ctx, &roles, spec)
	if err != nil {
		return err
	}
	role := issueBindingRole(spec.Role)
	target := namedBinding(&roles, role)
	next := withSetting(*target, harness, model, effort)
	*target = next
	if err := e.checkRoles(&roles, role); err != nil {
		return refuse("%s", err)
	}
	return e.checkChange(ctx, current, next)
}

// putIssueBinding stores the setting with no check.
func (e *Engine) putIssueBinding(ctx context.Context, role, repository string, issue int64, harness config.Harness, model, effort string) error {
	err := e.queries.UpsertIssueBinding(ctx, store.UpsertIssueBindingParams{
		Repository: repository,
		Issue:      issue,
		Role:       role,
		Harness:    string(harness),
		Model:      model,
		Effort:     effort,
		UpdatedAt:  now(),
	})
	if err != nil {
		return err
	}
	e.publish(Change{Pauses: true})
	e.workers.changed.notify()
	return nil
}

// ClearIssueBinding removes the setting of role for the task issue or the Workstream issue. The next session uses the
// next level again.
func (e *Engine) ClearIssueBinding(ctx context.Context, role, repository string, issue int64) error {
	if role != "implementer" && role != "lead" {
		return refuse("The Role %s has no setting for an issue.", role)
	}
	if err := e.queries.DeleteIssueBinding(ctx, store.DeleteIssueBindingParams{Repository: repository, Issue: issue, Role: role}); err != nil {
		return err
	}
	e.publish(Change{Pauses: true})
	e.workers.changed.notify()
	return nil
}
