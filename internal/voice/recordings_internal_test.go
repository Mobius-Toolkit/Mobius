package voice

import (
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestARecordingWithNoChunkEndsAfterTheIdleTimeout(t *testing.T) {
	r := NewRecordings(New(t.TempDir(), ""), NewDraft(t.TempDir(), ""))
	r.IdleTimeout = 50 * time.Millisecond

	if _, err := r.Create(); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.recordings) == 0
	})
}
