package engine

import (
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

func TestACuratorThatStartsAfterTheShutdownAddsNoSession(t *testing.T) {
	t.Parallel()
	e := researchEngine(t)
	e.stopWorkers()

	err := e.startCurator(t.Context(), "owner/shop")

	if want := "Mobius stops, so no Curator starts now."; err == nil || err.Error() != want {
		t.Errorf("error = %v", err)
	}
	if len(e.curators) != 0 {
		t.Errorf("curators = %+v", e.curators)
	}
	if _, err := e.queries.GetSession(t.Context(), 1); err == nil {
		t.Error("the session of the Curator exists")
	}
}

func TestARequestStaysInTheStoreWhenTheDrainIsSealedAtTheDeliveryOfTheResult(t *testing.T) {
	t.Parallel()
	e := researchEngine(t)
	if _, err := e.queries.AddCuratorRequest(t.Context(), store.AddCuratorRequestParams{Repository: "owner/shop", Text: "Add the lesson."}); err != nil {
		t.Fatal(err)
	}
	requests, err := e.queries.ListCuratorRequests(t.Context(), "owner/shop")
	if err != nil {
		t.Fatal(err)
	}
	e.drain.sealed = true
	a := &Agent{id: 1, spec: Spec{Role: CuratorRole, Organization: "owner", Repository: "owner/shop"}}

	err = e.answerRequests(t.Context(), a, requests, 0, "")

	if err == nil {
		t.Error("no error")
	}
	if kept, err := e.queries.ListCuratorRequests(t.Context(), "owner/shop"); err != nil || len(kept) != 1 {
		t.Errorf("requests = %+v, error = %v", kept, err)
	}
}
