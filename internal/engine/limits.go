package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/coder/acp-go-sdk"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const (
	// pausedPrefix starts the queue reason of a session that waits for the end of a pause of its Harness.
	pausedPrefix = "paused until "
	// noTimeWait is the pause after a usage limit with no reset time.
	noTimeWait = 30 * time.Minute
	// resetTolerance is how long after a reset time in the text of a usage limit the reset time still counts as now.
	resetTolerance = 5 * time.Minute
	// usageLimitKind is the kind of the Inbox item of a pause.
	usageLimitKind = "usage limit"
)

// limitData holds the fields of the data of an ACP error that tell a usage limit.
type limitData struct {
	ErrorKind         string `json:"errorKind"`
	RetryAfterSeconds *int64 `json:"retryAfterSeconds"`
}

// detect gives the end of the pause when err of a prompt of harness is a usage limit. hint is the last reset time
// of the usage updates of the session, or zero.
func detect(harness config.Harness, err *acp.RequestError, hint, now time.Time) (time.Time, bool) {
	text := err.Message
	var data limitData
	if err.Data != nil {
		raw, marshalErr := json.Marshal(err.Data)
		if marshalErr != nil {
			return time.Time{}, false
		}
		text += " " + string(raw)
		// Data that is not a JSON object has no fields of a usage limit.
		_ = json.Unmarshal(raw, &data)
	}
	var reset time.Time
	switch harness {
	case config.ClaudeCode:
		if data.ErrorKind != "rate_limit" {
			return time.Time{}, false
		}
		reset = hint
		if reset.IsZero() {
			reset = resetsAt(text, now)
		}
	case config.Antigravity:
		index := strings.Index(text, "Usage Limit Reached")
		if index < 0 {
			return time.Time{}, false
		}
		reset = resetIn(text[index:], now)
	case config.Devin:
		if err.Code != -32011 {
			return time.Time{}, false
		}
		if data.RetryAfterSeconds != nil {
			reset = now.Add(time.Duration(*data.RetryAfterSeconds) * time.Second)
		}
	}
	if reset.IsZero() {
		return now.Add(noTimeWait), true
	}
	return reset, true
}

// resetsAt reads "resets 3pm" or "resets 17:30" in text, and gives the next such time in UTC, or zero. A time that has just passed gives zero too.
func resetsAt(text string, now time.Time) time.Time {
	_, rest, ok := strings.Cut(text, "resets ")
	if !ok {
		return time.Time{}
	}
	if end := strings.IndexFunc(rest, func(r rune) bool {
		return r > unicode.MaxASCII || !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != ':'
	}); end >= 0 {
		rest = rest[:end]
	}
	clock := strings.ToLower(rest)
	pm, am := false, false
	if before, ok := strings.CutSuffix(clock, "pm"); ok {
		clock, pm = before, true
	} else if before, ok := strings.CutSuffix(clock, "am"); ok {
		clock, am = before, true
	}
	hourText, minuteText, hasMinute := strings.Cut(clock, ":")
	hour, err := strconv.ParseUint(hourText, 10, 8)
	if err != nil {
		return time.Time{}
	}
	minute := uint64(0)
	if hasMinute {
		if minute, err = strconv.ParseUint(minuteText, 10, 8); err != nil {
			return time.Time{}
		}
	}
	switch {
	case pm && hour < 12:
		hour += 12
	case am && hour == 12:
		hour = 0
	}
	if hour > 23 || minute > 59 {
		return time.Time{}
	}
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), int(hour), int(minute), 0, 0, time.UTC)
	if today.After(now) {
		return today
	}
	if now.Sub(today) <= resetTolerance {
		return time.Time{}
	}
	return today.AddDate(0, 0, 1)
}

// resetIn reads "reset in 2 days, 3 hours" in text, and gives now plus that time, or zero.
func resetIn(text string, now time.Time) time.Time {
	_, rest, ok := strings.Cut(text, "reset in ")
	if !ok {
		return time.Time{}
	}
	words := strings.FieldsFunc(rest, func(r rune) bool { return unicode.IsSpace(r) || r == ',' })
	units := map[string]time.Duration{
		"day": 24 * time.Hour, "days": 24 * time.Hour,
		"hour": time.Hour, "hours": time.Hour,
		"minute": time.Minute, "minutes": time.Minute,
	}
	var wait time.Duration
	for i := 0; i+1 < len(words); i++ {
		count, err := strconv.ParseInt(words[i], 10, 64)
		if err != nil {
			continue
		}
		wait += time.Duration(count) * units[strings.TrimRightFunc(words[i+1], func(r rune) bool { return !unicode.IsLetter(r) })]
	}
	if wait <= 0 {
		return time.Time{}
	}
	return now.Add(wait)
}

// waitOutLimit gives true when err of a prompt is a usage limit of the Harness of a. Then it pauses the Harness,
// and holds until the pause ends, so the caller sends the same prompt again.
func (a *Agent) waitOutLimit(ctx context.Context, err error) (bool, error) {
	var requestErr *acp.RequestError
	if !errors.As(err, &requestErr) {
		return false, nil
	}
	now := time.Now().UTC()
	a.mu.Lock()
	hint := a.resetHint
	a.mu.Unlock()
	// A reset time that is not later than now is an old hint.
	if !hint.After(now) {
		hint = time.Time{}
	}
	until, ok := detect(a.harness, requestErr, hint, now)
	if !ok {
		return false, nil
	}
	if err := a.engine.pause(ctx, a, until); err != nil {
		return false, err
	}
	return true, a.waitForPause(ctx)
}

// pause pauses the Harness of a until the time until, with an Inbox item and a message in the chat of the
// Workstream of a. A second session at the same pause adds no second Inbox item.
func (e *Engine) pause(ctx context.Context, a *Agent, until time.Time) error {
	e.pausing.Lock()
	defer e.pausing.Unlock()
	_, err := e.queries.GetHarnessPause(ctx, string(a.harness))
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	text := fmt.Sprintf("%s reached a usage limit. Mobius sends the prompt again at %s.", a.harness, until.UTC().Format(timeFormat))
	spec := a.spec
	item, err := e.addInboxItem(ctx, store.AddInboxItemParams{
		Kind:         usageLimitKind,
		Organization: spec.Organization,
		Repository:   spec.Repository,
		Workstream:   spec.Workstream,
		Issue:        spec.Workstream,
		Text:         text,
	})
	if err != nil {
		return err
	}
	pause := store.HarnessPause{Harness: string(a.harness), PausedUntil: until.UTC().Format(time.RFC3339Nano), InboxItem: item.ID}
	if err := e.queries.SetHarnessPause(ctx, store.SetHarnessPauseParams(pause)); err != nil {
		return err
	}
	if _, err := e.addChatMessage(ctx, ChatKey{spec.Organization, spec.Repository, spec.Workstream}, mobiusAuthor, text); err != nil {
		return err
	}
	return e.timer(pause)
}

// timer ends the pause at its time. A later pause of the same Harness has its own timer.
func (e *Engine) timer(pause store.HarnessPause) error {
	until, err := time.Parse(time.RFC3339Nano, pause.PausedUntil)
	if err != nil {
		return err
	}
	time.AfterFunc(time.Until(until), func() {
		ctx := context.Background()
		current, err := e.queries.GetHarnessPause(ctx, pause.Harness)
		if errors.Is(err, sql.ErrNoRows) || err == nil && current.PausedUntil != pause.PausedUntil {
			return
		}
		if err == nil {
			err = e.endPause(ctx, current)
		}
		if err != nil {
			log.Printf("end the pause of %s: %v", pause.Harness, err)
		}
	})
	return nil
}

// Resume ends the pause of the usage-limit Inbox item now. An item with no pause is no error.
func (e *Engine) Resume(ctx context.Context, inboxItem int64) error {
	pauses, err := e.queries.ListHarnessPauses(ctx)
	if err != nil {
		return err
	}
	for _, pause := range pauses {
		if pause.InboxItem == inboxItem {
			return e.endPause(ctx, pause)
		}
	}
	return nil
}

// harnessPause gives the pause of harness, or nil when harness has no pause.
func (e *Engine) harnessPause(ctx context.Context, harness config.Harness) (*store.HarnessPause, error) {
	pause, err := e.queries.GetHarnessPause(ctx, string(harness))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pause, nil
}

// endPauseSince ends the pause of a Harness after a prompt succeeded. before is the pause of the Harness when the
// prompt started. A pause that is new or different started while the prompt ran, so it stays.
func (e *Engine) endPauseSince(ctx context.Context, before *store.HarnessPause) error {
	if before == nil {
		return nil
	}
	current, err := e.harnessPause(ctx, config.Harness(before.Harness))
	if err != nil {
		return err
	}
	if current == nil || *current != *before {
		return nil
	}
	return e.endPause(ctx, *current)
}

func (e *Engine) endPause(ctx context.Context, pause store.HarnessPause) error {
	if err := e.queries.DeleteHarnessPause(ctx, pause.Harness); err != nil {
		return err
	}
	if err := e.Dismiss(ctx, pause.InboxItem); err != nil {
		return err
	}
	e.pausesChanged.notify()
	e.workers.changed.notify()
	return nil
}

func pausedReason(pause store.HarnessPause) (string, error) {
	until, err := time.Parse(time.RFC3339Nano, pause.PausedUntil)
	if err != nil {
		return "", err
	}
	return pausedPrefix + until.UTC().Format(timeFormat), nil
}

// waitForPause holds until the Harness of a has no pause. The session shows the pause in its queue reason while it
// waits. While the drain is on, the drain does not count the session. After the end of the pause, the session counts
// again before it goes on. After seal, it does not go on: it waits until the restart ends it.
func (a *Agent) waitForPause(ctx context.Context) error {
	e := a.engine
	shown := false
	for {
		changed := e.pausesChanged.wait()
		pause, err := e.queries.GetHarnessPause(ctx, string(a.harness))
		if errors.Is(err, sql.ErrNoRows) {
			if !a.setUncounted(false) {
				<-ctx.Done()
				return ctx.Err()
			}
			if !shown {
				return nil
			}
			cleared, err := e.queries.ClearQueueReason(ctx, a.id)
			if err != nil {
				return err
			}
			e.publish(Change{Node: new(node(cleared))})
			return nil
		}
		if err != nil {
			return err
		}
		if !shown {
			reason, err := pausedReason(pause)
			if err != nil {
				return err
			}
			queued, err := e.queries.SetQueueReason(ctx, store.SetQueueReasonParams{QueueReason: sql.NullString{String: reason, Valid: true}, ID: a.id})
			if err != nil {
				return err
			}
			e.publish(Change{Node: new(node(queued))})
			shown = true
		}
		a.setUncounted(e.draining())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}
