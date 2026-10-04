package engine_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/config"
	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit/testserver"
)

type activeAgent struct {
	Agent struct {
		ID           int64  `json:"id"`
		Role         string `json:"role"`
		Name         string `json:"name"`
		Title        string `json:"title"`
		Organization string `json:"organization"`
		Repository   string `json:"repository"`
		Workstream   int64  `json:"workstream"`
		Issue        *int64 `json:"issue"`
		QueueReason  string `json:"queueReason"`
	} `json:"agent"`
	WorkstreamTitle *string `json:"workstreamTitle"`
	IssueTitle      *string `json:"issueTitle"`
	PullRequest     *int64  `json:"pullRequest"`
}

type activeAgents struct {
	Count  int64 `json:"count"`
	Max    int64 `json:"max"`
	Groups []struct {
		Name   string        `json:"name"`
		Count  int64         `json:"count"`
		Max    int64         `json:"max"`
		Agents []activeAgent `json:"agents"`
	} `json:"groups"`
}

func overview(t *testing.T, server *testserver.Server) activeAgents {
	t.Helper()
	response, err := server.Client.Get(server.URL + "/api/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body struct {
		Data activeAgents `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func TestTheAgentsPageCountsTheOpenSessionsOfEachRoleAgainstItsLimit(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) {
		cfg.MaxAgents = 5
		cfg.Roles.Implementer.Max = 3
		cfg.Roles.Researcher.Max = 1
	})
	implementer := start(t, server, implementerSpec(t, server, 41))
	defer end(t, implementer, "stopped")
	lead := start(t, server, leadSpec(t))
	defer end(t, lead, "idle")
	researcher := start(t, server, roleSpec(t, engine.ResearcherRole))
	// A queued session is in its group, but it holds no slot.
	second := startLater(t.Context(), server, roleSpec(t, engine.ResearcherRole))
	defer func() {
		end(t, researcher, "done")
		end(t, await(t, second), "done")
	}()
	queued(t, server, engine.ResearcherRole)

	got := overview(t, server)

	type group struct {
		name       string
		count, max int64
		agents     int
	}
	var groups []group
	for _, g := range got.Groups {
		groups = append(groups, group{g.Name, g.Count, g.Max, len(g.Agents)})
	}
	want := []group{{"Lead", 1, 8, 1}, {"Triager", 0, 2, 0}, {"Implementer", 1, 3, 1}, {"Researcher", 1, 1, 2}, {"Reviewer", 0, 2, 0}, {"Judge", 0, 2, 0}}
	if got.Count != 2 || got.Max != 5 || !reflect.DeepEqual(groups, want) {
		t.Fatalf("overview = %d/%d %+v", got.Count, got.Max, groups)
	}
	leadRow := got.Groups[0].Agents[0].Agent
	if leadRow.Name != "Lead" || leadRow.Title != "chat session" || leadRow.Organization != "owner" || leadRow.Repository != shop ||
		leadRow.Workstream != 12 || leadRow.Issue != nil || leadRow.QueueReason != "" {
		t.Errorf("Lead = %+v", leadRow)
	}
	implementerRow := got.Groups[2].Agents[0].Agent
	if implementerRow.Role != engine.ImplementerRole || implementerRow.Issue == nil || *implementerRow.Issue != 41 {
		t.Errorf("Implementer = %+v", implementerRow)
	}
	researchers := got.Groups[3].Agents
	if researchers[0].Agent.Workstream != 12 || researchers[0].Agent.Issue != nil || researchers[1].Agent.QueueReason != "no free researcher slot (1/1)" {
		t.Errorf("Researchers = %+v", researchers)
	}
}

func TestEachRowShowsTheWorkstreamTheTicketAndThePullRequestWhenTheyExist(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	for _, statement := range []string{
		`INSERT INTO sessions (role, harness, model, organization, repository, workstream, issue, started_at)
		 VALUES ('implementer', 'devin', 'model', 'owner', 'owner/shop', 12, 41, '2026-10-04T10:00:00Z'),
		        ('researcher', 'antigravity', 'model', 'owner', 'owner/shop', 12, NULL, '2026-10-04T10:00:00Z'),
		        ('triager', 'claude-code', 'model', 'owner', '', 0, NULL, '2026-10-04T10:00:00Z')`,
		`INSERT INTO tasks (repository, issue, workstream, state, dispatched_at, pull_request) VALUES ('owner/shop', 41, 12, 'working', '2026-10-04T10:00:00Z', 42)`,
		`INSERT INTO copied_workstreams (repository, number, title, body, autopilot) VALUES ('owner/shop', 12, 'Integrate loyalty plans', '', 0)`,
		`INSERT INTO copied_issues (repository, workstream, position, number, parent, title, body, state, author, html_url, repository_url)
		 VALUES ('owner/shop', 12, 0, 41, 12, 'Add plan model', '', 'open', 'owner', '', '')`,
	} {
		if _, err := server.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	got := overview(t, server)

	row := func(group int) []any {
		agent := got.Groups[group].Agents[0]
		return []any{agent.WorkstreamTitle, agent.IssueTitle, agent.PullRequest}
	}
	str := func(text string) *string { return &text }
	pullRequest := int64(42)
	if want := []any{str("Integrate loyalty plans"), str("Add plan model"), &pullRequest}; !reflect.DeepEqual(row(2), want) {
		t.Errorf("Implementer = %v", row(2))
	}
	if want := []any{str("Integrate loyalty plans"), (*string)(nil), (*int64)(nil)}; !reflect.DeepEqual(row(3), want) {
		t.Errorf("Researcher = %v", row(3))
	}
	if want := []any{(*string)(nil), (*string)(nil), (*int64)(nil)}; !reflect.DeepEqual(row(1), want) {
		t.Errorf("Triager = %v", row(1))
	}
}
