package engine_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

var releaseAssets = []string{"mobius-aarch64-apple-darwin.tar.gz", "mobius-x86_64-unknown-linux-gnu.tar.gz"}

const (
	releasesLatest = "GET /repos/{owner}/{repo}/releases/latest"
	releaseStorage = "GET /release-storage/{id}"
)

// upgradeState is the state of the upgrade that the API gives.
type upgradeState struct {
	Step    string     `json:"step"`
	Tries   int        `json:"tries"`
	Failure string     `json:"failure"`
	NextTry *time.Time `json:"nextTry"`
}

// requestUpgrade sends the request to the upgrade route of the API, and gives the status, the state and the error text
// of the response.
func requestUpgrade(t *testing.T, server *testserver.Server, method string) (int, upgradeState, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, server.URL+"/api/upgrade", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body struct {
		Data  upgradeState `json:"data"`
		Error string       `json:"error"`
	}
	if response.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
	}
	return response.StatusCode, body.Data, body.Error
}

func startUpgrade(t *testing.T, server *testserver.Server) upgradeState {
	t.Helper()
	status, state, text := requestUpgrade(t, server, http.MethodPost)
	if status != http.StatusOK {
		t.Fatalf("start of the upgrade = %d %q", status, text)
	}
	return state
}

func cancelUpgrade(t *testing.T, server *testserver.Server) {
	t.Helper()
	if status, _, text := requestUpgrade(t, server, http.MethodDelete); status != http.StatusNoContent {
		t.Fatalf("cancel of the upgrade = %d %q", status, text)
	}
}

func upgradeStatus(t *testing.T, server *testserver.Server) upgradeState {
	t.Helper()
	status, state, text := requestUpgrade(t, server, http.MethodGet)
	if status != http.StatusOK {
		t.Fatalf("upgrade = %d %q", status, text)
	}
	return state
}

// waitForUpgrade waits until the state of the upgrade matches, and gives that state.
func waitForUpgrade(t *testing.T, server *testserver.Server, matches func(upgradeState) bool) upgradeState {
	t.Helper()
	var state upgradeState
	testkit.WaitFor(t, func() bool {
		state = upgradeStatus(t, server)
		return matches(state)
	})
	return state
}

func inRetry(tries int) func(upgradeState) bool {
	return func(state upgradeState) bool { return state.Step == "retry" && state.Tries == tries }
}

// requireIdle checks that the upgrade has ended with the failure and with no drain.
func requireIdle(t *testing.T, server *testserver.Server, failure string) {
	t.Helper()
	state := waitForUpgrade(t, server, func(state upgradeState) bool { return state.Step == "idle" })
	if state.Failure != failure || state.NextTry != nil {
		t.Errorf("state = %+v, want the failure %q and no next try", state, failure)
	}
	if got := server.Engine.Draining(); got.On {
		t.Errorf("drain = %+v", got)
	}
}

// fixedClock is a clock of the engine that only Advance moves.
type fixedClock struct{ nanoseconds atomic.Int64 }

func fixClock(server *testserver.Server) *fixedClock {
	clock := &fixedClock{}
	clock.nanoseconds.Store(time.Date(2026, time.November, 8, 10, 0, 0, 0, time.UTC).UnixNano())
	server.Engine.SetClock(clock.Now)
	return clock
}

func (c *fixedClock) Now() time.Time { return time.Unix(0, c.nanoseconds.Load()).UTC() }

func (c *fixedClock) Advance(d time.Duration) { c.nanoseconds.Add(int64(d)) }

// upgradeSetup gives a server with a release build, the program that the upgrade replaces, and a Lead that holds the
// drain, so an upgrade that passes all its steps waits in the step drain and never restarts the test program.
func upgradeSetup(t *testing.T) (*testkit.FakeGitHub, *testserver.Server, *fixedClock, *engine.Agent, string) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	engine.Release = "v0.1.0"
	t.Cleanup(func() { engine.Release = "" })
	program := filepath.Join(t.TempDir(), "mobius")
	if err := os.WriteFile(program, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine.UseProgram(t, program)
	return fake, server, fixClock(server), start(t, server, leadSpec(t)), program
}

func TestAnUpgradeOfALocalBuildEndsWithNoRetry(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	fake.SetLatestRelease("v0.3.0")

	if state := startUpgrade(t, server); state.Step != "release" {
		t.Errorf("state of the start = %+v", state)
	}

	requireIdle(t, server, "This Mobius build is not a release.")
	if state := upgradeStatus(t, server); state.Tries != 0 {
		t.Errorf("state = %+v", state)
	}
}

func TestAnUpgradeToTheNewestReleaseEndsWithNoRetryAndNoDrain(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	fake.SetLatestRelease("v0.2.0", releaseAssets...)
	engine.Release = "v0.2.0"
	defer func() { engine.Release = "" }()

	startUpgrade(t, server)

	requireIdle(t, server, "v0.2.0 is the newest release.")
}

func TestAnUpgradeStartedTwiceGivesTheStateAtOnceAndRefusesTheSecondStart(t *testing.T) {
	fake, server, _, lead, _ := upgradeSetup(t)
	defer end(t, lead, "idle")
	fake.SetLatestRelease("v0.3.0", releaseAssets...)
	hung := fake.HangNext(releasesLatest)

	if state := startUpgrade(t, server); state.Step != "release" || state.Tries != 0 || state.Failure != "" || state.NextTry != nil {
		t.Errorf("state of the start = %+v", state)
	}
	<-hung
	status, _, text := requestUpgrade(t, server, http.MethodPost)

	if status != http.StatusConflict || text != "An upgrade runs now." {
		t.Errorf("second start = %d %q", status, text)
	}
	cancelUpgrade(t, server)
}

// retryCases are the failures that can pass, each with the change that lets the next try pass.
var retryCases = []struct {
	name    string
	fail    func(*testkit.FakeGitHub)
	failure string
	pass    func(*testkit.FakeGitHub)
}{
	{
		"a release read error",
		func(*testkit.FakeGitHub) {},
		"404",
		func(fake *testkit.FakeGitHub) { fake.SetLatestRelease("v0.3.0", releaseAssets...) },
	},
	{
		"a release with no asset",
		func(fake *testkit.FakeGitHub) { fake.SetLatestRelease("v0.3.0", "mobius-other.tar.gz") },
		"Release v0.3.0 has no mobius-",
		func(fake *testkit.FakeGitHub) { fake.SetLatestRelease("v0.3.0", releaseAssets...) },
	},
	{
		"a download error",
		func(fake *testkit.FakeGitHub) {
			fake.SetLatestRelease("v0.3.0", releaseAssets...)
			fake.FailReleaseAssets(http.StatusGatewayTimeout)
		},
		"download of mobius-",
		func(fake *testkit.FakeGitHub) { fake.FailReleaseAssets(0) },
	},
	{
		"an archive with no program",
		func(fake *testkit.FakeGitHub) {
			fake.SetLatestRelease("v0.3.0", releaseAssets...)
			fake.SetReleaseHasNoProgram(true)
		},
		"has no mobius program.",
		func(fake *testkit.FakeGitHub) { fake.SetReleaseHasNoProgram(false) },
	},
}

func TestAnUpgradeTriesAgainAfterEachFailureThatCanPass(t *testing.T) {
	for _, c := range retryCases {
		t.Run(c.name, func(t *testing.T) {
			fake, server, clock, lead, _ := upgradeSetup(t)
			defer end(t, lead, "idle")
			c.fail(fake)
			start := clock.Now()

			startUpgrade(t, server)

			state := waitForUpgrade(t, server, inRetry(1))
			if !strings.Contains(state.Failure, c.failure) || state.NextTry == nil || !state.NextTry.Equal(start.Add(10*time.Second)) {
				t.Fatalf("state = %+v, want the failure %q and the next try in 10 s", state, c.failure)
			}
			c.pass(fake)
			clock.Advance(9 * time.Second)
			if got := upgradeStatus(t, server); !reflect.DeepEqual(got, state) {
				t.Errorf("state before the next try = %+v, want %+v", got, state)
			}
			clock.Advance(time.Second)

			passed := waitForUpgrade(t, server, func(state upgradeState) bool { return state.Step == "drain" })
			if passed.Tries != 1 || passed.Failure != state.Failure || passed.NextTry != nil {
				t.Errorf("state of the drain = %+v", passed)
			}
			cancelUpgrade(t, server)
			requireIdle(t, server, "")
		})
	}
}

func TestTheWaitBeforeATryDoublesAfterEachFailureAndStopsAtFiveMinutes(t *testing.T) {
	_, server, clock, lead, _ := upgradeSetup(t)
	defer end(t, lead, "idle")
	startUpgrade(t, server)

	for i, wait := range []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute} {
		want := clock.Now().Add(wait)
		state := waitForUpgrade(t, server, inRetry(i+1))
		if state.NextTry == nil || !state.NextTry.Equal(want) {
			t.Fatalf("try %d: next try = %v, want %v", i+1, state.NextTry, want)
		}
		clock.Advance(wait)
	}

	cancelUpgrade(t, server)
}

func TestAnUpgradeEndsWithNoRetryWhenTheSwapFails(t *testing.T) {
	fake, server, _, lead, program := upgradeSetup(t)
	fake.SetLatestRelease("v0.3.0", releaseAssets...)
	startUpgrade(t, server)
	waitForUpgrade(t, server, func(state upgradeState) bool { return state.Step == "drain" })
	if err := os.Remove(program); err != nil {
		t.Fatal(err)
	}

	end(t, lead, "idle")

	state := waitForUpgrade(t, server, func(state upgradeState) bool { return state.Step == "idle" })
	if !strings.Contains(state.Failure, "rename") || state.Tries != 0 || state.NextTry != nil {
		t.Errorf("state = %+v", state)
	}
	if got := server.Engine.Draining(); got.On {
		t.Errorf("drain = %+v", got)
	}
}

func TestACancelEndsTheUpgradeInEachStep(t *testing.T) {
	for _, c := range []struct {
		step  string
		setup func(*testkit.FakeGitHub) <-chan struct{}
	}{
		{"release", func(fake *testkit.FakeGitHub) <-chan struct{} { return fake.HangNext(releasesLatest) }},
		{"download", func(fake *testkit.FakeGitHub) <-chan struct{} {
			fake.SetLatestRelease("v0.3.0", releaseAssets...)
			return fake.HangNext(releaseStorage)
		}},
		{"retry", func(*testkit.FakeGitHub) <-chan struct{} { return nil }},
		{"drain", func(fake *testkit.FakeGitHub) <-chan struct{} {
			fake.SetLatestRelease("v0.3.0", releaseAssets...)
			return nil
		}},
	} {
		t.Run(c.step, func(t *testing.T) {
			fake, server, _, lead, _ := upgradeSetup(t)
			defer end(t, lead, "idle")
			hung := c.setup(fake)
			startUpgrade(t, server)
			if hung != nil {
				<-hung
			}
			waitForUpgrade(t, server, func(state upgradeState) bool { return state.Step == c.step })

			cancelUpgrade(t, server)

			requireIdle(t, server, "")
		})
	}
}

func TestACancelDuringTheWaitBeforeATryClearsTheFailure(t *testing.T) {
	_, server, _, lead, _ := upgradeSetup(t)
	defer end(t, lead, "idle")
	startUpgrade(t, server)
	if state := waitForUpgrade(t, server, inRetry(1)); state.Failure == "" {
		t.Fatalf("state = %+v", state)
	}

	cancelUpgrade(t, server)

	requireIdle(t, server, "")
}

func TestACancelOfTheDrainEndsTheUpgrade(t *testing.T) {
	fake, server, _, lead, _ := upgradeSetup(t)
	defer end(t, lead, "idle")
	fake.SetLatestRelease("v0.3.0", releaseAssets...)
	startUpgrade(t, server)
	waitForUpgrade(t, server, func(state upgradeState) bool { return state.Step == "drain" })

	cancelDrain(t, server)

	requireIdle(t, server, "")
}

func TestACancelWithNoUpgradeEndsTheDrain(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	states := drainEvents(t, server)
	ends := startDrain(t, server)
	waitForDrain(t, states, drainState{true, 0})

	cancelUpgrade(t, server)

	if got := <-ends; got != "drained" && got != "cancelled" {
		t.Errorf("drain end = %s", got)
	}
	waitForDrain(t, states, drainState{false, 0})
	if state := upgradeStatus(t, server); state.Step != "idle" {
		t.Errorf("state = %+v", state)
	}
}

func TestTheLiveEventUpgradeSendsEachChangeOfTheState(t *testing.T) {
	fake, server, clock, lead, _ := upgradeSetup(t)
	defer end(t, lead, "idle")
	states := upgradeEvents(t, server)
	start := clock.Now()
	startUpgrade(t, server)
	waitForUpgrade(t, server, inRetry(1))
	fake.SetLatestRelease("v0.3.0", releaseAssets...)
	clock.Advance(10 * time.Second)
	waitForUpgrade(t, server, func(state upgradeState) bool { return state.Step == "drain" })
	cancelUpgrade(t, server)

	var seen []string
	deadline := time.After(time.Minute)
	for {
		var state upgradeState
		select {
		case state = <-states:
		case <-deadline:
			t.Fatalf("no event of the step idle after one minute, steps = %q", seen)
		}
		seen = append(seen, state.Step)
		switch {
		case state.Step == "retry" && (state.Tries != 1 || state.NextTry == nil || !state.NextTry.Equal(start.Add(10*time.Second)) || !strings.Contains(state.Failure, "404")):
			t.Errorf("event of the step retry = %+v", state)
		case state.Step == "idle":
			if state.Failure != "" {
				t.Errorf("event of the step idle = %+v", state)
			}
			if want := []string{"release", "retry", "release", "download", "drain", "idle"}; !reflect.DeepEqual(seen, want) {
				t.Errorf("steps = %q, want %q", seen, want)
			}
			return
		}
	}
}

// upgradeEvents gives the data of each upgrade event of the live event stream.
func upgradeEvents(t *testing.T, server *testserver.Server) <-chan upgradeState {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	states := make(chan upgradeState, 100)
	go func() {
		events := bufio.NewReader(response.Body)
		for {
			name, data, err := nextEvent(events)
			if err != nil {
				return
			}
			var state upgradeState
			if name == "upgrade" && json.Unmarshal([]byte(data), &state) == nil {
				states <- state
			}
		}
	}()
	return states
}

// getData sends a GET request to path of the API, and decodes the data of the response into data. It gives the status and
// the error text of the response.
func getData(t *testing.T, server *testserver.Server, path string, data any) (int, string) {
	t.Helper()
	response, err := server.Client.Get(server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body := struct {
		Data  any    `json:"data"`
		Error string `json:"error"`
	}{Data: data}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body.Error
}

func newRelease(t *testing.T, server *testserver.Server) string {
	t.Helper()
	var release struct {
		Version string `json:"version"`
	}
	if status, text := getData(t, server, "/api/release", &release); status != http.StatusOK {
		t.Fatalf("release = %d %q", status, text)
	}
	return release.Version
}

func TestTheReleaseIsTheNewestReleaseWhenItIsNewerThanThisProgram(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	fake.SetLatestRelease("v0.2.0")
	defer func() { engine.Release = "" }()

	for _, release := range []struct{ current, want string }{{"", ""}, {"v0.1.4", "v0.2.0"}, {"v0.2.0", ""}} {
		engine.Release = release.current
		if got := newRelease(t, server); got != release.want {
			t.Errorf("release of %q = %q, want %q", release.current, got, release.want)
		}
	}
}

func TestTheReleaseChangesGiveTheFirstLineOfEachCommitTheNewestFirst(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	fake.SetLatestRelease("v0.2.0")
	fake.SetComparedCommits(
		"Check for a new release each hour (#316)\n\nThe check runs once each hour.",
		"Show the release changes in a modal before the upgrade (#320)",
	)

	var changes []string
	if status, text := getData(t, server, "/api/release/changes", &changes); status != http.StatusConflict || text != "This Mobius build is not a release." {
		t.Errorf("changes of a local build = %d %q", status, text)
	}
	engine.Release = "v0.1.4"
	defer func() { engine.Release = "" }()
	if status, text := getData(t, server, "/api/release/changes", &changes); status != http.StatusOK {
		t.Fatalf("changes = %d %q", status, text)
	}

	want := []string{"Show the release changes in a modal before the upgrade (#320)", "Check for a new release each hour (#316)"}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("changes = %q", changes)
	}
}

func TestTheReleaseNeedsTheTokenOfTheAppWhenGitHubLimitsTheCallsWithNoToken(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.SetAnonymousRateLimited(true)
	server, _ := connect(t, fake, "")
	fake.SetLatestRelease("v0.2.0")
	engine.Release = "v0.1.4"
	defer func() { engine.Release = "" }()

	if got := newRelease(t, server); got != "v0.2.0" {
		t.Errorf("release = %q", got)
	}
}

func TestTheReleaseChangesNeedTheTokenOfTheAppWhenGitHubLimitsTheCallsWithNoToken(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.SetAnonymousRateLimited(true)
	server, _ := connect(t, fake, "")
	fake.SetLatestRelease("v0.2.0")
	fake.SetComparedCommits("Check for a new release each hour (#316)")
	engine.Release = "v0.1.4"
	defer func() { engine.Release = "" }()

	var changes []string
	if status, text := getData(t, server, "/api/release/changes", &changes); status != http.StatusOK {
		t.Fatalf("changes = %d %q", status, text)
	}

	if want := []string{"Check for a new release each hour (#316)"}; !reflect.DeepEqual(changes, want) {
		t.Errorf("changes = %q", changes)
	}
}

func TestTheReleaseOfAServerWithNoAppNeedsNoToken(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server := testserver.Start(t, t.TempDir(), fake.URL)
	fake.SetLatestRelease("v0.2.0")
	engine.Release = "v0.1.4"
	defer func() { engine.Release = "" }()

	if got := newRelease(t, server); got != "v0.2.0" {
		t.Errorf("release = %q", got)
	}
}
