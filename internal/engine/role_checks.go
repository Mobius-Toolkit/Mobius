package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// accessRetry is the time after which the engine checks a failed key again.
const accessRetry = time.Hour

const accessDocs = "https://github.com/Mobius-Toolkit/Mobius/blob/main/docs/install.md#3-install-and-log-in-to-the-harnesses"

// accessKey is the access that a Role binding needs. Roles with the same key share one check.
type accessKey struct {
	harness       config.Harness
	model, effort string
}

func keyOf(binding config.RoleBinding) accessKey {
	return accessKey{binding.Harness, binding.Model, binding.Effort}
}

func (k accessKey) title() string {
	title := fmt.Sprintf("Access check failed: %s %s", k.harness, k.model)
	if k.effort != "" {
		title += " " + k.effort
	}
	return title
}

// accessState is the result of the access check of a key. A key with no state is in the check.
type accessState struct {
	passed bool
	// reason is the error text of the Harness when the check failed.
	reason string
	at     time.Time
}

// accessHold gives the queue reason of a session with binding, or "" when the access check of its key passed.
func (e *Engine) accessHold(binding config.RoleBinding) string {
	key := keyOf(binding)
	e.accessMu.Lock()
	state, checked := e.access[key]
	e.accessMu.Unlock()
	switch {
	case !checked:
		return fmt.Sprintf("waits for the access check of %s %s", key.harness, key.model)
	case !state.passed:
		return "access check failed: " + state.reason
	}
	return ""
}

func (e *Engine) setAccess(key accessKey, err error) {
	state := accessState{passed: err == nil, at: e.timeNow()}
	if err != nil {
		state.reason = err.Error()
	}
	e.accessMu.Lock()
	e.access[key] = state
	e.accessMu.Unlock()
	e.workers.changed.notify()
}

// accessDue tells if key has no check, or failed an hour ago or earlier.
func (e *Engine) accessDue(key accessKey) bool {
	e.accessMu.Lock()
	defer e.accessMu.Unlock()
	state, checked := e.access[key]
	return !checked || !state.passed && e.timeNow().Sub(state.at) >= accessRetry
}

// runAccess sends the access prompt to the Harness of key in a new directory.
func (e *Engine) runAccess(ctx context.Context, key accessKey) error {
	dir, err := os.MkdirTemp("", "mobius-access-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	return runner.CheckAccess(ctx, key.harness, key.model, key.effort, dir, dir, runner.AgentPath(e.config.DataDir, e.agents.Path))
}

// checkAccess checks the key of each Role binding that has no check or that failed an hour ago, one key at a time. Then
// it adds an Inbox item for each failed key, and dismisses the item of each key that passed or that no Role uses.
func (e *Engine) checkAccess(ctx context.Context) {
	roles := e.mergedRoles()
	for _, named := range roles.Bindings() {
		key := keyOf(*named.Binding)
		if !e.accessDue(key) {
			continue
		}
		err := e.runAccess(ctx, key)
		if ctx.Err() != nil {
			return
		}
		e.setAccess(key, err)
	}
	if err := e.syncAccessItems(ctx, &roles); err != nil {
		log.Printf("access check items: %v", err)
	}
}

// checkChange runs the access check of next when its key differs from the key of current. It refuses a failed check.
func (e *Engine) checkChange(ctx context.Context, current, next config.RoleBinding) error {
	key := keyOf(next)
	if key == keyOf(current) {
		return nil
	}
	if err := e.runAccess(ctx, key); err != nil {
		return refuse("access check failed: %s", err)
	}
	e.setAccess(key, nil)
	return nil
}

// syncAccessItems gives each failed key of roles one open Inbox item in each organization, and dismisses each open item
// whose key passed or is not in roles.
func (e *Engine) syncAccessItems(ctx context.Context, roles *config.Roles) error {
	items, err := e.Inbox(ctx)
	if err != nil {
		return err
	}
	uses := map[accessKey][]string{}
	for _, named := range roles.Bindings() {
		key := keyOf(*named.Binding)
		uses[key] = append(uses[key], named.Role)
	}
	e.accessMu.Lock()
	states := make(map[accessKey]accessState, len(uses))
	for key := range uses {
		if state, checked := e.access[key]; checked {
			states[key] = state
		}
	}
	e.accessMu.Unlock()
	titles := map[string]accessKey{}
	for key := range uses {
		titles[key.title()] = key
	}
	open := map[[2]string]bool{}
	for _, item := range items {
		if item.Kind != accessCheckKind {
			continue
		}
		key, used := titles[item.Title]
		if state, checked := states[key]; !used || checked && state.passed {
			if err := e.Dismiss(ctx, item.ID); err != nil {
				return err
			}
			continue
		}
		open[[2]string{item.Organization, item.Title}] = true
	}
	var failed []accessKey
	for key, state := range states {
		if !state.passed {
			failed = append(failed, key)
		}
	}
	slices.SortFunc(failed, func(a, b accessKey) int { return strings.Compare(a.title(), b.title()) })
	for _, key := range failed {
		for _, organization := range e.github.Organizations() {
			if open[[2]string{organization, key.title()}] {
				continue
			}
			err := e.addInboxItem(ctx, store.AddInboxItemParams{
				Kind:         accessCheckKind,
				Organization: organization,
				Title:        key.title(),
				Reason:       states[key].reason,
				Text:         accessText(key, uses[key], states[key].reason),
				Link:         accessDocs,
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func accessText(key accessKey, roles []string, reason string) string {
	effort := "no effort"
	if key.effort != "" {
		effort = "the effort " + key.effort
	}
	return fmt.Sprintf("The Harness %s refused the access check for the model %s and %s. These roles use it: %s. Mobius holds the new sessions of these roles. It checks again each hour.\n\n%s",
		key.harness, key.model, effort, strings.Join(roles, ", "), reason)
}
