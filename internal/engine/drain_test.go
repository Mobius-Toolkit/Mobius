package engine_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const drainReason = "Mobius prepares an upgrade"

type drainState struct {
	On      bool  `json:"on"`
	Waiting int64 `json:"waiting"`
}

// drainEvents gives the data of each drain event of the live event stream.
func drainEvents(t *testing.T, server *testserver.Server) <-chan drainState {
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
	states := make(chan drainState, 100)
	go func() {
		events := bufio.NewReader(response.Body)
		for {
			name, data, err := nextEvent(events)
			if err != nil {
				return
			}
			var state drainState
			if name == "drain" && json.Unmarshal([]byte(data), &state) == nil {
				states <- state
			}
		}
	}()
	return states
}

// startDrain starts the drain through the API, and gives the end of the drain when it comes.
func startDrain(t *testing.T, server *testserver.Server) <-chan string {
	t.Helper()
	ends := make(chan string, 1)
	go func() {
		response, err := server.Client.Post(server.URL+"/api/drain", "application/json", nil)
		if err != nil {
			ends <- err.Error()
			return
		}
		defer func() { _ = response.Body.Close() }()
		var body struct {
			Data struct {
				End string `json:"end"`
			} `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			ends <- err.Error()
			return
		}
		ends <- body.Data.End
	}()
	return ends
}

func cancelDrain(t *testing.T, server *testserver.Server) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, server.URL+"/api/drain", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel: status %d", response.StatusCode)
	}
}

// waitForDrain waits for a drain event with state. The test fails after one minute.
func waitForDrain(t *testing.T, states <-chan drainState, state drainState) {
	t.Helper()
	var seen []drainState
	deadline := time.After(time.Minute)
	for {
		select {
		case got := <-states:
			if got == state {
				return
			}
			seen = append(seen, got)
		case <-deadline:
			t.Fatalf("no drain event %+v after one minute, only %+v", state, seen)
		}
	}
}

func TestTheDrainHoldsNewWorkersWaitsForTheRunningSessionsAndACancelReleasesThem(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "[[prompts]]\nhang = true\n", func(cfg *config.Config) { cfg.MaxAgents = 1 })
	states := drainEvents(t, server)
	implementer := start(t, server, implementerSpec(t, server, fake, 41))
	go func() { _ = implementer.Prompt(t.Context(), "Store plans in cents.") }()
	held := startLater(t.Context(), server, implementerSpec(t, server, fake, 43))
	waiting := queued(t, server, engine.ImplementerRole)
	lead := start(t, server, leadSpec(t))

	ends := startDrain(t, server)

	waitForDrain(t, states, drainState{true, 2})
	testkit.WaitFor(t, func() bool { return session(t, server, waiting.ID).QueueReason.String == drainReason })
	end(t, implementer, "stopped")
	waitForDrain(t, states, drainState{true, 1})
	// The free slot does not go to the held Worker.
	if got := session(t, server, waiting.ID); got.QueueReason.String != drainReason || got.AcpSessionID.Valid {
		t.Errorf("held Worker = %+v", got)
	}
	end(t, lead, "idle")
	if got := <-ends; got != "drained" {
		t.Fatalf("drain end = %s", got)
	}
	waitForDrain(t, states, drainState{true, 0})
	if got := server.Engine.Draining(); got != (engine.DrainState{On: true}) {
		t.Errorf("drain = %+v", got)
	}

	cancelDrain(t, server)

	waitForDrain(t, states, drainState{false, 0})
	agent := await(t, held)
	defer end(t, agent, "done")
	if got := session(t, server, agent.ID()); got.QueueReason.Valid {
		t.Errorf("released Worker = %+v", got)
	}
}

func TestACancelEndsTheWaitOfTheDrain(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	states := drainEvents(t, server)
	lead := start(t, server, leadSpec(t))
	defer end(t, lead, "idle")
	ends := startDrain(t, server)
	waitForDrain(t, states, drainState{true, 1})

	cancelDrain(t, server)

	if got := <-ends; got != "cancelled" {
		t.Errorf("drain end = %s", got)
	}
}

func TestTheDrainClosesTheLeadAndHoldsTheEventsAndTheTriagersUntilACancel(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, `
[[prompts]]
when = "Plan the loyalty API"
reply = ["Hello"]

[[prompts]]
when = "You are the Triager"
reply = ["A proposal."]
`, keepSessionOpen)
	dispatchTask(fake, 41, "Add plan model")
	testkit.WaitFor(t, func() bool { return eventDelivered(t, server) })
	sendChat(t, server, leadChat, "Plan the loyalty API")
	waitForChat(t, server, leadChat, "Lead", "Hello")

	if end := <-startDrain(t, server); end != "drained" {
		t.Fatalf("end = %s", end)
	}

	if !slices.Contains(leadPrompts(t, server), "Save in the Workstream memory what the next session needs.") {
		t.Errorf("prompts = %q", leadPrompts(t, server))
	}
	sessions := chatSessions(t, server, leadChat, engine.LeadRole)
	if len(sessions) != 1 || !sessions[0].EndedAt.Valid {
		t.Fatalf("sessions = %+v", sessions)
	}
	// A comment on the task stays an undelivered event, and the Triager does not start: the issue keeps mobius:ready.
	fake.AddComment(shop, 41, "owner", "One more thing.")
	fake.AddIssue(shop, 50, "Change request")
	fake.AddLabel(shop, 50, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool { return len(undelivered(t, server)) == 1 })
	waitForPolls(t, fake)
	if got := chatSessions(t, server, leadChat, engine.LeadRole); len(got) != 1 {
		t.Errorf("sessions = %+v", got)
	}
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 0 || !slices.Equal(fake.Labels(shop, 50), []string{"mobius:ready"}) {
		t.Errorf("Triagers = %+v, labels = %v", got, fake.Labels(shop, 50))
	}

	cancelDrain(t, server)

	testkit.WaitFor(t, func() bool {
		return slices.ContainsFunc(leadPrompts(t, server), func(prompt string) bool { return strings.Contains(prompt, "One more thing.") })
	})
	testkit.WaitFor(t, func() bool { return len(chatSessions(t, server, issueTriagers, engine.TriagerRole)) > 0 })
}

func TestATriagerThatTheDrainHeldDuringAPollStartsInThePollAfterACancel(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nwhen = \"You are the Triager\"\nreply = [\"A proposal.\"]\n")
	fake.AddIssue(shop, 50, "Change request")
	reached, release := fake.HoldIssueEvents(shop, 50)
	fake.AddLabel(shop, 50, "mobius:ready", "owner")

	// The poll read the ready list and waits for the events of the issue.
	select {
	case <-reached:
	case <-time.After(time.Minute):
		t.Fatal("the poll did not read the events after one minute")
	}
	ends := startDrain(t, server)
	testkit.WaitFor(t, func() bool { return server.Engine.Draining().On })
	release()

	waitForPolls(t, fake)
	if got := chatSessions(t, server, issueTriagers, engine.TriagerRole); len(got) != 0 || !slices.Equal(fake.Labels(shop, 50), []string{"mobius:ready"}) {
		t.Errorf("Triagers = %+v, labels = %v", got, fake.Labels(shop, 50))
	}
	if end := <-ends; end != "drained" {
		t.Fatalf("end = %s", end)
	}

	cancelDrain(t, server)

	testkit.WaitFor(t, func() bool { return len(chatSessions(t, server, issueTriagers, engine.TriagerRole)) > 0 })
}

func TestTheDrainHoldsTheReviewerAndTheJudgeUntilACancel(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	dataDir := t.TempDir()
	goFile := filepath.Join(dataDir, "go")
	server, _ := connectTaskIn(t, fake, dataDir, "[[prompts]]\nwhen = \"You are the Judge\"\nreply = [\"Judged.\"]\n\n"+leadStarts, commits, func(cfg *config.Config) {
		cfg.ReviewQuietPeriod = 50 * time.Millisecond
	})
	fake.SetCheck(shop, fmt.Sprintf("while [ ! -e '%s' ]; do sleep 0.05; done", goFile))
	fake.AddLabel(shop, 41, "mobius:ready", "owner")
	testkit.WaitFor(t, func() bool {
		implementers := roleSessions(t, server, engine.ImplementerRole)
		return len(implementers) == 1 && implementers[0].QueueReason.String == "runs .mobius/check"
	})

	ends := startDrain(t, server)

	// The running Implementer ends its work, and the Reviewer waits.
	touch(t, goFile)
	testkit.WaitFor(t, func() bool {
		reviewers := roleSessions(t, server, engine.ReviewerRole)
		return len(reviewers) == 1 && reviewers[0].QueueReason.String == drainReason
	})
	if end := <-ends; end != "drained" {
		t.Fatalf("drain end = %s", end)
	}
	// A new comment on the pull request of a reviewed task does not start the Judge.
	if _, err := server.DB.Exec("UPDATE tasks SET state = 'reviewed' WHERE issue = 41 AND state = 'queued'"); err != nil {
		t.Fatal(err)
	}
	fake.AddComment(shop, 42, "owner", "Rename the field.")
	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if judges := roleSessions(t, server, engine.JudgeRole); len(judges) != 0 {
		t.Errorf("Judges = %+v", judges)
	}

	cancelDrain(t, server)

	testkit.WaitFor(t, func() bool { return len(roleSessions(t, server, engine.JudgeRole)) > 0 })
}

// A Judge whose Worker starts after the start of the drain stays held, and does not fail.
func TestTheDrainHoldsAJudgeThatAPollStartedBeforeTheDrain(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, func(cfg *config.Config) { cfg.ReviewQuietPeriod = 0 })
	waitForApproval(t, server, fake)
	reached, release := fake.HoldReviewThreads(shop, 42)
	// The poll reads the items of the Judge and waits for the review threads.
	select {
	case <-reached:
	case <-time.After(time.Minute):
		t.Fatal("the poll did not read the review threads after one minute")
	}
	fake.AddComment(shop, 42, "owner", "Why cents?")
	startDrain(t, server)
	testkit.WaitFor(t, func() bool { return server.Engine.Draining().On })

	release()

	waitForPolls(t, fake)
	if judges := roleSessions(t, server, engine.JudgeRole); len(judges) != 0 {
		t.Errorf("Judges = %+v", judges)
	}
	if state := taskState(t, server); state != "approval" {
		t.Errorf("state = %s", state)
	}

	cancelDrain(t, server)

	testkit.WaitFor(t, func() bool { return len(roleSessions(t, server, engine.JudgeRole)) > 0 })
}

// The first prompt hits the usage limit of Devin, and the same prompt again hangs.
const usageLimitThenHang = `
[[prompts]]
error = { code = -32011, message = "Rate limited", data = { retryAfterSeconds = 3600 } }

[[prompts]]
hang = true
`

func devinLead(cfg *config.Config) {
	keepSessionOpen(cfg)
	cfg.Roles.Lead.Harness = config.Devin
}

func hasPausedReason(session store.Session) bool {
	return strings.HasPrefix(session.QueueReason.String, "paused until ")
}

// startPausedImplementer starts an Implementer whose prompt hit the usage limit, and waits until it waits for the pause.
func startPausedImplementer(t *testing.T, server *testserver.Server, fake *testkit.FakeGitHub) *engine.Agent {
	t.Helper()
	agent := start(t, server, implementerSpec(t, server, fake, 41))
	go func() { _ = agent.Prompt(t.Context(), "Store plans in cents.") }()
	testkit.WaitFor(t, func() bool { return hasPausedReason(session(t, server, agent.ID())) })
	return agent
}

func TestTheDrainDoesNotWaitForALeadAndAWorkerThatWaitForAPause(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, usageLimit, devinLead)
	states := drainEvents(t, server)
	agent := startPausedImplementer(t, server, fake)
	sendChat(t, server, leadChat, "Plan the API")
	waitForChatSession(t, server, leadChat, engine.LeadRole, hasPausedReason)
	defer end(t, agent, "stopped")

	ends := startDrain(t, server)

	if got := <-ends; got != "drained" {
		t.Fatalf("drain end = %s", got)
	}
	waitForDrain(t, states, drainState{true, 0})
	if got := server.Engine.Draining(); got != (engine.DrainState{On: true}) {
		t.Errorf("drain = %+v", got)
	}
}

func TestACancelOfTheDrainCountsTheSessionsThatWaitForAPauseAgain(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, usageLimit, devinLead)
	agent := startPausedImplementer(t, server, fake)
	sendChat(t, server, leadChat, "Plan the API")
	waitForChatSession(t, server, leadChat, engine.LeadRole, hasPausedReason)
	defer end(t, agent, "stopped")
	if got := <-startDrain(t, server); got != "drained" {
		t.Fatalf("drain end = %s", got)
	}

	cancelDrain(t, server)

	testkit.WaitFor(t, func() bool { return server.Engine.Draining() == engine.DrainState{Waiting: 2} })
}

func TestASessionCountsInTheDrainAgainBeforeItSendsThePromptAfterThePause(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, usageLimitThenHang)
	states := drainEvents(t, server)
	agent := startPausedImplementer(t, server, fake)
	if got := <-startDrain(t, server); got != "drained" {
		t.Fatalf("drain end = %s", got)
	}
	found := testkit.WaitForValue(t, func() (pause, bool) { return devinPause(t, server) })

	if err := server.Engine.Resume(t.Context(), found.inboxItem); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return len(promptTexts(t, server, agent.ID())) == 2 })
	waitForDrain(t, states, drainState{true, 1})
	end(t, agent, "stopped")
	testkit.WaitFor(t, func() bool { return server.Engine.Draining() == engine.DrainState{On: true} })
}

func TestASessionSendsThePromptAfterThePauseWhenTheSealIsAborted(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, usageLimitThenHang)
	agent := startPausedImplementer(t, server, fake)
	defer end(t, agent, "stopped")
	if got := <-startDrain(t, server); got != "drained" {
		t.Fatalf("drain end = %s", got)
	}
	if got := server.Engine.Seal(); got != engine.Drained {
		t.Fatalf("seal = %s", got)
	}
	server.Engine.AbortDrain()
	found := testkit.WaitForValue(t, func() (pause, bool) { return devinPause(t, server) })

	if err := server.Engine.Resume(t.Context(), found.inboxItem); err != nil {
		t.Fatal(err)
	}

	testkit.WaitFor(t, func() bool { return len(promptTexts(t, server, agent.ID())) == 2 })
}

func TestASessionSendsThePromptWhenThePauseEndsInASealedDrainAndTheSealIsAborted(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, usageLimitThenHang)
	agent := startPausedImplementer(t, server, fake)
	defer end(t, agent, "stopped")
	if got := <-startDrain(t, server); got != "drained" {
		t.Fatalf("drain end = %s", got)
	}
	if got := server.Engine.Seal(); got != engine.Drained {
		t.Fatalf("seal = %s", got)
	}
	found := testkit.WaitForValue(t, func() (pause, bool) { return devinPause(t, server) })
	if err := server.Engine.Resume(t.Context(), found.inboxItem); err != nil {
		t.Fatal(err)
	}
	waitForPolls(t, fake)

	server.Engine.AbortDrain()

	testkit.WaitFor(t, func() bool { return len(promptTexts(t, server, agent.ID())) == 2 })
}

func TestASessionDoesNotSendThePromptAfterThePauseWhenTheDrainIsSealed(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, usageLimit)
	agent := startPausedImplementer(t, server, fake)
	defer end(t, agent, "stopped")
	if got := <-startDrain(t, server); got != "drained" {
		t.Fatalf("drain end = %s", got)
	}
	if !agent.HarnessRuns() {
		t.Fatal("the Harness of the session ended before the seal")
	}
	if got := server.Engine.Seal(); got != engine.Drained {
		t.Fatalf("seal = %s", got)
	}
	if !agent.HarnessRuns() {
		t.Fatal("the seal ended the Harness of the session")
	}
	server.Engine.ClosePaused()
	if agent.HarnessRuns() {
		t.Error("the Harness of the session runs after the close")
	}
	found := testkit.WaitForValue(t, func() (pause, bool) { return devinPause(t, server) })

	if err := server.Engine.Resume(t.Context(), found.inboxItem); err != nil {
		t.Fatal(err)
	}

	waitForPolls(t, fake)
	if got := promptTexts(t, server, agent.ID()); len(got) != 1 {
		t.Errorf("prompts = %q", got)
	}
	if got := server.Engine.Draining(); got != (engine.DrainState{On: true}) {
		t.Errorf("drain = %+v", got)
	}
}
