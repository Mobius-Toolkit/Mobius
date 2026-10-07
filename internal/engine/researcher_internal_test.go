package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

var (
	shopRepository = github.Repository{FullName: "owner/shop"}
	leadCaller     = caller{role: LeadRole, organization: "owner", repository: "owner/shop", workstream: 12}
)

func researchEngine(t *testing.T) *Engine {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, nil, limits(t), Agents{MCP: mcp.New()})
}

// runningResearcher gives a Researcher of the Workstream #12 that runs, the context of its Worker, and the Lead chat.
// The chat holds its messages in a queue.
func runningResearcher(t *testing.T, e *Engine) (*Agent, context.Context, *chat) {
	t.Helper()
	a, err := e.newAgent(t.Context(), Spec{Role: ResearcherRole, Organization: "owner", Repository: "owner/shop", Workstream: 12})
	if err != nil {
		t.Fatal(err)
	}
	e.researchers[a.id] = a
	ctx, stop := context.WithCancel(t.Context())
	e.stops[researcherKey(a.id)] = stopper{ctx: ctx, stop: stop}
	queue := &chat{wake: make(chan struct{}, 1)}
	e.chats[leadChat("owner/shop", 12)] = queue
	return a, ctx, queue
}

func TestAResearcherThatStartsAfterTheShutdownIsDeclined(t *testing.T) {
	e := researchEngine(t)
	e.stopWorkers()

	_, err := e.startResearcher(t.Context(), leadCaller, shopRepository, questionInput{Question: "Where do plans store the price?"})

	if want := "Mobius stops, so no Researcher starts now."; err == nil || err.Error() != want {
		t.Errorf("error = %v", err)
	}
	if len(e.researchers) != 0 {
		t.Errorf("researchers = %+v", e.researchers)
	}
	session, err := e.queries.GetSession(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !session.EndedAt.Valid || session.EndReason.String != "declined" {
		t.Errorf("session = %+v", session)
	}
}

func TestAStopBeforeTheReportMakesTheResearcherLeaveStopped(t *testing.T) {
	e := researchEngine(t)
	a, ctx, queue := runningResearcher(t, e)

	if _, err := e.stopResearcher(t.Context(), leadCaller, shopRepository, researcherInput{ID: a.id}); err != nil {
		t.Fatal(err)
	}

	if !e.leave(ctx, a.id) {
		t.Error("the Researcher did not see the stop")
	}
	want := fmt.Sprintf("The Researcher %d stopped. No report arrives.", a.id)
	if len(queue.queue) != 1 || queue.queue[0].message.Text != want {
		t.Errorf("queue = %+v", queue.queue)
	}
}

func TestAReportBeforeTheStopRefusesTheStop(t *testing.T) {
	e := researchEngine(t)
	a, ctx, queue := runningResearcher(t, e)

	if e.leave(ctx, a.id) {
		t.Error("the Researcher saw a stop")
	}
	_, err := e.stopResearcher(t.Context(), leadCaller, shopRepository, researcherInput{ID: a.id})
	if want := fmt.Sprintf("No Researcher %d of this Workstream runs now.", a.id); err == nil || err.Error() != want {
		t.Errorf("error = %v", err)
	}
	if ctx.Err() != nil {
		t.Error("the stop reached the Researcher")
	}
	if err := e.deliverReport(t.Context(), leadCaller, a.id, "Where?", "In cents."); err != nil {
		t.Fatal(err)
	}
	if len(queue.queue) != 1 || !strings.Contains(queue.queue[0].message.Text, "Report of the Researcher") {
		t.Errorf("queue = %+v", queue.queue)
	}
}
