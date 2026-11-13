package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// The kinds of the Inbox items of a missing Workstream.
const (
	missingMaintenanceKind = "missing maintenance"
	missingSkillsKind      = "missing skills"
)

const (
	missingMaintenanceText = "The organization has no Maintenance Workstream. A CI failure and a CI break of the default branch get no issue. A check timeout ends with no issue. The Curator cannot create issues."
	missingSkillsText      = "The organization has no Skills Workstream. The tool propose_skill refuses each skill proposal of the organization."
)

func missingWorkstreamKind(kind string) bool {
	return kind == missingMaintenanceKind || kind == missingSkillsKind
}

// syncMissingWorkstreams gives each organization with an open Workstream one open Inbox item for each of its missing
// Maintenance and Skills Workstreams. It dismisses each open item whose Workstream exists, and adds no item for a kind
// that the Owner dismissed. With onlyChanged, it checks only the organizations whose open Workstreams or their settings
// changed since their last check.
func (e *Engine) syncMissingWorkstreams(ctx context.Context, onlyChanged bool) error {
	e.missingWorkstreamsMu.Lock()
	defer e.missingWorkstreamsMu.Unlock()
	for _, organization := range e.github.Organizations() {
		workstreams, err := e.queries.ListOpenWorkstreamsOfOrganization(ctx, organization)
		if err != nil {
			return err
		}
		checked := fmt.Sprint(workstreams)
		if onlyChanged && e.checkedWorkstreams[organization] == checked {
			continue
		}
		if err := e.syncMissingWorkstreamsOf(ctx, organization, len(workstreams) > 0); err != nil {
			return err
		}
		e.checkedWorkstreams[organization] = checked
	}
	return nil
}

func (e *Engine) syncMissingWorkstreamsOf(ctx context.Context, organization string, hasWorkstreams bool) error {
	missing := map[string]bool{}
	if hasWorkstreams {
		maintenance, err := e.organizationMaintenance(ctx, organization)
		if err != nil && !Refused(err) {
			return err
		}
		missing[missingMaintenanceKind] = err != nil || maintenance.number == 0
		_, err = e.queries.GetSkillsWorkstream(ctx, organization)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		missing[missingSkillsKind] = err != nil
	}
	items, err := e.Inbox(ctx)
	if err != nil {
		return err
	}
	covered := map[string]bool{}
	for _, item := range items {
		if item.Organization != organization || !missingWorkstreamKind(item.Kind) {
			continue
		}
		if missing[item.Kind] {
			covered[item.Kind] = true
		} else if err := e.Dismiss(ctx, item.ID); err != nil {
			return err
		}
	}
	dismissals, err := e.queries.ListMissingWorkstreamDismissals(ctx)
	if err != nil {
		return err
	}
	for _, dismissal := range dismissals {
		if dismissal.Organization == organization {
			covered[dismissal.Kind] = true
		}
	}
	add := func(kind, title, text string) error {
		if !missing[kind] || covered[kind] {
			return nil
		}
		return e.addInboxItem(ctx, store.AddInboxItemParams{Kind: kind, Organization: organization, Title: title, Text: text})
	}
	if err := add(missingMaintenanceKind, "Maintenance Workstream is missing", missingMaintenanceText); err != nil {
		return err
	}
	return add(missingSkillsKind, "Skills Workstream is missing", missingSkillsText)
}
