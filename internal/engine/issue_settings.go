package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// BindingText gives the Harness, the model and the effort as "claude-code · opus · high". An empty effort is not in
// the text.
func BindingText(harness, model, effort string) string {
	text := harness + " · " + model
	if effort != "" {
		text += " · " + effort
	}
	return text
}

// settingSubject names the session that the setting of spec decides, as the Owner reads it in the chat.
func settingSubject(spec Spec) string {
	switch {
	case spec.Issue.Valid:
		return "Implementer of " + eventRef(spec.WorkstreamRepository, spec.Repository, spec.Issue.Int64)
	case spec.Role == LeadRole:
		return "Lead"
	}
	return "Implementer of the Workstream"
}

// CheckOpenWorkstream refuses an issue that is not an open Workstream in the local copy.
func (e *Engine) CheckOpenWorkstream(ctx context.Context, repositoryName string, number int64) error {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return err
	}
	_, err = e.queries.GetCopiedWorkstream(ctx, store.GetCopiedWorkstreamParams{Repository: repository.FullName, Number: number})
	if errors.Is(err, sql.ErrNoRows) {
		return refuse("The issue is not an open Workstream.")
	}
	return err
}

// SetSetting sets the setting of role for the task issue or the Workstream issue of repositoryName, as
// SetIssueBinding does. Then it shows the change on the Workstream page and adds a record to the Lead chat.
func (e *Engine) SetSetting(ctx context.Context, role, repositoryName string, issue int64, harness config.Harness, model, effort string) error {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return err
	}
	spec, err := e.issueSpec(ctx, role, repository.FullName, issue)
	if err != nil {
		return err
	}
	if err := e.SetIssueBinding(ctx, role, repository.FullName, issue, harness, model, effort); err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	e.addRecord(ctx, leadChat(spec.WorkstreamRepository, spec.Workstream), fmt.Sprintf("The Owner set the %s to %s.", settingSubject(spec), BindingText(string(harness), model, effort)))
	return nil
}

// ClearSetting removes the setting of role for the task issue or the Workstream issue of repositoryName, as
// ClearIssueBinding does. When the issue has no setting, it changes nothing.
func (e *Engine) ClearSetting(ctx context.Context, role, repositoryName string, issue int64) error {
	repository, err := e.repository(repositoryName)
	if err != nil {
		return err
	}
	spec, err := e.issueSpec(ctx, role, repository.FullName, issue)
	if err != nil {
		return err
	}
	_, found, err := e.IssueBinding(ctx, role, repository.FullName, issue)
	if err != nil || !found {
		return err
	}
	if err := e.ClearIssueBinding(ctx, role, repository.FullName, issue); err != nil {
		return err
	}
	e.publish(Change{Workstreams: true})
	e.addRecord(ctx, leadChat(spec.WorkstreamRepository, spec.Workstream), fmt.Sprintf("The Owner cleared the setting of the %s.", settingSubject(spec)))
	return nil
}

// NextSetting is the binding of the next session of a Role in a Workstream.
type NextSetting struct {
	Harness string
	Model   string
	Effort  string
	// Own is true when the Workstream has its own setting, and false when the binding comes from the Role.
	Own bool
}

func (e *Engine) nextSetting(ctx context.Context, role, repository string, workstream int64) (NextSetting, error) {
	binding, level, err := e.NextBinding(ctx, role, repository, workstream)
	if err != nil {
		return NextSetting{}, err
	}
	return NextSetting{Harness: string(binding.Harness), Model: binding.Model, Effort: binding.Effort, Own: level == LevelWorkstream}, nil
}
