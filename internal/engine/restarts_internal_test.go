package engine

import (
	"errors"
	"fmt"
	"testing"
	"time"

	gh "github.com/google/go-github/v92/github"
)

func TestTheWaitBeforeARestartGrowsWithEachRestart(t *testing.T) {
	t.Parallel()
	for restart, want := range map[int64]time.Duration{1: restartDelays[0], 2: restartDelays[1], 3: restartDelays[2], 7: restartDelays[2]} {
		if got := restartDelay(restart); got != want {
			t.Errorf("restart %d waits %v, want %v", restart, got, want)
		}
	}
}

func TestTheWaitAfterAGitHubRateLimitEndsFiveSecondsAfterItsReset(t *testing.T) {
	t.Parallel()
	now := time.Now()
	limit := fmt.Errorf("read the issue: %w", &gh.RateLimitError{Rate: gh.Rate{Reset: gh.Timestamp{Time: now.Add(time.Hour)}}, Message: "API rate limit exceeded"})
	retry := 2 * time.Minute
	abuse := &gh.AbuseRateLimitError{Message: "secondary rate limit", RetryAfter: &retry}
	abuseWithNoWait := fmt.Errorf("read the issue: %w", &gh.AbuseRateLimitError{Message: "secondary rate limit"})

	for _, test := range []struct {
		name string
		err  error
		want time.Duration
	}{
		{"rate limit", limit, time.Hour + 5*time.Second},
		{"secondary rate limit", abuse, 2*time.Minute + 5*time.Second},
		{"secondary rate limit with no wait", abuseWithNoWait, time.Minute + 5*time.Second},
	} {
		if got, limited := rateLimitWait(test.err, now); !limited || got != test.want {
			t.Errorf("%s: wait = %v, %v, want %v", test.name, got, limited, test.want)
		}
	}
	if _, limited := rateLimitWait(errors.New("exit 1"), now); limited {
		t.Error("an error that is not a rate limit has a rate-limit wait")
	}
}

func TestTheStopEventKeepsTheEndOfALongError(t *testing.T) {
	t.Parallel()
	if got := tail("abcdé", 3); got != "cdé" {
		t.Errorf("tail = %q", got)
	}
	if got := tail("ab", 3); got != "ab" {
		t.Errorf("tail = %q", got)
	}
}
