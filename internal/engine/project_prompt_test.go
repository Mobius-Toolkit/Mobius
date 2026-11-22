package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const (
	projectBody    = "Show the plan prices on the website."
	projectSection = "# Brief\n\nShip loyalty plans to all shops.\n\n# Project\n\nowner/shop#20 Plan pricing\n\n" + projectBody + "\n\n# Issue\n\n"
	rejects        = `call = { tool = "submit_verdicts", arguments = { items = [
    { item = 2, actions = [{ verdict = "reject", text = "The API needs this name." }] },
] } }
`
)

// addProject adds the Project #20 below the Workstream #12.
func addProject(fake *testkit.FakeGitHub) {
	fake.AddIssue(shop, 20, "Plan pricing")
	fake.AddLabel(shop, 20, "mobius:project", "owner")
	fake.SetBody(shop, 20, projectBody)
	fake.AddSubIssue(shop, 12, 20)
}

// belowProject gives an adjust function that moves #41 below the Project #20 before the server starts.
func belowProject(fake *testkit.FakeGitHub) func(*config.Config) {
	return func(*config.Config) {
		addProject(fake)
		fake.AddSubIssue(shop, 20, 41)
	}
}

// roleProjectPrompts runs the Implementer, the Reviewer and the Judge of #41, and gives the first prompt of each.
func roleProjectPrompts(t *testing.T, fake *testkit.FakeGitHub, adjust func(*config.Config)) (implementer, reviewer, judge string) {
	t.Helper()
	server := connectJudge(t, fake, rejects, "", adjust)
	reviewer = promptTexts(t, server, endedReviewers(t, server, 1)[0].ID)[0]
	reject := fake.AddReviewComment(shop, 42, 0, bot, "Rename plan to tier.")
	resolved(t, fake, reject)
	return implementerPrompts(t, server)[0], reviewer, judgePrompts(t, server)[0]
}

func TestTheImplementerTheReviewerAndTheJudgeGetTheProjectBetweenTheBriefAndTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)

	implementer, reviewer, judge := roleProjectPrompts(t, fake, belowProject(fake))

	for name, prompt := range map[string]string{"Implementer": implementer, "Reviewer": reviewer, "Judge": judge} {
		if strings.Count(prompt, "# Project\n") != 1 {
			t.Errorf("prompt of the %s = %s", name, prompt)
		}
		inOrder(t, prompt, projectSection)
	}
}

func TestATaskBelowAProjectThatIsBelowATaskIssueGetsTheNearestProject(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	nest := func(*config.Config) {
		fake.AddSubIssueOf(shop, 12, 21, "Website")
		fake.AddLabel(shop, 21, "mobius:project", "owner")
		fake.AddSubIssueOf(shop, 21, 30, "Pricing page")
		addProject(fake)
		fake.AddSubIssue(shop, 30, 20)
		fake.AddSubIssueOf(shop, 20, 50, "Price table")
		fake.AddSubIssue(shop, 50, 41)
	}

	server, _ := connectTask(t, fake, leadStarts, commits, nest)

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	approvalCheckRuns(t, server, fake)
	implementer := implementerPrompts(t, server)[0]
	inOrder(t, implementer, projectSection)
	if strings.Contains(implementer, "#21") {
		t.Errorf("prompt = %s", implementer)
	}
}

func TestATaskWithNoProjectGetsNoProjectSection(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)

	implementer, reviewer, judge := roleProjectPrompts(t, fake, noChange)

	for name, prompt := range map[string]string{"Implementer": implementer, "Reviewer": reviewer, "Judge": judge} {
		if strings.Contains(prompt, "# Project") {
			t.Errorf("prompt of the %s = %s", name, prompt)
		}
		inOrder(t, prompt, "# Brief\n\nShip loyalty plans to all shops.\n\n# Issue\n\n")
	}
}

func TestAFixRoundOfANewSessionGetsTheProjectBetweenTheBriefAndTheIssue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, belowProject(fake))
	firstRound(t, server, fake)
	waitForKept(t, server, 41)

	server.Engine.SetClock(func() time.Time { return time.Now().Add(61 * time.Minute) })
	startFixRound(t, server)

	sessions := waitForImplementerSessions(t, server, 2)
	prompts := promptTexts(t, server, sessions[1].ID)
	if len(prompts) != 1 || strings.Count(prompts[0], "# Project\n") != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	inOrder(t, prompts[0], projectSection, "# Open items\n")
}

func TestAShortPromptHasTheNewProjectBodyAndNoOldBody(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, belowProject(fake))
	firstRound(t, server, fake)
	waitForKept(t, server, 41)

	fake.SetBody(shop, 20, "Show the plan prices and the discounts.")
	testkit.WaitFor(t, func() bool { return issueRowOf(t, server, shop, 20).Body == "Show the plan prices and the discounts." })
	startFixRound(t, server)

	sessions := waitForImplementerSessions(t, server, 2)
	prompts := promptTexts(t, server, sessions[1].ID)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %q", prompts)
	}
	inOrder(t, prompts[0], "# Open items\n", "# Project\n\nowner/shop#20 Plan pricing\n\nShow the plan prices and the discounts.\n\nThis Project replaces the Project that the earlier round read.\n")
	if strings.Contains(prompts[0], projectBody) {
		t.Errorf("prompt still has the old Project body: %s", prompts[0])
	}
	if sessions[1].Continued != 1 {
		t.Errorf("continued = %d, want the kept session", sessions[1].Continued)
	}
}

func TestAShortPromptHasNoProjectSectionWhenTheProjectBodyDidNotChange(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := keptServer(t, fake, commits, belowProject(fake))
	firstRound(t, server, fake)
	waitForKept(t, server, 41)

	startFixRound(t, server)

	sessions := waitForImplementerSessions(t, server, 2)
	prompts := promptTexts(t, server, sessions[1].ID)
	if len(prompts) != 1 || !strings.Contains(prompts[0], "# Open items\n") || strings.Contains(prompts[0], "# Project") {
		t.Errorf("prompts = %q", prompts)
	}
	if sessions[1].Continued != 1 {
		t.Errorf("continued = %d, want the kept session", sessions[1].Continued)
	}
}
