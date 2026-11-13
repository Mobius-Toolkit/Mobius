package engine_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type moreBinding struct {
	ID      int64  `json:"id"`
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
	Context int    `json:"context"`
	Max     int    `json:"max"`
}

type implementerState struct {
	Role         string        `json:"role"`
	Harness      string        `json:"harness"`
	Model        string        `json:"model"`
	Effort       string        `json:"effort"`
	Max          int           `json:"max"`
	UI           roleFields    `json:"ui"`
	MoreBindings []moreBinding `json:"moreBindings"`
}

const (
	sonnetHigh = `{"harness": "claude-code", "model": "sonnet", "effort": "high", "context": 0, "max": 3}`
	opusLow    = `{"harness": "claude-code", "model": "opus", "effort": "low", "context": 200000, "max": 1}`
	geminiPro  = `{"harness": "antigravity", "model": "gemini-3-pro", "effort": "", "context": 0, "max": 2}`
)

func sendBinding(t *testing.T, server *testserver.Server, method, path, body string) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, server.URL+"/api/roles/implementer/bindings"+path, strings.NewReader(body))
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

func decodeImplementer(t *testing.T, text string) implementerState {
	t.Helper()
	var answer struct {
		Data implementerState `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &answer); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return answer.Data
}

// changeBinding sends the request and fails the test when the answer is not 200.
func changeBinding(t *testing.T, server *testserver.Server, method, path, body string) implementerState {
	t.Helper()
	status, text := sendBinding(t, server, method, path, body)
	if status != http.StatusOK {
		t.Fatalf("%s %s: status %d: %s", method, path, status, text)
	}
	return decodeImplementer(t, text)
}

// listedImplementer reads the Implementer with GET /api/roles, and the moreBindings of each other Role.
func listedImplementer(t *testing.T, server *testserver.Server) implementerState {
	t.Helper()
	response, err := server.Client.Get(server.URL + "/api/roles")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body struct {
		Data []implementerState `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var found implementerState
	for _, role := range body.Data {
		if role.Role == "implementer" {
			found = role
			continue
		}
		if role.MoreBindings == nil || len(role.MoreBindings) != 0 {
			t.Errorf("moreBindings of %s = %#v, want an empty list", role.Role, role.MoreBindings)
		}
	}
	return found
}

func bindingRows(t *testing.T, server *testserver.Server) int {
	t.Helper()
	var count int
	if err := server.DB.QueryRow("SELECT count(*) FROM implementer_bindings").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestABindingIsAddedChangedAndRemovedAndEachChangeIsReadBackWithGetRoles(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")

	added := changeBinding(t, server, http.MethodPost, "", sonnetHigh)

	first := moreBinding{ID: added.MoreBindings[0].ID, Harness: "claude-code", Model: "sonnet", Effort: "high", Max: 3}
	if want := []moreBinding{first}; !reflect.DeepEqual(added.MoreBindings, want) || added.Harness != "devin" || added.Model != "swe-1.5" {
		t.Errorf("POST answer = %+v, want moreBindings %+v and the first binding of config", added, want)
	}
	if listed := listedImplementer(t, server); !reflect.DeepEqual(listed, added) {
		t.Errorf("GET answer = %+v, want %+v", listed, added)
	}

	second := changeBinding(t, server, http.MethodPost, "", geminiPro).MoreBindings[1]
	changed := changeBinding(t, server, http.MethodPut, "/"+strconv.FormatInt(first.ID, 10), opusLow)

	want := []moreBinding{
		{ID: first.ID, Harness: "claude-code", Model: "opus", Effort: "low", Context: 200000, Max: 1},
		{ID: second.ID, Harness: "antigravity", Model: "gemini-3-pro", Max: 2},
	}
	if second.ID <= first.ID || !reflect.DeepEqual(changed.MoreBindings, want) {
		t.Errorf("PUT answer = %+v, want moreBindings %+v", changed.MoreBindings, want)
	}
	if listed := listedImplementer(t, server); !reflect.DeepEqual(listed, changed) {
		t.Errorf("GET answer = %+v, want %+v", listed, changed)
	}

	removed := changeBinding(t, server, http.MethodDelete, "/"+strconv.FormatInt(first.ID, 10), "")

	if !reflect.DeepEqual(removed.MoreBindings, want[1:]) || bindingRows(t, server) != 1 {
		t.Errorf("DELETE answer = %+v, rows = %d", removed.MoreBindings, bindingRows(t, server))
	}
	if listed := listedImplementer(t, server); !reflect.DeepEqual(listed, removed) {
		t.Errorf("GET answer = %+v, want %+v", listed, removed)
	}
}

func TestABindingThatFailsACheckGives422AndStoresNoRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, "")
	stored := changeBinding(t, server, http.MethodPost, "", sonnetHigh).MoreBindings[0]
	if err := os.Remove(filepath.Join(dataDir, "harnesses", "agy_acp_server")); err != nil {
		t.Fatal(err)
	}
	before := listedImplementer(t, server)

	for _, c := range []struct{ name, body, want string }{
		{"max 0", `{"harness": "claude-code", "model": "sonnet", "effort": "high", "max": 0}`, "roles.implementer.max: must be 1 or more"},
		{"Harness not on the PATH", geminiPro, "roles.implementer.harness: agy_acp_server is not on the PATH of the agents"},
		{"unknown Harness", `{"harness": "gpt", "model": "sonnet", "effort": "high", "max": 1}`, "roles.implementer.harness: must be claude-code, antigravity, devin or dsh"},
	} {
		for _, request := range []struct{ method, path string }{{http.MethodPost, ""}, {http.MethodPut, "/" + strconv.FormatInt(stored.ID, 10)}} {
			status, text := sendBinding(t, server, request.method, request.path, c.body)

			if status != http.StatusUnprocessableEntity || !strings.Contains(text, c.want) {
				t.Errorf("%s %s: status %d, answer %s, want 422 with %q", c.name, request.method, status, text, c.want)
			}
		}
	}
	if after := listedImplementer(t, server); !reflect.DeepEqual(after, before) || bindingRows(t, server) != 1 {
		t.Errorf("implementer = %+v, want %+v, rows = %d", after, before, bindingRows(t, server))
	}
}

func TestABindingWhoseAccessCheckFailsGives422AndStoresNoRow(t *testing.T) {
	t.Parallel()
	server, _, _ := accessServer(t, "", noKey)
	stored := changeBinding(t, server, http.MethodPost, "", sonnetHigh).MoreBindings[0]
	before := listedImplementer(t, server)
	devin := `{"harness": "devin", "model": "swe-1.5", "effort": "low", "max": 1}`

	for _, request := range []struct{ method, path string }{{http.MethodPost, ""}, {http.MethodPut, "/" + strconv.FormatInt(stored.ID, 10)}} {
		status, text := sendBinding(t, server, request.method, request.path, devin)

		if status != http.StatusUnprocessableEntity || !strings.Contains(text, "access check failed: devin: turn failed: no API key for provider route") {
			t.Errorf("%s: status %d, answer %s", request.method, status, text)
		}
	}
	if after := listedImplementer(t, server); !reflect.DeepEqual(after, before) || bindingRows(t, server) != 1 {
		t.Errorf("implementer = %+v, want %+v, rows = %d", after, before, bindingRows(t, server))
	}
}

func TestAChangeOrARemovalOfAnUnknownBindingGives404(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	changeBinding(t, server, http.MethodPost, "", sonnetHigh)
	before := listedImplementer(t, server)

	for _, request := range []struct{ method, path, body string }{
		{http.MethodPut, "/999", sonnetHigh},
		{http.MethodPut, "/0", sonnetHigh},
		{http.MethodDelete, "/999", ""},
	} {
		status, text := sendBinding(t, server, request.method, request.path, request.body)

		if status != http.StatusNotFound {
			t.Errorf("%s %s: status %d: %s", request.method, request.path, status, text)
		}
	}
	if after := listedImplementer(t, server); !reflect.DeepEqual(after, before) {
		t.Errorf("implementer = %+v, want %+v", after, before)
	}
}

func TestARemovalOfTheFirstBindingMovesTheSecondBindingToTheFirstPlace(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	changeBinding(t, server, http.MethodPost, "", sonnetHigh)

	got := changeBinding(t, server, http.MethodDelete, "/0", "")

	want := implementerState{
		Role: "implementer", Harness: "claude-code", Model: "sonnet", Effort: "high", Max: 3,
		UI: roleFields{Harness: true, Model: true, Max: true}, MoreBindings: []moreBinding{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DELETE answer = %+v, want %+v", got, want)
	}
	if listed := listedImplementer(t, server); !reflect.DeepEqual(listed, want) || bindingRows(t, server) != 0 {
		t.Errorf("GET answer = %+v, want %+v, rows = %d", listed, want, bindingRows(t, server))
	}

	reset := resetImplementer(t, server)

	if reset.Harness != "devin" || reset.Model != "swe-1.5" || reset.UI != (roleFields{}) {
		t.Errorf("implementer after the reset = %+v", reset)
	}
}

func resetImplementer(t *testing.T, server *testserver.Server) implementerState {
	t.Helper()
	status, text := sendRole(t, server, "implementer", "")
	if status != http.StatusOK {
		t.Fatalf("DELETE /api/roles/implementer: status %d: %s", status, text)
	}
	return decodeImplementer(t, text)
}

func TestARemovalOfTheFirstBindingWithNoOtherBindingGives422AndChangesNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	before := listedImplementer(t, server)

	status, text := sendBinding(t, server, http.MethodDelete, "/0", "")

	if status != http.StatusUnprocessableEntity || !strings.Contains(text, "The Implementer has only one binding") {
		t.Errorf("status %d, answer %s", status, text)
	}
	if after := listedImplementer(t, server); !reflect.DeepEqual(after, before) || roleRows(t, server) != 0 {
		t.Errorf("implementer = %+v, want %+v, role rows = %d", after, before, roleRows(t, server))
	}
}

func TestARemovalOfTheFirstBindingWhoseSecondBindingFailsACheckGives422AndChangesNothing(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connect(t, fake, "")
	changeBinding(t, server, http.MethodPost, "", geminiPro)
	if err := os.Remove(filepath.Join(dataDir, "harnesses", "agy_acp_server")); err != nil {
		t.Fatal(err)
	}
	before := listedImplementer(t, server)

	status, text := sendBinding(t, server, http.MethodDelete, "/0", "")

	if status != http.StatusUnprocessableEntity || !strings.Contains(text, "agy_acp_server is not on the PATH of the agents") {
		t.Errorf("status %d, answer %s", status, text)
	}
	if after := listedImplementer(t, server); !reflect.DeepEqual(after, before) || roleRows(t, server) != 0 || bindingRows(t, server) != 1 {
		t.Errorf("implementer = %+v, want %+v, role rows = %d, binding rows = %d", after, before, roleRows(t, server), bindingRows(t, server))
	}
}

func TestTheImplementerBindingsStayAfterANewStartOnTheSameDatabase(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	first, dataDir := connect(t, fake, "")
	changeBinding(t, first, http.MethodPost, "", sonnetHigh)
	want := changeBinding(t, first, http.MethodPost, "", opusLow)
	first.Stop()

	second := testserver.Start(t, dataDir, fake.URL)

	if got := listedImplementer(t, second); !reflect.DeepEqual(got, want) {
		t.Errorf("implementer = %+v, want %+v", got, want)
	}
}
