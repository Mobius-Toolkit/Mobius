package engine_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type roleFields struct {
	Harness bool `json:"harness"`
	Model   bool `json:"model"`
	Effort  bool `json:"effort"`
	Max     bool `json:"max"`
}

type roleState struct {
	Role    string     `json:"role"`
	Harness string     `json:"harness"`
	Model   string     `json:"model"`
	Effort  string     `json:"effort"`
	Max     int        `json:"max"`
	UI      roleFields `json:"ui"`
}

func getRoles(t *testing.T, server *testserver.Server) []roleState {
	t.Helper()
	response, err := server.Client.Get(server.URL + "/api/roles")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/roles: status %d", response.StatusCode)
	}
	var body struct {
		Data []roleState `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func getRole(t *testing.T, server *testserver.Server, role string) roleState {
	t.Helper()
	for _, state := range getRoles(t, server) {
		if state.Role == role {
			return state
		}
	}
	t.Fatalf("GET /api/roles has no role %s", role)
	return roleState{}
}

// sendRole sends a PUT with body, or a DELETE when body is empty, to the route of role. It gives the status and the text of the answer.
func sendRole(t *testing.T, server *testserver.Server, role, body string) (int, string) {
	t.Helper()
	method := http.MethodPut
	if body == "" {
		method = http.MethodDelete
	}
	request, err := http.NewRequestWithContext(t.Context(), method, server.URL+"/api/roles/"+role, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	text, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(text)
}

// putRole sets the role and fails the test when the answer is not 200.
func putRole(t *testing.T, server *testserver.Server, role, body string) roleState {
	t.Helper()
	status, text := sendRole(t, server, role, body)
	if status != http.StatusOK {
		t.Fatalf("PUT /api/roles/%s: status %d: %s", role, status, text)
	}
	var answer struct {
		Data roleState `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &answer); err != nil {
		t.Fatal(err)
	}
	return answer.Data
}

func roleRows(t *testing.T, server *testserver.Server) int {
	t.Helper()
	var count int
	if err := server.DB.QueryRow("SELECT count(*) FROM role_settings").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// turnModels gives the models of the turn_usage rows of the session.
func turnModels(t *testing.T, server *testserver.Server, session int64) []string {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, model *string) error {
		return rows.Scan(model)
	}, "SELECT model FROM turn_usage WHERE session = ?", session)
}

const (
	leadHaiku = `{"harness": "claude-code", "model": "haiku", "effort": "high", "max": 8}`
	leadOpus  = `{"harness": "claude-code", "model": "opus", "effort": "high", "max": 8}`
	usageTurn = "[[prompts]]\nreply = [\"ok\"]\nusage = '{\"inputTokens\": 10, \"outputTokens\": 2}'\n"
)

func TestAPutOfTheModelGivesTheNextSessionTheNewModel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, usageTurn)

	got := putRole(t, server, "lead", leadHaiku)

	want := roleState{Role: "lead", Harness: "claude-code", Model: "haiku", Effort: "high", Max: 8, UI: roleFields{Model: true}}
	if got != want {
		t.Errorf("PUT answer = %+v, want %+v", got, want)
	}
	if listed := getRole(t, server, "lead"); listed != want {
		t.Errorf("GET answer = %+v, want %+v", listed, want)
	}
	if other := getRole(t, server, "reviewer"); other.Model != "opus" || other.UI != (roleFields{}) {
		t.Errorf("reviewer = %+v", other)
	}
	id := run(t, server, leadSpec(t), "Who runs this session?")
	updates := rows(t, server, id, "update")
	configured := []string{session(t, server, id).Model, currentOption(t, updates, "model"), currentOption(t, updates, "thought_level")}
	if want := []string{"haiku", "haiku", "high"}; !reflect.DeepEqual(configured, want) {
		t.Errorf("session model, option model, option effort = %q, want %q", configured, want)
	}
}

func TestAPutWithTheValuesOfConfigStoresNoRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")

	got := putRole(t, server, "lead", leadOpus)

	if got.UI != (roleFields{}) || roleRows(t, server) != 0 {
		t.Errorf("answer = %+v, rows = %d", got, roleRows(t, server))
	}
	putRole(t, server, "lead", leadHaiku)
	if roleRows(t, server) != 1 {
		t.Fatalf("rows = %d, want 1", roleRows(t, server))
	}
	putRole(t, server, "lead", leadOpus)
	if listed := getRole(t, server, "lead"); listed.UI != (roleFields{}) || listed.Model != "opus" || roleRows(t, server) != 0 {
		t.Errorf("lead = %+v, rows = %d", listed, roleRows(t, server))
	}
}

func TestAPutThatFailsACheckGives422AndChangesNoValue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, "")
	if err := os.Remove(filepath.Join(dataDir, "harnesses", "agy_acp_server")); err != nil {
		t.Fatal(err)
	}
	before := getRoles(t, server)

	for _, c := range []struct{ name, role, body, want string }{
		{"unknown Harness", "lead", `{"harness": "gpt", "model": "opus", "effort": "high", "max": 8}`, "roles.lead.harness: must be claude-code, antigravity or devin"},
		{"empty model", "lead", `{"harness": "claude-code", "model": "", "effort": "high", "max": 8}`, "roles.lead.model: must not be empty"},
		{"effort for antigravity", "researcher", `{"harness": "antigravity", "model": "gemini-3-pro", "effort": "high", "max": 2}`, "roles.researcher.effort: is not allowed for antigravity"},
		{"no effort for claude-code", "lead", `{"harness": "claude-code", "model": "opus", "effort": "", "max": 8}`, "roles.lead.effort: is required for claude-code"},
		{"reviewer equal to implementer", "reviewer", `{"harness": "devin", "model": "swe-1.5", "effort": "high", "max": 2}`, "roles.reviewer: must not have the same harness and model as roles.implementer"},
		{"Harness not on the PATH", "curator", `{"harness": "antigravity", "model": "gemini-3-pro", "effort": "", "max": 2}`, "roles.curator.harness: agy_acp_server is not on the PATH of the agents"},
		{"max 0", "lead", `{"harness": "claude-code", "model": "opus", "effort": "high", "max": 0}`, "roles.lead.max: must be 1 or more"},
	} {
		status, text := sendRole(t, server, c.role, c.body)

		if status != http.StatusUnprocessableEntity || !strings.Contains(text, c.want) {
			t.Errorf("%s: status %d, answer %s, want 422 with %q", c.name, status, text, c.want)
		}
	}
	if status, text := sendRole(t, server, "boss", leadHaiku); status < 400 || status >= 500 {
		t.Errorf("unknown role: status %d: %s", status, text)
	}
	if after := getRoles(t, server); !reflect.DeepEqual(after, before) || roleRows(t, server) != 0 {
		t.Errorf("roles = %+v, want %+v, rows = %d", after, before, roleRows(t, server))
	}
}

func TestARunningSessionKeepsItsModelAfterAPut(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, usageTurn)
	running := start(t, server, leadSpec(t))

	putRole(t, server, "lead", leadHaiku)
	if err := running.Prompt(t.Context(), "Who runs this session?", nil); err != nil {
		t.Fatal(err)
	}
	end(t, running, "done")
	next := run(t, server, leadSpec(t), "Who runs this session?")

	if got := turnModels(t, server, running.ID()); !reflect.DeepEqual(got, []string{"opus"}) {
		t.Errorf("models of the running session = %q", got)
	}
	if got := turnModels(t, server, next); !reflect.DeepEqual(got, []string{"haiku"}) {
		t.Errorf("models of the next session = %q", got)
	}
}

func TestAPutThatRaisesMaxStartsAWaitingSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) { cfg.Roles.Lead.Max = 1 })
	first := start(t, server, leadSpec(t))
	defer end(t, first, "idle")
	other := leadSpec(t)
	other.Workstream = 50
	second := startLater(t.Context(), server, other)
	waiting := testkit.WaitForValue(t, func() (store.Session, bool) {
		var found store.Session
		err := server.DB.QueryRow("SELECT id, queue_reason FROM sessions WHERE workstream = 50").Scan(&found.ID, &found.QueueReason)
		return found, err == nil && found.QueueReason.Valid
	})
	if waiting.QueueReason.String != "no free lead slot (1/1)" {
		t.Fatalf("queue reason = %q", waiting.QueueReason.String)
	}

	got := putRole(t, server, "lead", `{"harness": "claude-code", "model": "opus", "effort": "high", "max": 2}`)

	agent := await(t, second)
	defer end(t, agent, "idle")
	var reason sql.NullString
	if err := server.DB.QueryRow("SELECT queue_reason FROM sessions WHERE id = ?", agent.ID()).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if !got.UI.Max || agent.ID() != waiting.ID || reason.Valid {
		t.Errorf("answer = %+v, agent = %d, waiting = %d, queue reason = %v", got, agent.ID(), waiting.ID, reason)
	}
}

func TestADeleteRemovesTheRowAndTheNextSessionUsesConfig(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	putRole(t, server, "lead", `{"harness": "claude-code", "model": "haiku", "effort": "low", "max": 3}`)

	status, text := sendRole(t, server, "lead", "")

	if status != http.StatusOK {
		t.Fatalf("DELETE: status %d: %s", status, text)
	}
	want := roleState{Role: "lead", Harness: "claude-code", Model: "opus", Effort: "high", Max: 8}
	if got := getRole(t, server, "lead"); got != want || roleRows(t, server) != 0 {
		t.Errorf("lead = %+v, rows = %d, want %+v", got, roleRows(t, server), want)
	}
	id := run(t, server, leadSpec(t), "Who runs this session?")
	if got := session(t, server, id); got.Model != "opus" || got.Effort.String != "high" {
		t.Errorf("session = %+v", got)
	}
}

func TestADeleteThatMakesTheReviewerEqualTheImplementerIsRefused(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	putRole(t, server, "reviewer", `{"harness": "claude-code", "model": "sonnet", "effort": "high", "max": 2}`)
	putRole(t, server, "implementer", `{"harness": "claude-code", "model": "opus", "effort": "high", "max": 2}`)

	status, text := sendRole(t, server, "reviewer", "")

	if status != http.StatusUnprocessableEntity || !strings.Contains(text, "must not have the same harness and model") {
		t.Errorf("status %d, answer %s", status, text)
	}
	if got := getRole(t, server, "reviewer"); got.Model != "sonnet" || !got.UI.Model {
		t.Errorf("reviewer = %+v", got)
	}
}

func TestTheRoleValuesOfTheUIStayAfterANewStartOnTheSameDatabase(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	first, dataDir := connect(t, fake, "")
	want := putRole(t, first, "lead", leadHaiku)
	first.Stop()

	second := testserver.Start(t, dataDir, fake.URL)

	if got := getRole(t, second, "lead"); got != want {
		t.Errorf("lead = %+v, want %+v", got, want)
	}
}

func TestANewStartFailsWhenARowFailsTheChecksWithTheValuesOfConfig(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	first, dataDir := connect(t, fake, "")
	putRole(t, first, "reviewer", `{"harness": "claude-code", "model": "sonnet", "effort": "high", "max": 2}`)
	first.Stop()
	cfg := testserver.Config(t, dataDir)
	cfg.Roles.Implementer = config.RoleBinding{Harness: config.ClaudeCode, Model: "sonnet", Effort: "high", Max: 2}

	err := engine.New(first.DB, nil, cfg, engine.Agents{}).Recover(t.Context())

	if err == nil || !strings.HasPrefix(err.Error(), "role settings of the UI: ") || !strings.Contains(err.Error(), "roles.reviewer") {
		t.Errorf("Recover = %v", err)
	}
}
