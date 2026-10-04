package engine_test

import (
	"testing"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/config"
	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
)

func TestAFreeSlotGoesToTheFixRoundOfAnOldPullRequestBeforeANewTicket(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) { cfg.MaxAgents = 1 })
	running := start(t, server, implementerSpec(t, server, 43))
	ticketSpec := implementerSpec(t, server, 45)
	ticket := startLater(t.Context(), server, ticketSpec)
	waiting := queued(t, server, engine.ImplementerRole)
	fixSpec := implementerSpec(t, server, 41)
	server.Engine.ReplaceWork(map[int64]engine.Work{fixSpec.Task: {Repository: shop, PullRequest: 42, CreatedAt: time.Unix(1000, 0)}})
	fix := startLater(t.Context(), server, fixSpec)
	testkit.WaitFor(t, func() bool { return len(tree(t, server)) == 3 && tree(t, server)[2].Session.QueueReason.Valid })

	end(t, running, "done")

	fixAgent := await(t, fix)
	if got := session(t, server, waiting.ID); got.QueueReason.String != "no free agent slot (1/1)" {
		t.Errorf("ticket = %+v", got)
	}
	end(t, fixAgent, "done")
	ticketAgent := await(t, ticket)
	defer end(t, ticketAgent, "done")
	startsAfter(t, session(t, server, ticketAgent.ID()), session(t, server, fixAgent.ID()))
}

// A pull request with work for an agent does not hold a new ticket while a slot is free (Mobius#385).
func TestWithTwoFreeSlotsAFixAndANewTicketBothStart(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	fixSpec := implementerSpec(t, server, 41)
	server.Engine.ReplaceWork(map[int64]engine.Work{fixSpec.Task: {Repository: shop, PullRequest: 42, CreatedAt: time.Unix(1000, 0)}})

	ticket := start(t, server, implementerSpec(t, server, 45))
	defer end(t, ticket, "done")
	fix := start(t, server, fixSpec)
	defer end(t, fix, "done")

	for _, agent := range []*engine.Agent{ticket, fix} {
		if got := session(t, server, agent.ID()); got.QueueReason.Valid {
			t.Errorf("session = %+v", got)
		}
	}
}
