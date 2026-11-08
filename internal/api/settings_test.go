package api_test

import (
	"database/sql"
	"net/http"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const fakeOptions = `
[options]
model = ["sonnet", "opus", "haiku", "swe-1.5", "gemini-3-pro"]
thought_level = ["low", "medium", "high"]
mode = ["default", "bypassPermissions", "yolo"]
`

type issueSetting struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

type nextSetting struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
	Own     bool   `json:"own"`
}

// startWithSettings starts a server with the Workstream #12 in owner/shop, the tasks #41 and #42 below it, and the fake
// Harness for each Role.
func startWithSettings(t *testing.T) *testserver.Server {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	fake.AddAccount("owner", "User")
	fake.AddRepository(shop)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddSubIssueOf(shop, 12, 41, "Add plan model")
	fake.AddSubIssueOf(shop, 12, 42, "Add plan prices")
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, fakeOptions)
	server := testserver.StartWith(t, testserver.Config(t, dataDir), fake.URL)
	_, err := server.DB.Exec("INSERT INTO github_apps (app_id, slug, private_key, client_id, client_secret) VALUES (?, ?, ?, ?, ?)",
		testkit.AppID, testkit.AppSlug, testkit.AppPrivateKey, testkit.AppClientID, testkit.AppClientSecret)
	if err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool { return len(tasks(t, server)) == 2 })
	return server
}

type taskLine struct {
	Number      int64         `json:"number"`
	Implementer *issueSetting `json:"implementer"`
}

func tasks(t *testing.T, server *testserver.Server) []taskLine {
	t.Helper()
	var body struct {
		Data []taskLine `json:"data"`
	}
	if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/workstreams/owner/shop/12/tasks", "", &body); reply.StatusCode != http.StatusOK {
		return nil
	}
	return body.Data
}

func implementerOf(t *testing.T, server *testserver.Server, number int64) *issueSetting {
	t.Helper()
	for _, line := range tasks(t, server) {
		if line.Number == number {
			return line.Implementer
		}
	}
	t.Fatalf("no task #%d", number)
	return nil
}

type errorBody struct {
	Error string `json:"error"`
}

func details(t *testing.T, server *testserver.Server) (lead, implementer nextSetting) {
	t.Helper()
	var body struct {
		Data struct {
			Lead        nextSetting `json:"lead"`
			Implementer nextSetting `json:"implementer"`
		} `json:"data"`
	}
	if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/workstreams/owner/shop/12/details", "", &body); reply.StatusCode != http.StatusOK {
		t.Fatalf("details: status %d", reply.StatusCode)
	}
	return body.Data.Lead, body.Data.Implementer
}

func records(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	rows, err := server.DB.Query("SELECT text FROM chat_messages WHERE repository = ? AND workstream = 12 AND author = 'Record' ORDER BY id", shop)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var texts []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, text)
	}
	return texts
}

func bindingRows(t *testing.T, server *testserver.Server) int {
	t.Helper()
	var count int
	if err := server.DB.QueryRow("SELECT count(*) FROM issue_bindings").Scan(&count); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	return count
}

func TestTheTaskImplementerEndpointsSetAndClearTheSettingOfOneTask(t *testing.T) {
	server := startWithSettings(t)
	url := server.URL + "/api/repositories/owner/shop/issues/41/implementer"

	if reply := call(t, server.Client, http.MethodPut, url, `{"harness":"devin","model":"opus","effort":"low"}`, nil); reply.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT: status %d", reply.StatusCode)
	}

	if got, want := implementerOf(t, server, 41), (&issueSetting{"devin", "opus", "low"}); got == nil || *got != *want {
		t.Errorf("task #41 setting = %+v, want %+v", got, want)
	}
	if got := implementerOf(t, server, 42); got != nil {
		t.Errorf("task #42 setting = %+v, want null", got)
	}
	if got, want := records(t, server), []string{"The Owner set the Implementer of #41 to devin · opus · low."}; !slices.Equal(got, want) {
		t.Errorf("records = %q, want %q", got, want)
	}

	if reply := call(t, server.Client, http.MethodDelete, url, "", nil); reply.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: status %d", reply.StatusCode)
	}

	if got := implementerOf(t, server, 41); got != nil {
		t.Errorf("task #41 setting = %+v after the clear, want null", got)
	}
	if got, want := records(t, server), []string{"The Owner set the Implementer of #41 to devin · opus · low.", "The Owner cleared the setting of the Implementer of #41."}; !slices.Equal(got, want) {
		t.Errorf("records = %q, want %q", got, want)
	}
}

func TestTheTaskImplementerEndpointRefusesAnInvalidSettingWith422AndStoresNoRow(t *testing.T) {
	server := startWithSettings(t)
	url := server.URL + "/api/repositories/owner/shop/issues/41/implementer"

	for _, body := range []string{
		`{"harness":"gemini","model":"swe-1.5","effort":"high"}`,
		`{"harness":"devin","model":"","effort":"high"}`,
		`{"harness":"devin","model":"swe-1.5","effort":""}`,
		`{"harness":"antigravity","model":"gemini-3-pro","effort":"high"}`,
	} {
		var refusal errorBody
		reply := call(t, server.Client, http.MethodPut, url, body, &refusal)
		if reply.StatusCode != http.StatusUnprocessableEntity || refusal.Error == "" {
			t.Errorf("PUT %s: status %d, error %q, want 422 with a reason", body, reply.StatusCode, refusal.Error)
		}
	}

	if rows := bindingRows(t, server); rows != 0 {
		t.Errorf("issue_bindings has %d rows, want 0", rows)
	}
	if got := records(t, server); len(got) != 0 {
		t.Errorf("records = %q, want none", got)
	}
}

func TestTheWorkstreamEndpointsSetAndClearTheLeadSettingAndTheImplementerSetting(t *testing.T) {
	server := startWithSettings(t)
	lead, implementer := details(t, server)
	if want := (nextSetting{"claude-code", "opus", "high", false}); lead != want {
		t.Errorf("lead = %+v, want %+v", lead, want)
	}
	if want := (nextSetting{"devin", "swe-1.5", "high", false}); implementer != want {
		t.Errorf("implementer = %+v, want %+v", implementer, want)
	}

	for _, put := range [][2]string{
		{"lead", `{"harness":"claude-code","model":"sonnet","effort":"low"}`},
		{"implementer", `{"harness":"devin","model":"opus","effort":"medium"}`},
	} {
		if reply := call(t, server.Client, http.MethodPut, server.URL+"/api/workstreams/owner/shop/12/"+put[0], put[1], nil); reply.StatusCode != http.StatusNoContent {
			t.Fatalf("PUT %s: status %d", put[0], reply.StatusCode)
		}
	}

	lead, implementer = details(t, server)
	if want := (nextSetting{"claude-code", "sonnet", "low", true}); lead != want {
		t.Errorf("lead = %+v, want %+v", lead, want)
	}
	if want := (nextSetting{"devin", "opus", "medium", true}); implementer != want {
		t.Errorf("implementer = %+v, want %+v", implementer, want)
	}
	if got := implementerOf(t, server, 41); got != nil {
		t.Errorf("task #41 setting = %+v, want null: the Workstream setting is no task setting", got)
	}
	wantRecords := []string{
		"The Owner set the Lead to claude-code · sonnet · low.",
		"The Owner set the Implementer of the Workstream to devin · opus · medium.",
	}
	if got := records(t, server); !slices.Equal(got, wantRecords) {
		t.Errorf("records = %q, want %q", got, wantRecords)
	}

	for _, path := range []string{"lead", "implementer"} {
		if reply := call(t, server.Client, http.MethodDelete, server.URL+"/api/workstreams/owner/shop/12/"+path, "", nil); reply.StatusCode != http.StatusNoContent {
			t.Fatalf("DELETE %s: status %d", path, reply.StatusCode)
		}
	}

	lead, implementer = details(t, server)
	if lead.Own || implementer.Own || lead.Model != "opus" || implementer.Model != "swe-1.5" {
		t.Errorf("after the clear: lead = %+v, implementer = %+v, want the bindings of the Roles", lead, implementer)
	}
	if rows := bindingRows(t, server); rows != 0 {
		t.Errorf("issue_bindings has %d rows, want 0", rows)
	}
	wantRecords = append(wantRecords, "The Owner cleared the setting of the Lead.", "The Owner cleared the setting of the Implementer of the Workstream.")
	if got := records(t, server); !slices.Equal(got, wantRecords) {
		t.Errorf("records = %q, want %q", got, wantRecords)
	}
}

func TestAWorkstreamEndpointRefusesAnInvalidSettingWith422AndAnIssueThatIsNoWorkstreamWith409(t *testing.T) {
	server := startWithSettings(t)

	for _, path := range []string{"lead", "implementer"} {
		var refusal errorBody
		reply := call(t, server.Client, http.MethodPut, server.URL+"/api/workstreams/owner/shop/12/"+path, `{"harness":"gemini","model":"opus","effort":"high"}`, &refusal)
		if reply.StatusCode != http.StatusUnprocessableEntity || refusal.Error == "" {
			t.Errorf("PUT %s: status %d, error %q, want 422 with a reason", path, reply.StatusCode, refusal.Error)
		}
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			var conflict errorBody
			reply := call(t, server.Client, method, server.URL+"/api/workstreams/owner/shop/41/"+path, `{"harness":"devin","model":"opus","effort":"low"}`, &conflict)
			if reply.StatusCode != http.StatusConflict || conflict.Error != "The issue is not an open Workstream." {
				t.Errorf("%s %s of a task: status %d, error %q, want 409", method, path, reply.StatusCode, conflict.Error)
			}
		}
	}

	if rows := bindingRows(t, server); rows != 0 {
		t.Errorf("issue_bindings has %d rows, want 0", rows)
	}
}

func TestTheAgentsEndpointGivesTheEffortOfEachSession(t *testing.T) {
	server := startWithSettings(t)
	for _, role := range []string{engine.LeadRole, engine.ResearcherRole} {
		_, err := server.Engine.Start(t.Context(), engine.Spec{Role: role, Organization: "owner", Repository: shop, Workstream: 12, Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
	}

	var body struct {
		Data []struct {
			Harness string `json:"harness"`
			Effort  string `json:"effort"`
		} `json:"data"`
	}
	if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/workstreams/owner/shop/12/agents", "", &body); reply.StatusCode != http.StatusOK {
		t.Fatalf("agents: status %d", reply.StatusCode)
	}

	efforts := map[string]string{}
	for _, agent := range body.Data {
		efforts[agent.Harness] = agent.Effort
	}
	if len(body.Data) != 2 || efforts["claude-code"] != "high" || efforts["antigravity"] != "" {
		t.Errorf("agents = %+v, want claude-code with the effort high and antigravity with none", body.Data)
	}
}
