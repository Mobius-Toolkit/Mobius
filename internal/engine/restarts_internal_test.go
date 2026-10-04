package engine

import (
	"errors"
	"fmt"
	"testing"
	"time"

	gh "github.com/google/go-github/v92/github"
)

func TestTheWaitBeforeARestartGrowsWithEachRestart(t *testing.T) {
	now := time.Now()
	failure := errors.New("exit 1")

	for restart, want := range map[int64]time.Duration{1: restartDelays[0], 2: restartDelays[1], 3: restartDelays[2], 7: restartDelays[2]} {
		if got := restartDelay(restart, failure, now); got != want {
			t.Errorf("restart %d waits %v, want %v", restart, got, want)
		}
	}
}

func TestARestartAfterAGitHubRateLimitWaitsForItsReset(t *testing.T) {
	now := time.Now()
	reset := now.Add(time.Hour)
	limit := fmt.Errorf("read the issue: %w", &gh.RateLimitError{Rate: gh.Rate{Reset: gh.Timestamp{Time: reset}}, Message: "API rate limit exceeded"})
	retry := time.Hour
	abuse := &gh.AbuseRateLimitError{Message: "secondary rate limit", RetryAfter: &retry}

	if got := restartDelay(1, limit, now); got != time.Hour {
		t.Errorf("wait after the rate limit = %v", got)
	}
	if got := restartDelay(1, abuse, now); got != time.Hour {
		t.Errorf("wait after the secondary rate limit = %v", got)
	}
}

func TestTheStopEventKeepsTheEndOfALongError(t *testing.T) {
	if got := tail("abcdé", 3); got != "cdé" {
		t.Errorf("tail = %q", got)
	}
	if got := tail("ab", 3); got != "ab" {
		t.Errorf("tail = %q", got)
	}
}
