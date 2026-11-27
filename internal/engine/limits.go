package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/coder/acp-go-sdk"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const (
	// pausedPrefix starts the queue reason of a session that waits for the end of a pause of its Harness.
	pausedPrefix = "paused until "
	// noTimeWait is the pause after a usage limit with no reset time.
	noTimeWait = 30 * time.Minute
	// resetTolerance is how long after a reset time in the text of a usage limit the reset time still counts as now.
	resetTolerance = 5 * time.Minute
	// retryWindow is how long after the end of a pause a new usage limit of the same Harness still counts as a retry
	// that failed again.
	retryWindow = 5 * time.Minute
)

const dshTurnFailed = "Internal error: turn failed: "

var dshLimit = regexp.MustCompile(`(?i)\binsufficient[\s_-]+(quota|balance|credits?)\b` +
	`|\b(quota|usage[\s_-]+limit)[\s_-]+(exceeded|exhausted|reached)\b` +
	`|\bexceed(ed|s)?[\s_-]+((your|the)[\s_-]+)?(current[\s_-]+)?quota\b` +
	`|\b(balance|credits?)[\s_-]+(exhausted|depleted)\b` +
	`|\bout[\s_-]+of[\s_-]+(credits?|budget)\b` +
	`|rate limit|request failed \(402\)|request failed \(429\)`)

// limitData holds the fields of the data of an ACP error that tell a usage limit.
type limitData struct {
	ErrorKind         string `json:"errorKind"`
	RetryAfterSeconds *int64 `json:"retryAfterSeconds"`
	CodexErrorInfo    string `json:"codexErrorInfo"`
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
	case config.DSH:
		// dsh puts the message of a failed turn after "Internal error: turn failed: ", with no HTTP status, no retry time
		// and no error kind. The patterns follow isQuotaExceededError of @deepseek-ai/dsh-llm, and the texts follow
		// providerError of @deepseek-ai/dsh-llm-deepseek (both 0.2.1-rc.5).
		failure, ok := strings.CutPrefix(err.Message, dshTurnFailed)
		if err.Code != -32603 || !ok || !dshLimit.MatchString(failure) {
			return time.Time{}, false
		}
	case config.Codex:
		// The message names the reset time only in the local time of the machine, so the pause has no reset time.
		if err.Code != -32603 || data.CodexErrorInfo != "usageLimitExceeded" {
			return time.Time{}, false
		}
	}
	if reset.IsZero() {
		return now.Add(noTimeWait), true
	}
	return reset, true
}

// resetsAt reads "resets 3pm" or "resets 17:30" in text, and gives the next such time in UTC, or zero. The clock time
// is in the time zone that follows it in parentheses, for example "resets 5:10pm (Europe/Warsaw)". It is in UTC when
// the text has no time zone or the time zone is not known. A time that has just passed gives zero too.
func resetsAt(text string, now time.Time) time.Time {
	_, rest, ok := strings.Cut(text, "resets ")
	if !ok {
		return time.Time{}
	}
	zone := time.UTC
	if end := strings.IndexFunc(rest, func(r rune) bool {
		return r > unicode.MaxASCII || !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != ':'
	}); end >= 0 {
		if name, ok := strings.CutPrefix(rest[end:], " ("); ok {
			if name, _, ok := strings.Cut(name, ")"); ok {
				if loaded, err := time.LoadLocation(name); err == nil {
					zone = loaded
				}
			}
		}
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
	now = now.In(zone)
	today := time.Date(now.Year(), now.Month(), now.Day(), int(hour), int(minute), 0, 0, zone)
	if today.After(now) {
		return today.UTC()
	}
	if now.Sub(today) <= resetTolerance {
		return time.Time{}
	}
	return today.AddDate(0, 0, 1).UTC()
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
		"second": time.Second, "seconds": time.Second,
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

// pauseForReplyLimit pauses the Harness of a until the reset time in text, or noTimeWait when text has no reset time.
// The subscription error of Claude Code has no reset time, so subscription pauses for noTimeWait.
func (a *Agent) pauseForReplyLimit(ctx context.Context, text string, subscription bool) error {
	now := time.Now().UTC()
	until := now.Add(noTimeWait)
	cause := subscriptionError
	if !subscription {
		cause = usageLimit
		if reset := resetIn(text, now); !reset.IsZero() {
			until = reset
		}
	}
	log.Printf("usage limit of %s in reply text: pause until %s", a.binding.Harness, until.UTC().Format(timeFormat))
	return a.pauseForSession(ctx, until, cause)
}

// pauseForLimit gives true when err of a prompt is a usage limit of the Harness of a. Then it pauses the Harness.
func (a *Agent) pauseForLimit(ctx context.Context, err error) (bool, error) {
	var requestErr *acp.RequestError
	if !errors.As(err, &requestErr) {
		return false, nil
	}
	now := time.Now().UTC()
	a.mu.Lock()
	hint := a.resetHint
	a.mu.Unlock()
	reported, hintState := "none", "missing"
	if !hint.IsZero() {
		reported = hint.UTC().Format(timeFormat)
	}
	// A reset time that is not later than now is an old hint.
	if !hint.IsZero() && !hint.After(now) {
		hint, hintState = time.Time{}, "too old"
	}
	until, ok := detect(a.binding.Harness, requestErr, hint, now)
	if !ok {
		return false, nil
	}
	if !hint.IsZero() {
		hintState = "used"
	}
	log.Printf("usage limit of %s: reset time of the usage update %s (%s), pause until %s", a.binding.Harness, reported, hintState, until.UTC().Format(timeFormat))
	if err := a.pauseForSession(ctx, until, usageLimit); err != nil {
		return false, err
	}
	return true, nil
}

// pauseForSession pauses the Harness of a until until. The save prompt of a closing chat sends no prompt again, so its
// pause adds no chat message that promises it. cause tells why the Harness pauses.
func (a *Agent) pauseForSession(ctx context.Context, until time.Time, cause pauseCause) error {
	if a.isSaving() {
		_, err := a.engine.pauseQuiet(ctx, a.binding.Harness, until, cause.reason())
		return err
	}
	return a.engine.pause(ctx, a, until, cause)
}

// pauseCause is the cause of a pause of a Harness.
type pauseCause int

const (
	// usageLimit is a usage limit.
	usageLimit pauseCause = iota
	// subscriptionError is the subscription error of Claude Code in a turn. Prompt sends the prompt again after the
	// pause.
	subscriptionError
	// accountNotice is an account notification of Claude Code. The turn that runs does not change, so the pause
	// promises no resend.
	accountNotice
)

// limitText is the Mobius message of a usage limit of harness with the end of the pause until. It has the same form
// for each Harness.
func limitText(harness config.Harness, until time.Time) string {
	return fmt.Sprintf("%s reached a usage limit. Mobius sends the prompt again at %s.", harness, until.UTC().Format(timeFormat))
}

// subscriptionText is the Mobius message of the subscription error of Claude Code with the end of the pause until.
// account is the account that Claude Code uses, or "" when it is not known. With resend, Mobius sends the prompt of the
// turn again. Without it, the next prompt waits.
func subscriptionText(until time.Time, account string, resend bool) string {
	if account != "" {
		account = " The account is: " + account + "."
	}
	next := "The next prompt waits until"
	if resend {
		next = "Mobius sends the prompt again at"
	}
	return fmt.Sprintf("Claude Code cannot use the Claude subscription.%s The Owner must pay or renew at https://claude.ai/settings/billing. %s %s.", account, next, until.UTC().Format(timeFormat))
}

// pauseText gives the message of the pause of a until until.
func (a *Agent) pauseText(until time.Time, cause pauseCause) string {
	if cause == usageLimit {
		return limitText(a.binding.Harness, until)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return subscriptionText(until, a.account, cause == subscriptionError)
}

// authStatus handles an account notification of Claude Code. An account that is not the Claude subscription pauses
// the Harness for noTimeWait, before the turn fails. It does not change the turn that runs: a turn that ends with no
// error keeps its reply and is not sent again. The Claude subscription ends that pause. The notification
// auth/status_update is a part of ACP, so the other Harnesses can send it too: they pause no Harness.
func (a *Agent) authStatus(status runner.AuthStatus) {
	if a.binding.Harness != config.ClaudeCode {
		return
	}
	ctx := context.Background()
	if status.Kind == "account" {
		a.mu.Lock()
		a.account = ""
		a.mu.Unlock()
		if err := a.engine.endAuthPause(ctx); err != nil {
			log.Printf("end the pause of %s after the notification of the Claude subscription: %v", a.binding.Harness, err)
		}
		return
	}
	account := status.Label
	if status.Detail != "" {
		account += " (" + status.Detail + ")"
	}
	a.mu.Lock()
	a.account = account
	a.mu.Unlock()
	until := time.Now().UTC().Add(noTimeWait)
	log.Printf("account of %s is %s: pause until %s", a.binding.Harness, account, until.Format(timeFormat))
	if err := a.pauseForSession(ctx, until, accountNotice); err != nil {
		log.Printf("pause %s for the account %s: %v", a.binding.Harness, account, err)
		return
	}
	if err := a.engine.markAuthPause(ctx, a.binding.Harness, until); err != nil {
		log.Printf("read the pause of %s: %v", a.binding.Harness, err)
	}
}

// markAuthPause keeps the pause of harness as the pause of an account notification, when its end is until. A pause
// that was there before, or that has another end, is not kept.
func (e *Engine) markAuthPause(ctx context.Context, harness config.Harness, until time.Time) error {
	e.pausing.Lock()
	defer e.pausing.Unlock()
	current, err := e.queries.GetHarnessPause(ctx, string(harness))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.PausedUntil == until.UTC().Format(time.RFC3339Nano) && current.Reason == subscriptionReason {
		e.authPause = current
	}
	return nil
}

// endAuthPause ends the pause of the last account notification, unless it ended or another pause replaced it. A pause
// of the reply text ends only by time or by Resume: a new process of Claude Code sends the notification of the Claude
// subscription at its start, also when the payment is past due.
func (e *Engine) endAuthPause(ctx context.Context) error {
	e.pausing.Lock()
	pause := e.authPause
	e.authPause = store.HarnessPause{}
	e.pausing.Unlock()
	if pause.Harness == "" {
		return nil
	}
	return e.endPauseSince(ctx, &pause)
}

// pause pauses the Harness of a until the time until. The first pause also adds a message in the chat of the
// Workstream of a. A retry that fails again within retryWindow adds the message only for a session with a chat. A
// session with a chat gets the message in its chat for each usage limit that it hits, also for a pause that another
// session started.
func (e *Engine) pause(ctx context.Context, a *Agent, until time.Time, cause pauseCause) error {
	e.pausing.Lock()
	defer e.pausing.Unlock()
	current, err := e.queries.GetHarnessPause(ctx, string(a.binding.Harness))
	if err == nil {
		if a.author == "" {
			return nil
		}
		currentEnd, err := pauseEnd(current)
		if err != nil {
			return err
		}
		text := a.pauseText(*currentEnd, cause)
		spec := a.spec
		last, err := e.queries.GetLastChatMessageOf(ctx, store.GetLastChatMessageOfParams{
			Organization: spec.Organization, Repository: spec.WorkstreamRepository, Workstream: spec.Workstream, Author: mobiusAuthor,
		})
		if err == nil && last.Text == text {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return e.addLimitMessage(ctx, a, text)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	retry, err := e.pauseEndedWithin(ctx, a.binding.Harness, retryWindow)
	if err != nil {
		return err
	}
	if !retry || a.author != "" {
		if err := e.addLimitMessage(ctx, a, a.pauseText(until, cause)); err != nil {
			return err
		}
	}
	pause := store.HarnessPause{Harness: string(a.binding.Harness), PausedUntil: until.UTC().Format(time.RFC3339Nano), Reason: cause.reason()}
	if err := e.queries.SetHarnessPause(ctx, store.SetHarnessPauseParams(pause)); err != nil {
		return err
	}
	e.publish(Change{Pauses: true})
	return nil
}

// subscriptionReason is the reason of a pause for the subscription error of Claude Code. The reason of a usage limit is
// empty.
const subscriptionReason = "subscription"

func (c pauseCause) reason() string {
	if c == usageLimit {
		return ""
	}
	return subscriptionReason
}

// pauseQuiet pauses harness until until when it has no pause. It adds no chat message. reason is the reason of the
// pause.
func (e *Engine) pauseQuiet(ctx context.Context, harness config.Harness, until time.Time, reason string) (time.Time, error) {
	e.pausing.Lock()
	defer e.pausing.Unlock()
	current, err := e.queries.GetHarnessPause(ctx, string(harness))
	if err == nil {
		end, err := pauseEnd(current)
		if err != nil {
			return until, err
		}
		return *end, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return until, err
	}
	pause := store.HarnessPause{Harness: string(harness), PausedUntil: until.UTC().Format(time.RFC3339Nano), Reason: reason}
	if err := e.queries.SetHarnessPause(ctx, store.SetHarnessPauseParams(pause)); err != nil {
		return until, err
	}
	e.publish(Change{Pauses: true})
	return until, nil
}

// addLimitMessage adds text to the chat of the Workstream of a.
func (e *Engine) addLimitMessage(ctx context.Context, a *Agent, text string) error {
	spec := a.spec
	_, err := e.addChatMessage(ctx, ChatKey{spec.Organization, spec.WorkstreamRepository, spec.Workstream}, mobiusAuthor, text, "", "", nil, nil, false, 0)
	return err
}

func (e *Engine) endDuePauses(ctx context.Context) {
	pauses, err := e.queries.ListHarnessPauses(ctx)
	if err != nil {
		log.Printf("list harness pauses: %v", err)
		return
	}
	now := e.timeNow()
	for _, pause := range pauses {
		until, err := pauseEnd(pause)
		if err != nil {
			log.Printf("parse pause of %s: %v", pause.Harness, err)
			continue
		}
		if !until.After(now) {
			if err := e.endPauseSince(ctx, &pause); err != nil {
				log.Printf("end the pause of %s: %v", pause.Harness, err)
			}
		}
	}
}

// Resume ends the pause of harness now. A Harness with no pause is no error.
func (e *Engine) Resume(ctx context.Context, harness string) error {
	pause, err := e.harnessPause(ctx, config.Harness(harness))
	if err != nil || pause == nil {
		return err
	}
	return e.endPause(ctx, *pause)
}

// The kinds of a PauseUser.
const (
	pauseUserRole       = "role"
	pauseUserWorkstream = "workstream"
	pauseUserTask       = "task"
	pauseUserSession    = "session"
)

// PauseUser is a user of a paused Harness: a Role binding, a setting of a Workstream or of a task, or an open session
// that waits for the pause. Binding is set only for a Role binding of a Role with more than one binding. Repository,
// Number and Title name the issue of the user, and WorkstreamRepository, Workstream and WorkstreamTitle name its
// Workstream. A Role binding has no issue and no Workstream.
type PauseUser struct {
	Kind                 string
	Role                 string
	Binding              string
	Repository           string
	Number               int64
	Title                string
	WorkstreamRepository string
	Workstream           int64
	WorkstreamTitle      string
}

// PauseState is the pause of a Harness with its users. Users is empty when nothing uses the Harness.
type PauseState struct {
	store.HarnessPause
	Users []PauseUser
}

// Pauses gives the pause of each Harness that has one.
func (e *Engine) Pauses(ctx context.Context) ([]PauseState, error) {
	pauses, err := e.queries.ListHarnessPauses(ctx)
	if err != nil {
		return nil, err
	}
	workstreams, err := e.queries.ListWorkstreamSettingUsers(ctx)
	if err != nil {
		return nil, err
	}
	tasks, err := e.queries.ListTaskSettingUsers(ctx)
	if err != nil {
		return nil, err
	}
	waiting, err := e.queries.ListWaitingSessions(ctx, sql.NullString{String: string(waitPaused), Valid: true})
	if err != nil {
		return nil, err
	}
	roles := e.mergedRoles()
	implementer := bindingList(roles.Implementer, e.implementerBindings())
	states := make([]PauseState, 0, len(pauses))
	for _, pause := range pauses {
		users := []PauseUser{}
		for _, named := range roles.Bindings() {
			if named.Role == ImplementerRole {
				for _, b := range implementer {
					if string(b.Binding.Harness) != pause.Harness {
						continue
					}
					user := PauseUser{Kind: pauseUserRole, Role: named.Role}
					if len(implementer) > 1 {
						user.Binding = string(b.Binding.Harness) + " · " + b.Binding.Model
					}
					users = append(users, user)
				}
			} else if string(named.Binding.Harness) == pause.Harness {
				users = append(users, PauseUser{Kind: pauseUserRole, Role: named.Role})
			}
		}
		for _, row := range workstreams {
			if row.Harness == pause.Harness {
				users = append(users, PauseUser{
					Kind: pauseUserWorkstream, Role: row.Role,
					Repository: row.Repository, Number: row.Number, Title: row.Title,
					WorkstreamRepository: row.Repository, Workstream: row.Number, WorkstreamTitle: row.Title,
				})
			}
		}
		for _, row := range tasks {
			if row.Harness == pause.Harness {
				users = append(users, PauseUser{
					Kind: pauseUserTask, Role: row.Role,
					Repository: row.Repository, Number: row.Number, Title: row.Title,
					WorkstreamRepository: row.WorkstreamRepository, Workstream: row.Workstream, WorkstreamTitle: row.WorkstreamTitle,
				})
			}
		}
		for _, row := range waiting {
			if row.Harness != pause.Harness {
				continue
			}
			user := PauseUser{Kind: pauseUserSession, Role: row.Role}
			if row.Workstream != 0 {
				user.Repository, user.Number, user.Title = row.WorkstreamRepository, row.Workstream, row.WorkstreamTitle.String
				user.WorkstreamRepository, user.Workstream, user.WorkstreamTitle = row.WorkstreamRepository, row.Workstream, row.WorkstreamTitle.String
				if row.Issue.Valid {
					user.Repository, user.Number, user.Title = row.Repository, row.Issue.Int64, row.IssueTitle.String
				}
			}
			if !slices.Contains(users, user) {
				users = append(users, user)
			}
		}
		states = append(states, PauseState{HarnessPause: pause, Users: users})
	}
	return states, nil
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
	ended := store.SetHarnessPauseEndParams{Harness: pause.Harness, EndedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := e.queries.SetHarnessPauseEnd(ctx, ended); err != nil {
		return err
	}
	if err := e.queries.DeleteHarnessPause(ctx, pause.Harness); err != nil {
		return err
	}
	e.publish(Change{Pauses: true})
	e.pausesChanged.notify()
	e.workers.changed.notify()
	return nil
}

// pauseEndedWithin tells if the last pause of harness ended less than window ago.
func (e *Engine) pauseEndedWithin(ctx context.Context, harness config.Harness, window time.Duration) (bool, error) {
	text, err := e.queries.GetHarnessPauseEnd(ctx, string(harness))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ended, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return false, err
	}
	return time.Since(ended) <= window, nil
}

func pausedReason(pause store.HarnessPause) (string, error) {
	until, err := time.Parse(time.RFC3339Nano, pause.PausedUntil)
	if err != nil {
		return "", err
	}
	return pausedPrefix + until.UTC().Format(timeFormat), nil
}

// PausedUntil gives the end of the pause that session waits for, or nil when the session does not wait for a pause.
func (e *Engine) PausedUntil(ctx context.Context, session store.Session) (*time.Time, error) {
	return e.pausedUntil(ctx, session.Harness, Wait(session.QueueWait.String))
}

// waitsForPause tells if the open session id has the queue reason of a wait for a pause.
func (e *Engine) waitsForPause(ctx context.Context, id int64) (bool, error) {
	session, err := e.queries.GetSession(ctx, id)
	if err != nil {
		return false, err
	}
	return Wait(session.QueueWait.String) == waitPaused, nil
}

func pauseEnd(pause store.HarnessPause) (*time.Time, error) {
	until, err := time.Parse(time.RFC3339Nano, pause.PausedUntil)
	if err != nil {
		return nil, err
	}
	return &until, nil
}

// movingRoles are the Roles whose session leaves a wait for a pause when the Harness of its Role changes.
var movingRoles = map[string]bool{
	LeadRole: true, CoordinatorRole: true, ImplementerRole: true, ReviewerRole: true, JudgeRole: true,
	TriagerRole: true, CuratorRole: true, DiagnoserRole: true, ResearcherRole: true,
}

// errMoved tells that the Owner changed the Harness of the Role of a session while it waited for a pause, or that
// another binding of the Implementer has no hold. The session ends with the reason "moved", and its caller starts the
// work again on the new binding.
var errMoved = errors.New("the Harness of the Role changed")

// watchMove adds a to pauseWaiters. It gives the channel that closes when a must move, and the function that removes a.
func (e *Engine) watchMove(a *Agent) (<-chan struct{}, func()) {
	moved := make(chan struct{})
	e.pauseWaitersMu.Lock()
	e.pauseWaiters[a] = moved
	e.pauseWaitersMu.Unlock()
	roles := e.mergedRoles()
	e.moveWaiters(&roles, e.implementerList())
	return moved, func() {
		e.pauseWaitersMu.Lock()
		delete(e.pauseWaiters, a)
		e.pauseWaitersMu.Unlock()
	}
}

// moveWaiters wakes each agent in pauseWaiters of a moving Role whose Harness is not the Harness of its Role in roles.
// For the Implementer, the Harness of each binding of implementers counts. A session that got its binding from a
// setting of its task or Workstream never moves.
func (e *Engine) moveWaiters(roles *config.Roles, implementers []ImplementerBinding) {
	e.pauseWaitersMu.Lock()
	defer e.pauseWaitersMu.Unlock()
	for a, moved := range e.pauseWaiters {
		if !movingRoles[a.spec.Role] || a.level != LevelRole {
			continue
		}
		binding, _ := roleBinding(roles, a.spec.Role)
		follows := binding.Harness == a.binding.Harness
		if a.spec.Role == ImplementerRole {
			follows = slices.ContainsFunc(implementers, func(b ImplementerBinding) bool { return b.Binding.Harness == a.binding.Harness })
		}
		if !follows {
			close(moved)
			delete(e.pauseWaiters, a)
		}
	}
}

// waitForPause holds until the Harness of a has no pause. The session shows the pause in its queue reason while it
// waits. While the drain is on, the drain does not count the session. After the end of the pause, the session counts
// again before it goes on. After seal, it does not go on: it waits until abortDrain or the restart ends it. A session of
// a moving Role gives errMoved when the Harness of its Role changes. An Implementer session that follows the Role also
// gives errMoved when a binding with another Harness has no hold. A value on stop gives errStopped when a stop is
// pending. A nil stop never ends the wait.
func (a *Agent) waitForPause(ctx context.Context, stop <-chan struct{}) error {
	e := a.engine
	moved, forget := e.watchMove(a)
	defer forget()
	shown := false
	for {
		changed := e.pausesChanged.wait()
		pause, err := e.queries.GetHarnessPause(ctx, string(a.binding.Harness))
		if errors.Is(err, sql.ErrNoRows) {
			if !a.setUncounted(false) {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-changed:
					continue
				}
			}
			if !shown {
				return nil
			}
			return a.clearPauseReason(ctx)
		}
		if err != nil {
			return err
		}
		if a.spec.Role == ImplementerRole && a.level == LevelRole {
			free, err := e.hasFreeImplementerBinding(ctx, a.binding.Harness)
			if err != nil {
				return err
			}
			if free {
				if !a.setUncounted(false) {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-changed:
						continue
					}
				}
				return errMoved
			}
		}
		if !shown {
			reason, err := pausedReason(pause)
			if err != nil {
				return err
			}
			queued, err := e.setQueueWait(ctx, a.id, queueWait{waitPaused, reason})
			if err != nil {
				return err
			}
			e.publish(Change{Node: new(e.node(queued))})
			e.publish(Change{Pauses: true})
			e.publishChat(a)
			shown = true
		}
		a.setUncounted(e.draining())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-moved:
			return errMoved
		case <-stop:
			a.mu.Lock()
			pending := a.stopRequested
			a.mu.Unlock()
			if pending {
				a.setUncounted(false)
				if shown {
					if err := a.clearPauseReason(ctx); err != nil {
						return err
					}
				}
				return errStopped
			}
		case <-changed:
		}
	}
}

// hasFreeImplementerBinding tells if a binding of the Implementer with a Harness other than harness has no pause of its
// Harness and no access hold.
func (e *Engine) hasFreeImplementerBinding(ctx context.Context, harness config.Harness) (bool, error) {
	for _, b := range e.implementerList() {
		if b.Binding.Harness == harness {
			continue
		}
		paused, err := e.pausedHarness(ctx, b.Binding.Harness)
		if err != nil {
			return false, err
		}
		if paused.none() && e.accessHold(b.Binding).none() {
			return true, nil
		}
	}
	return false, nil
}

// clearPauseReason removes the queue reason of a wait for a pause from the session of a.
func (a *Agent) clearPauseReason(ctx context.Context) error {
	e := a.engine
	cleared, err := e.queries.ClearQueueReason(ctx, a.id)
	if err != nil {
		return err
	}
	e.publish(Change{Node: new(e.node(cleared))})
	e.publish(Change{Pauses: true})
	e.publishChat(a)
	return nil
}
