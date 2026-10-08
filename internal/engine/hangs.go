package engine

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
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

// retryHang counts a hang and gives the retry prompt. After the last retry, it adds a note and gives errHung.
func (a *Agent) retryHang(ctx context.Context) (string, error) {
	if a.retries >= maxRetries {
		return "", errors.Join(errHung, a.addNote(ctx, fmt.Sprintf("The agent had no activity for %s after %d retries. Mobius stops the session.", hangTimeout, maxRetries)))
	}
	a.retries++
	if err := a.addNote(ctx, fmt.Sprintf("The agent had no activity for %s. Mobius stopped the turn and sends retry %d of %d.", hangTimeout, a.retries, maxRetries)); err != nil {
		return "", err
	}
	return retryText(a.spec.Role), nil
}

// sendPrompt sends text and images and holds until the response of the agent. When the turn has no activity for hangTimeout,
// sendPrompt sends the cancel, stops the wait for the response, and gives errHung. When the Harness absorbed the
// prompt into an autonomous turn, the cancel ends the prompt, and sendPrompt adds a note and gives nil: the
// autonomous turn did the work.
func (a *Agent) sendPrompt(ctx context.Context, text string, images []Image) error {
	promptCtx, stop := context.WithCancel(ctx)
	defer stop()
	hung := make(chan bool, 1)
	absorbed := make(chan bool, 1)
	go func() { hung <- a.watch(promptCtx, stop) }()
	go func() { absorbed <- a.watchAbsorbed(promptCtx) }()
	result, err := a.session.Prompt(promptCtx, text, images)
	if usageErr := a.addUsage(context.WithoutCancel(ctx), result, err); usageErr != nil {
		log.Printf("add the usage of the session %d: %v", a.id, usageErr)
	}
	stop()
	if <-hung {
		<-absorbed
		return errHung
	}
	if <-absorbed {
		return a.addNote(ctx, fmt.Sprintf("The agent ended an autonomous turn and had no more work for %s, so the prompt had no response. Mobius cancelled the prompt and counts the turn as complete.", absorbTimeout))
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
