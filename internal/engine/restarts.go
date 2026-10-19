package engine

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
	"unicode/utf8"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// errorTail is the number of characters of the end of an error in a stop event.
const errorTail = 2000

// restartDelays are the waits before the first, the second, and each later restart of a Worker.
var restartDelays = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

// rateLimitGrace is the wait after the reset of a GitHub rate limit before the Worker starts again. The first call after
// the reset must not fail on the same limit.
var rateLimitGrace = 5 * time.Second

// abuseWait is the wait after a secondary rate limit that gives no RetryAfter, before rateLimitGrace.
const abuseWait = time.Minute

// errWorkerLost is the error of a Worker that the poll restarts because the task has no Worker.
var errWorkerLost = errors.New("the Worker of the task ended with no state change")

// restartLost starts the Worker of each queued or working task of repository that has no Worker, and moves a working
// task of a Judge back to its earlier state, so that the poll gives the items to a new Judge.
//
// A task that waits for a free slot has a Worker, because its goroutine runs and waits. A step that moves a task to
// queued or working before it starts the Worker holds the task (holdWorker), and the hand-over from one Worker to the
// next starts the next goroutine before the old one returns. Thus a task with no goroutine and no hold has lost its
// Worker. The Worker starts again like after any error: the restart counts and waits, and startIdleWorker starts no
// second Worker. afterRestart tells that the server restarted. Then no Worker of the earlier run exists, and the
// Worker starts again at once with no count and no wait.
func (e *Engine) restartLost(ctx context.Context, repository github.Repository, afterRestart bool) error {
	tasks, err := e.queries.ListLiveTasks(ctx, repository.FullName)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.State != "queued" && task.State != "working" || e.hasWorker(task.ID) {
			continue
		}
		switch task.Worker.String {
		case ImplementerRole, checkRoundWorker, conflictRoundWorker:
			e.restartImplementer(repository, task, !afterRestart)
		case ReviewerRole:
			e.restartReviewer(repository, task, !afterRestart)
		// The poll gives the items to a new Judge.
		case JudgeRole:
			before := cmp.Or(task.WorkerInput.String, "reviewed")
			if _, err := e.setTaskState(ctx, store.SetTaskStateParams{State: before, ID: task.ID, FromState: "working"}); err != nil {
				log.Printf("start the %s of %s#%d again: %v", task.Worker.String, repository.FullName, task.Issue, err)
			}
		}
	}
	return nil
}

// RestartWorker tells if the Worker of the task starts again after err. title is the title of the issue of the task.
//
// Before true, RestartWorker waits: the wait grows with each restart. A GitHub rate limit in err is not a failure of the
// Worker. Then RestartWorker does not count a restart, and it waits until the reset of the limit plus rateLimitGrace.
// At max_worker_restarts, it hands the task to a human, adds a stop event with the end of err for the Lead, and gives
// false. A task that is not queued or working also gives false.
func (e *Engine) RestartWorker(ctx context.Context, task store.Task, title string, err error) (bool, error) {
	if wait, limited := rateLimitWait(err, e.timeNow()); limited {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(wait):
		}
		return true, nil
	}
	restarts, dbErr := e.queries.AddWorkerRestart(ctx, store.AddWorkerRestartParams{ID: task.ID, Max: int64(e.config.MaxWorkerRestarts)})
	if dbErr == nil {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(restartDelay(restarts)):
		}
		return true, nil
	}
	if !errors.Is(dbErr, sql.ErrNoRows) {
		return false, dbErr
	}
	reason := fmt.Sprintf("the Worker failed after %d restarts. Mobius added mobius:needs-human. The last error ends with these lines:\n\n```\n%s\n```",
		e.config.MaxWorkerRestarts, tail(err.Error(), errorTail))
	return false, e.handOverFailed(ctx, task, title, reason, "Restart limit", "The Worker failed too many times. Mobius does not start it again.", stopNotice{
		reason: "Restart limit",
		text:   fmt.Sprintf("The Worker failed after %d restarts. Mobius does not start it again.", e.config.MaxWorkerRestarts),
		output: fmt.Sprintf("The last error ends with these lines:\n\n```\n%s\n```", tail(err.Error(), errorTail)),
		next:   "Read the error in the Lead chat or the Inbox. Fix the cause, then Resume.",
	})
}

// restartDelay gives the wait before the restart number restart, which counts from 1.
func restartDelay(restart int64) time.Duration {
	return restartDelays[min(int(restart), len(restartDelays))-1]
}

// rateLimitWait tells if err holds a GitHub rate limit, and gives the wait until its reset at now plus rateLimitGrace.
func rateLimitWait(err error, now time.Time) (time.Duration, bool) {
	var rateLimit *gh.RateLimitError
	if errors.As(err, &rateLimit) {
		return max(rateLimit.Rate.Reset.Sub(now), 0) + rateLimitGrace, true
	}
	var abuse *gh.AbuseRateLimitError
	if errors.As(err, &abuse) {
		wait := abuseWait
		if abuse.RetryAfter != nil {
			wait = *abuse.RetryAfter
		}
		return wait + rateLimitGrace, true
	}
	return 0, false
}

// tail gives the last count characters of text.
func tail(text string, count int) string {
	for extra := utf8.RuneCountInString(text) - count; extra > 0; extra-- {
		_, size := utf8.DecodeRuneInString(text)
		text = text[size:]
	}
	return text
}

// handToHuman moves the task from working, queued or checks to needs_human, and stores the stop notice of the move in
// the same transaction. Then it moves the issue of the task from mobius:working or mobius:review to mobius:needs-human,
// with the pull request of the task. A caller sends the notice with sendStopNotices after its other steps. When a GitHub
// call fails, the poll sends the notice. leadText is the text of the stop event. It gives false when the task is not
// working, queued or checks, for example after a decline. A notice with no text gives the Lead an event only.
// failedHead is the head that the CI failed on, or empty for another stop. The move stores it in the same transaction,
// so that continueNeedsHuman sees a CI stop also when a later call fails.
func (e *Engine) handToHuman(ctx context.Context, task store.Task, title, leadText, failedHead string, notice stopNotice) (bool, error) {
	return e.handFromStates(ctx, []string{"working", "queued", "checks"}, task, title, leadText, failedHead, notice)
}

// handFromStates does handToHuman for a task in one of the states.
func (e *Engine) handFromStates(ctx context.Context, states []string, task store.Task, title, leadText, failedHead string, notice stopNotice) (bool, error) {
	from := ""
	err := e.inTx(ctx, func(q *store.Queries) error {
		for _, state := range states {
			moved, err := q.HandTaskToHuman(ctx, store.HandTaskToHumanParams{ID: task.ID, FromState: state})
			if err != nil {
				return err
			}
			if moved > 0 {
				from = state
				if err := q.SetTaskCiFailedHead(ctx, store.SetTaskCiFailedHeadParams{CiFailedHead: sql.NullString{String: failedHead, Valid: failedHead != ""}, ID: task.ID}); err != nil {
					return err
				}
				return q.AddStopNotice(ctx, storeStopNotice(task, title, leadText, notice))
			}
		}
		return nil
	})
	if err != nil || from == "" {
		return false, err
	}
	e.publishReadyForReview(from, "needs_human")
	if err := e.dismissReadyForReview(ctx, task, from); err != nil {
		return false, err
	}
	repository, err := e.repository(task.Repository)
	if err != nil {
		return false, err
	}
	current, err := e.queries.GetLiveTask(ctx, store.GetLiveTaskParams{Repository: task.Repository, Issue: task.Issue})
	if err != nil {
		return false, err
	}
	return true, setStopLabels(ctx, repository, current)
}

// setStopLabels moves the issue of the task from mobius:working or mobius:review to mobius:needs-human, with the pull
// request of the task.
func setStopLabels(ctx context.Context, repository github.Repository, task store.Task) error {
	if err := repository.RemoveLabel(ctx, task.Issue, workingLabel); err != nil {
		return err
	}
	if err := repository.RemoveLabel(ctx, task.Issue, reviewLabel); err != nil {
		return err
	}
	return addNeedsHuman(ctx, repository, task)
}

// addNeedsHuman adds mobius:needs-human to the issue of the task, and to its pull request when it has one.
func addNeedsHuman(ctx context.Context, repository github.Repository, task store.Task) error {
	if err := repository.AddLabel(ctx, task.Issue, needsHumanLabel); err != nil {
		return err
	}
	if !task.PullRequest.Valid {
		return nil
	}
	return repository.AddLabel(ctx, task.PullRequest.Int64, needsHumanLabel)
}

// removeNeedsHuman removes mobius:needs-human from the issue of the task, and from its pull request when it has one.
func removeNeedsHuman(ctx context.Context, repository github.Repository, task store.Task) error {
	if err := repository.RemoveLabel(ctx, task.Issue, needsHumanLabel); err != nil {
		return err
	}
	if !task.PullRequest.Valid {
		return nil
	}
	return repository.RemoveLabel(ctx, task.PullRequest.Int64, needsHumanLabel)
}
