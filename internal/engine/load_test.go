package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// fakeLoad is the load average of the machine in the tests, in hundredths, and the count of its reads.
type fakeLoad struct {
	hundredths atomic.Int64
	reads      atomic.Int64
}

func (f *fakeLoad) set(load float64) {
	f.hundredths.Store(int64(load * 100))
}

func (f *fakeLoad) average() (float64, error) {
	f.reads.Add(1)
	return float64(f.hundredths.Load()) / 100, nil
}

// installLoad replaces the load average of the machine with a fake one until the end of the test.
func installLoad(t *testing.T, load float64) *fakeLoad {
	t.Helper()
	fake := &fakeLoad{}
	fake.set(load)
	before := *engine.LoadAverage
	*engine.LoadAverage = fake.average
	t.Cleanup(func() { *engine.LoadAverage = before })
	return fake
}

func highLoad() float64 {
	return float64(engine.Cores) + 1.5
}

// checkTexts gives the texts of the check rows of the session.
func checkTexts(t *testing.T, server *testserver.Server, session store.Session) []string {
	t.Helper()
	var texts []string
	for _, row := range rows(t, server, session.ID, "check") {
		texts = append(texts, row["text"].(string))
	}
	return texts
}

func TestALowLoadStartsTheCheckAtOnce(t *testing.T) {
	installLoad(t, 0)
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	session := endedImplementers(t, server, 1)[0]
	texts := checkTexts(t, server, session)
	if len(texts) != 2 || texts[0] != ".mobius/check started." {
		t.Errorf("check rows = %q", texts)
	}
}

func TestAHighLoadMakesTheCheckWaitUntilTheLoadIsLow(t *testing.T) {
	load := installLoad(t, highLoad())
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool {
		sessions := roleSessions(t, server, engine.ImplementerRole)
		return len(sessions) == 1 && sessions[0].QueueReason.String == "waits for a low load"
	})
	load.set(0)

	session := endedImplementers(t, server, 1)[0]
	want := fmt.Sprintf("The check waits for a low load. The 1-minute load average is %.2f, and the machine has %d cores.", highLoad(), engine.Cores)
	texts := checkTexts(t, server, session)
	if len(texts) != 3 || texts[0] != want || texts[1] != ".mobius/check started." || session.QueueReason.Valid {
		t.Errorf("check rows = %q, session = %+v", texts, session)
	}
}

func TestACheckThatStartsLessThanTheGapAfterTheLastStartWaitsForTheGap(t *testing.T) {
	testkit.Slow(t)
	installLoad(t, 0)
	before := *engine.CheckGap
	*engine.CheckGap = 3 * time.Second
	t.Cleanup(func() { *engine.CheckGap = before })
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStartsTwo, commits, noChange)
	log := filepath.Join(dataDir, "starts.log")
	fake.SetCheck(shop, fmt.Sprintf("date +%%s >> '%s'", log))

	dispatchTwo(fake)

	endedImplementers(t, server, 2)
	text, err := os.ReadFile(filepath.Clean(log))
	if err != nil {
		t.Fatal(err)
	}
	starts := strings.Fields(string(text))
	if len(starts) != 2 {
		t.Fatalf("starts = %q", starts)
	}
	first, _ := strconv.Atoi(starts[0])
	second, _ := strconv.Atoi(starts[1])
	if second-first < 2 {
		t.Errorf("starts = %q", starts)
	}
}

func TestALongWaitForALowLoadGivesTheLeadOneEvent(t *testing.T) {
	load := installLoad(t, highLoad())
	before := *engine.LoadWaitEvent
	*engine.LoadWaitEvent = 100 * time.Millisecond
	t.Cleanup(func() { *engine.LoadWaitEvent = before })
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadEvents(t, server), func(event leadEvent) bool { return event.Kind == "load_wait" })
	})
	reads := load.reads.Load()
	testkit.WaitFor(t, func() bool { return load.reads.Load() >= reads+20 })
	load.set(0)

	endedImplementers(t, server, 1)
	var payloads []string
	for _, event := range leadEvents(t, server) {
		if event.Kind == "load_wait" {
			payloads = append(payloads, event.Payload)
		}
	}
	if len(payloads) != 1 {
		t.Fatalf("payloads = %q", payloads)
	}
	for _, want := range []string{
		`long wait for a low load of #41 "Add plan model": the .mobius/check waited 0 minutes.`,
		fmt.Sprintf("The 1-minute load average is %.2f, and the machine has %d cores.", highLoad(), engine.Cores),
	} {
		if !strings.Contains(payloads[0], want) {
			t.Errorf("payload = %q, want %q", payloads[0], want)
		}
	}
}
