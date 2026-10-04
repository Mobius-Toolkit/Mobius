package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// errorTail is the number of characters of the end of an error in a stop event.
const errorTail = 2000

// restartDelays are the waits before the first, the second, and each later restart of a Worker.
var restartDelays = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

// RestartWorker tells if the Worker of the task starts again after err. title is the title of the issue of the task.
//
// Before true, RestartWorker waits: the wait grows with each restart, and after a GitHub rate limit it lasts at
// least until the reset of the limit. At max_worker_restarts, it hands the task to a human, adds a stop event with
// the end of err for the Lead, and gives false. A task that is not queued or working also gives false.
func (e *Engine) RestartWorker(ctx context.Context, task store.Task, title string, err error) (bool, error) {
	restarts, dbErr := e.queries.AddWorkerRestart(ctx, store.AddWorkerRestartParams{ID: task.ID, Max: int64(e.config.MaxWorkerRestarts)})
	if dbErr == nil {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(restartDelay(restarts, err, time.Now())):
		}
		return true, nil
	}
	if !errors.Is(dbErr, sql.ErrNoRows) {
		return false, dbErr
	}
	reason := fmt.Sprintf("the Worker failed after %d restarts. Mobius added mobius:needs-human. The last error ends with these lines:\n\n```\n%s\n```",
		e.config.MaxWorkerRestarts, tail(err.Error(), errorTail))
	return false, e.handOver(ctx, task, title, reason)
}

// restartDelay gives the wait before the restart number restart, which counts from 1, after err at now.
func restartDelay(restart int64, err error, now time.Time) time.Duration {
	delay := restartDelays[min(int(restart), len(restartDelays))-1]
	var rateLimit *gh.RateLimitError
	if errors.As(err, &rateLimit) {
		delay = max(delay, rateLimit.Rate.Reset.Sub(now))
	}
	var abuse *gh.AbuseRateLimitError
	if errors.As(err, &abuse) && abuse.RetryAfter != nil {
		delay = max(delay, *abuse.RetryAfter)
	}
	return delay
}

// tail gives the last count characters of text.
func tail(text string, count int) string {
	for extra := utf8.RuneCountInString(text) - count; extra > 0; extra-- {
		_, size := utf8.DecodeRuneInString(text)
		text = text[size:]
	}
	return text
}

// handToHuman moves the task from working or queued to needs_human, and the issue of the task from mobius:working
// to mobius:needs-human. It gives false when the task is not working or queued, for example after a decline.
func (e *Engine) handToHuman(ctx context.Context, task store.Task) (bool, error) {
	moved := int64(0)
	for _, from := range []string{"working", "queued"} {
		if moved == 0 {
			var err error
			if moved, err = e.queries.SetTaskState(ctx, store.SetTaskStateParams{State: "needs_human", ID: task.ID, FromState: from}); err != nil {
				return false, err
			}
		}
	}
	if moved == 0 {
		return false, nil
	}
	repository, err := e.repository(task.Repository)
	if err != nil {
		return false, err
	}
	if err := repository.RemoveLabel(ctx, task.Issue, workingLabel); err != nil {
		return false, err
	}
	return true, repository.AddLabel(ctx, task.Issue, needsHumanLabel)
}
