package engine_test

import (
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

type waitFields struct {
	Number      int64      `json:"number"`
	Issue       *int64     `json:"issue"`
	Wait        string     `json:"wait"`
	PausedUntil *time.Time `json:"pausedUntil"`
}

func TestATaskThatWaitsForAHarnessPauseHasTheWaitPausedWithTheSameEndOnTheBoardInTheTreeAndOnTheAgent(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStartsTwo, usageLimit, noChange)
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	found := testkit.WaitForValue(t, func() (pause, bool) { return devinPause(t, server) })
	until := parseTime(t, found.until)

	fake.AddIssue(shop, 43, "Add plan price")
	fake.AddSubIssue(shop, 12, 43)
	fake.AddLabel(shop, 43, "mobius:ready", "owner")

	paused := func(entries []waitFields, number int64, issue bool) bool {
		for _, entry := range entries {
			matches := entry.Number == number
			if issue {
				matches = entry.Issue != nil && *entry.Issue == number
			}
			if matches {
				return entry.Wait == "paused" && entry.PausedUntil != nil && entry.PausedUntil.Equal(until)
			}
		}
		return false
	}
	for _, number := range []int64{41, 43} {
		testkit.WaitFor(t, func() bool {
			return paused(apiData[[]waitFields](t, server, "/api/tasks"), number, false)
		})
		testkit.WaitFor(t, func() bool {
			return paused(apiData[[]waitFields](t, server, "/api/workstreams/owner/shop/12/tasks"), number, false)
		})
		testkit.WaitFor(t, func() bool {
			return paused(apiData[[]waitFields](t, server, "/api/workstreams/owner/shop/12/agents"), number, true)
		})
	}
}
