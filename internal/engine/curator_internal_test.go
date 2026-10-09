package engine

import "testing"

func TestACuratorThatStartsAfterTheShutdownAddsNoSession(t *testing.T) {
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
