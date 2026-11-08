package engine

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/coder/acp-go-sdk"

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

// incompletePrompt is the start of the prompt that follows a turn with an incomplete stop reason. It has the stop
// reason as its argument.
const incompletePrompt = "Your previous turn ended with the stop reason `%s` before your work was complete. Read the state of your work and continue. The prompt of that turn follows."

// hangTimeout is the time with no activity after which a running turn is hung.
var hangTimeout = 15 * time.Minute

// errHung is the error of a prompt that hung after the last retry.
var errHung = errors.New("the agent had no activity after the last retry")

// errIncomplete is the error of a prompt that the Harness ended with the stop reason max_tokens, max_turn_requests or
// refusal. The agent did not complete its work.
type errIncomplete struct {
	reason acp.StopReason
}

func (e *errIncomplete) Error() string {
	return fmt.Sprintf("the turn ended with the stop reason %s before the work was complete", e.reason)
}

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
	if err == nil && slices.Contains([]acp.StopReason{acp.StopReasonMaxTokens, acp.StopReasonMaxTurnRequests, acp.StopReasonRefusal}, result.StopReason) {
		return &errIncomplete{reason: result.StopReason}
	}
	return err
}

// watch holds until ctx ends or the turn has no activity for hangTimeout. In the second case, watch first asks the
// agent for its state with check, or waits for an open tool call (openCallWait), and waits for the activity of the
// agent. Otherwise it sends the cancel, calls stop, and gives true.
func (a *Agent) watch(ctx context.Context, stop context.CancelFunc) bool {
	timer := time.NewTimer(hangTimeout)
	defer timer.Stop()
	noted := false
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
		}
		a.mu.Lock()
		idle := time.Since(a.activity)
		a.mu.Unlock()
		if idle < hangTimeout {
			noted = false
			timer.Reset(hangTimeout - idle)
			continue
		}
		if a.session.Steering() {
			if a.check(ctx) {
				timer.Reset(hangTimeout)
				continue
			}
			if ctx.Err() != nil {
				return false
			}
		} else if wait := a.openCallWait(idle); wait > 0 {
			if !noted {
				noted = true
				if err := a.addNote(ctx, fmt.Sprintf("The agent had no activity for %s and a tool call is open. Mobius sends no cancel until %s after the last activity.", hangTimeout, backgroundTimeout)); err != nil {
					log.Printf("add the note of the open tool call of the session %d: %v", a.id, err)
				}
			}
			timer.Reset(wait)
			continue
		}
		_ = a.cancel(ctx)
		stop()
		return true
	}
}

// openCallWait gives the time that watch waits before it reads the state again, or 0 when watch sends the cancel. A
// cancel can end a command that still runs, so a turn with an open tool call that is not a retry gets no cancel until
// backgroundTimeout after its last activity. idle is the time with no activity.
func (a *Agent) openCallWait(idle time.Duration) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.retries > 0 || len(a.openCalls) == 0 || idle >= backgroundTimeout {
		return 0
	}
	return min(hangTimeout, backgroundTimeout-idle)
}

// check asks the agent for its state with a steering request, and gives true when the Harness put the request into
// the running turn. A check goes out only in a turn that is no retry, and only when the agent had activity after the
// last check. A check goes out for at most backgroundTimeout after the first check of the current hour. Work of
// hangTimeout or more after the last check starts a new hour.
func (a *Agent) check(ctx context.Context) bool {
	a.mu.Lock()
	if a.retries > 0 || (!a.lastCheck.IsZero() && !a.activity.After(a.lastCheck)) {
		a.mu.Unlock()
		return false
	}
	if a.lastCheck.IsZero() || a.activity.Sub(a.lastCheck) >= hangTimeout {
		a.firstCheck = time.Now()
	}
	if time.Since(a.firstCheck) >= backgroundTimeout {
		a.mu.Unlock()
		return false
	}
	a.lastCheck = time.Now()
	a.mu.Unlock()
	text := retryText(a.spec.Role)
	if err := a.addCheckRows(ctx, text); err != nil {
		log.Printf("add the check rows of the session %d: %v", a.id, err)
		return false
	}
	injected, err := a.session.Steer(ctx, text)
	if err != nil && ctx.Err() == nil {
		log.Printf("steer the session %d: %v", a.id, err)
	}
	return injected
}

// addCheckRows adds the note of a check and the prompt row with its text to the Transcript. The prompt row has the
// field check, so the list of retry prompts of the Curator does not show it.
func (a *Agent) addCheckRows(ctx context.Context, text string) error {
	if err := a.addNote(ctx, fmt.Sprintf("The agent had no activity for %s. Mobius asks the agent for its state and does not stop the turn.", hangTimeout)); err != nil {
		return err
	}
	row, err := compact(map[string]any{"text": text, "check": true})
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.chunk = nil
	return a.engine.addRow(ctx, a.id, "prompt", row, false)
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

// hungText tells the Owner that Mobius stopped the session after its hangs.
func (a *Agent) hungText() string {
	spec := a.spec
	text := fmt.Sprintf("Mobius stopped the %s session %d. It had no activity after %d retries.", spec.Role, a.id, maxRetries)
	if spec.Issue.Valid {
		text += fmt.Sprintf(" The session worked on #%d.", spec.Issue.Int64)
	}
	return text
}

// endHung ends the session after the hang that follows the last retry, and adds an activity on the issue of the
// session, or on the Workstream issue when the session has no issue. A session with no Workstream has no issue and no link.
func (a *Agent) endHung(ctx context.Context) error {
	spec := a.spec
	issue := spec.Issue.Int64
	if !spec.Issue.Valid {
		issue = spec.Workstream
	}
	err := a.engine.queries.AddEvent(ctx, store.AddEventParams{
		Time:         now(),
		Organization: spec.Organization,
		Repository:   spec.Repository,
		Workstream:   spec.Workstream,
		Issue:        issue,
		Actor:        mobiusActor,
		Text:         a.hungText(),
		Link:         issueLink(spec.Repository, issue),
	})
	return errors.Join(err, a.End(ctx, "hung"))
}

// endHungTask ends the session of the task after the hang that follows the last retry. It hands the task to a human
// with a stop event for the Lead, a comment and an Inbox item.
func (e *Engine) endHungTask(ctx context.Context, a *Agent, task store.Task, title string) error {
	spec := a.spec
	reason := fmt.Sprintf("the %s session %d had no activity after %d retries. Mobius stopped the session and added mobius:needs-human.", spec.Role, a.id, maxRetries)
	return errors.Join(a.End(ctx, "hung"), e.handOverFailed(ctx, task, title, reason, "Session hung", "The agent session had no activity after the last retry. Mobius stopped the session.", stopNotice{
		reason: fmt.Sprintf("The %s session hung", spec.Role),
		text:   a.hungText(),
		next:   "Resume to start a new session.",
	}))
}
