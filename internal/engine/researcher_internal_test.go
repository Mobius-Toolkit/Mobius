package engine

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
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

func TestAReportBeforeTheStopRefusesTheStop(t *testing.T) {
	e := researchEngine(t)
	gh, err := github.New(e.queries, "http://127.0.0.1", "http://127.0.0.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.github = gh
	a, err := e.newAgent(t.Context(), Spec{Role: ResearcherRole, Organization: "owner", Repository: "owner/shop", Workstream: 12})
	if err != nil {
		t.Fatal(err)
	}
	e.researchers[a.id] = a
	queue := &chat{wake: make(chan struct{}, 1)}
	e.chats[leadChat("owner/shop", 12)] = queue
	e.gitMu.Lock()
	finished := make(chan error, 1)
	go func() { finished <- e.research(t.Context(), leadCaller, shopRepository, a, "Where?") }()
	testkit.WaitFor(t, func() bool {
		e.detailsMu.Lock()
		defer e.detailsMu.Unlock()
		return len(e.researchers) == 0
	})

	_, err = e.stopResearcher(t.Context(), leadCaller, shopRepository, researcherInput{ID: a.id})

	if want := fmt.Sprintf("No Researcher %d of this Workstream runs now.", a.id); err == nil || err.Error() != want {
		t.Errorf("error = %v", err)
	}
	e.gitMu.Unlock()
	if err := <-finished; err == nil || err.Error() != "The Mobius App has no access to owner/shop." {
		t.Errorf("research error = %v", err)
	}
	session, err := e.queries.GetSession(t.Context(), a.id)
	if err != nil {
		t.Fatal(err)
	}
	if session.EndReason.String != "failed" {
		t.Errorf("end reason = %s", session.EndReason.String)
	}
	if len(queue.queue) != 1 || !strings.Contains(queue.queue[0].message.Text, "Report of the Researcher") {
		t.Errorf("queue = %+v", queue.queue)
	}
}
