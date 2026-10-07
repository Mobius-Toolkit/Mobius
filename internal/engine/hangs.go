package engine

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

//go:embed prompts/retry.md
var retryPrompt string

const (
	// maxRetries is the number of retry prompts that a session gets. The next hang stops the session.
	maxRetries = 3
	// retryAdvice is the line of the retry prompt for a Role that has no Mobius tool to tell that it cannot do the work.
	retryAdvice = "If you cannot do the work, say so in your reply and give the reason."
	// implementerRetryAdvice is the line of the retry prompt of the Implementer.
	implementerRetryAdvice = "If you cannot do the work, call `cannot_do` with the reason."
)

// hangTimeout is the time with no activity after which a running turn is hung.
var hangTimeout = 15 * time.Minute

// errHung is the error of a prompt that hung after the last retry.
var errHung = errors.New("the agent had no activity after the last retry")

func retryText(role string) string {
	if role == ImplementerRole {
		return fmt.Sprintf(retryPrompt, implementerRetryAdvice)
	}
	return fmt.Sprintf(retryPrompt, retryAdvice)
}

// sendPrompt sends text and holds until the response of the agent. When the turn has no activity for hangTimeout,
// sendPrompt sends the cancel, stops the wait for the response, and gives errHung.
func (a *Agent) sendPrompt(ctx context.Context, text string) error {
	promptCtx, stop := context.WithCancel(ctx)
	defer stop()
	hung := make(chan bool, 1)
	go func() { hung <- a.watch(promptCtx, stop) }()
	_, err := a.session.Prompt(promptCtx, text)
	stop()
	if <-hung {
		return errHung
	}
	return err
}

// watch holds until ctx ends or the turn has no activity for hangTimeout. In the second case, it sends the cancel,
// calls stop, and gives true.
func (a *Agent) watch(ctx context.Context, stop context.CancelFunc) bool {
	timer := time.NewTimer(hangTimeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
		}
		a.mu.Lock()
		idle := time.Since(a.activity)
		a.mu.Unlock()
		if idle >= hangTimeout {
			_ = a.cancel(ctx)
			stop()
			return true
		}
		timer.Reset(hangTimeout - idle)
	}
}

// touch records activity of the agent now.
func (a *Agent) touch() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.activity = time.Now()
}

// addNote adds a row of Mobius with text to the Transcript.
func (a *Agent) addNote(ctx context.Context, text string) error {
	row, err := compact(map[string]string{"text": text})
	if err != nil {
		return err
	}
	return a.engine.addRow(ctx, a.id, "check", row, false)
}

// endHung ends the session after the hang that follows the last retry, and adds an Inbox item for the Owner.
func (a *Agent) endHung(ctx context.Context) error {
	spec := a.spec
	text := fmt.Sprintf("Mobius stopped the %s session %d. It had no activity after %d retries.", spec.Role, a.id, maxRetries)
	if spec.Issue.Valid {
		text += fmt.Sprintf(" The session worked on #%d.", spec.Issue.Int64)
	}
	_, err := a.engine.addInboxItem(ctx, store.AddInboxItemParams{
		Kind:         stoppedKind,
		Organization: spec.Organization,
		Repository:   spec.Repository,
		Workstream:   spec.Workstream,
		Issue:        spec.Issue.Int64,
		Text:         text,
	})
	return errors.Join(err, a.End(ctx, "hung"))
}

// endHungTask does endHung, and hands the task to a human with a stop event for the Lead.
func (e *Engine) endHungTask(ctx context.Context, a *Agent, task store.Task, title string) error {
	reason := fmt.Sprintf("the %s session %d had no activity after %d retries. Mobius stopped the session and added mobius:needs-human.", a.spec.Role, a.id, maxRetries)
	return errors.Join(a.endHung(ctx), e.handOver(ctx, task, title, reason))
}
