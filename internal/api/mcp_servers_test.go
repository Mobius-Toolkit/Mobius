package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	mcpServersPath = "/api/organizations/owner/mcp-servers"
	selectionPath  = "/api/workstreams/owner/shop/12/mcp-servers"
	makeadirBody   = `{"name":"makeadir","kind":"http","url":"https://makeadir.example.com/mcp","headers":[{"name":"Authorization","value":"Bearer secret-token"},{"name":"X-Site","value":"site-secret"}]}`
	filesBody      = `{"name":"files","kind":"stdio","command":"files-server","args":["--root","/srv"],"env":[{"name":"FILES_KEY","value":"env-secret"}]}`
)

type mcpName struct {
	Name string `json:"name"`
}

type mcpServer struct {
	ID      int64     `json:"id"`
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	URL     string    `json:"url"`
	Headers []mcpName `json:"headers"`
	Command string    `json:"command"`
	Args    []string  `json:"args"`
	Env     []mcpName `json:"env"`
}

func itoa(number int64) string {
	return strconv.FormatInt(number, 10)
}

func mcpRequest(t *testing.T, server *testserver.Server, method, path, body string) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	reply, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reply.Body.Close() }()
	text, err := io.ReadAll(reply.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply.StatusCode, string(text)
}

func decodeMcp[T any](t *testing.T, text string) T {
	t.Helper()
	var body struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	return body.Data
}

func addMcpServer(t *testing.T, server *testserver.Server, organization, body string) mcpServer {
	t.Helper()
	status, text := mcpRequest(t, server, http.MethodPost, "/api/organizations/"+organization+"/mcp-servers", body)
	if status != http.StatusCreated {
		t.Fatalf("POST %s: status %d: %s", body, status, text)
	}
	return decodeMcp[mcpServer](t, text)
}

func storedMcpHeaders(t *testing.T, server *testserver.Server, id int64) string {
	t.Helper()
	var headers string
	if err := server.DB.QueryRow("SELECT headers FROM mcp_servers WHERE id = ?", id).Scan(&headers); err != nil {
		t.Fatal(err)
	}
	return headers
}

func selectedMcpIDs(t *testing.T, server *testserver.Server) []int64 {
	t.Helper()
	status, text := mcpRequest(t, server, http.MethodGet, "/api/workstreams/owner/shop/12/details", "")
	if status != http.StatusOK {
		t.Fatalf("details: status %d: %s", status, text)
	}
	return decodeMcp[struct {
		McpServers []int64 `json:"mcpServers"`
	}](t, text).McpServers
}

func TestAddingAnHttpServerAndAStdioServerGivesTheNamesOfTheSecretsAndNoSecretValue(t *testing.T) {
	server := startWithSettings(t)

	remote := addMcpServer(t, server, "owner", makeadirBody)
	stdio := addMcpServer(t, server, "owner", filesBody)

	if remote.Name != "makeadir" || remote.Kind != "http" || remote.URL != "https://makeadir.example.com/mcp" || !slices.Equal(remote.Headers, []mcpName{{"Authorization"}, {"X-Site"}}) || remote.Command != "" || len(remote.Env) != 0 {
		t.Errorf("added http server = %+v", remote)
	}
	if stdio.Name != "files" || stdio.Kind != "stdio" || stdio.Command != "files-server" || !slices.Equal(stdio.Args, []string{"--root", "/srv"}) || !slices.Equal(stdio.Env, []mcpName{{"FILES_KEY"}}) || stdio.URL != "" || len(stdio.Headers) != 0 {
		t.Errorf("added stdio server = %+v", stdio)
	}
	status, text := mcpRequest(t, server, http.MethodGet, mcpServersPath, "")
	if status != 200 {
		t.Fatalf("GET: status %d: %s", status, text)
	}
	listed := decodeMcp[[]mcpServer](t, text)
	if len(listed) != 2 || listed[0].ID != stdio.ID || listed[1].ID != remote.ID {
		t.Errorf("listed = %+v, want files then makeadir", listed)
	}
	for _, secret := range []string{"secret-token", "site-secret", "env-secret"} {
		if strings.Contains(text, secret) {
			t.Errorf("the list shows the secret %q: %s", secret, text)
		}
	}
}

func TestAnEmptyValueInAChangeKeepsTheStoredValueAndANewValueReplacesIt(t *testing.T) {
	server := startWithSettings(t)
	added := addMcpServer(t, server, "owner", makeadirBody)
	path := mcpServersPath + "/" + itoa(added.ID)

	keep := `{"name":"makeadir","kind":"http","url":"https://makeadir.example.com/mcp","headers":[{"name":"Authorization","value":""},{"name":"X-Site","value":"new-site-secret"}]}`
	status, text := mcpRequest(t, server, http.MethodPut, path, keep)
	if status != http.StatusOK {
		t.Fatalf("PUT: status %d: %s", status, text)
	}
	changed := decodeMcp[mcpServer](t, text)
	if !slices.Equal(changed.Headers, []mcpName{{"Authorization"}, {"X-Site"}}) || strings.Contains(text, "secret") {
		t.Errorf("changed server = %s", text)
	}
	want := `[{"name":"Authorization","value":"Bearer secret-token"},{"name":"X-Site","value":"new-site-secret"}]`
	if got := storedMcpHeaders(t, server, added.ID); got != want {
		t.Errorf("stored headers = %s, want %s", got, want)
	}

	newEmpty := `{"name":"makeadir","kind":"http","url":"https://makeadir.example.com/mcp","headers":[{"name":"Authorization","value":""},{"name":"X-New","value":""}]}`
	if status, text := mcpRequest(t, server, http.MethodPut, path, newEmpty); status != http.StatusUnprocessableEntity {
		t.Errorf("PUT with a new header with no value: status %d, want 422: %s", status, text)
	}
	if got := storedMcpHeaders(t, server, added.ID); got != want {
		t.Errorf("stored headers after the refused PUT = %s, want %s", got, want)
	}
}

func TestTheRulesOfAnMcpServerGive422Or409Or404(t *testing.T) {
	server := startWithSettings(t)
	first := addMcpServer(t, server, "owner", makeadirBody)
	addMcpServer(t, server, "owner", filesBody)
	other := addMcpServer(t, server, "other", `{"name":"makeadir","kind":"http","url":"https://other.example.com/mcp"}`)
	valid := `"kind":"http","url":"https://example.com/mcp"`
	for _, test := range []struct {
		name, body string
		want       int
	}{
		{"a name with a space", `{"name":"my server",` + valid + `}`, 422},
		{"an empty name", `{"name":"",` + valid + `}`, 422},
		{"the name mobius", `{"name":"mobius",` + valid + `}`, 422},
		{"the name Mobius", `{"name":"Mobius",` + valid + `}`, 422},
		{"an unknown kind", `{"name":"a","kind":"sse","url":"https://example.com/mcp"}`, 422},
		{"a relative url", `{"name":"a","kind":"http","url":"/mcp"}`, 422},
		{"a url with no host", `{"name":"a","kind":"http","url":"https:///mcp"}`, 422},
		{"a url with another scheme", `{"name":"a","kind":"http","url":"ftp://example.com/mcp"}`, 422},
		{"an empty url", `{"name":"a","kind":"http","url":""}`, 422},
		{"an empty command", `{"name":"a","kind":"stdio","command":"  "}`, 422},
		{"an empty header name", `{"name":"a",` + valid + `,"headers":[{"name":"","value":"x"}]}`, 422},
		{"a header name twice", `{"name":"a",` + valid + `,"headers":[{"name":"X","value":"1"},{"name":"X","value":"2"}]}`, 422},
		{"an empty env name", `{"name":"a","kind":"stdio","command":"x","env":[{"name":"","value":"x"}]}`, 422},
		{"an env name twice", `{"name":"a","kind":"stdio","command":"x","env":[{"name":"X","value":"1"},{"name":"X","value":"2"}]}`, 422},
		{"a new header with an empty value", `{"name":"a",` + valid + `,"headers":[{"name":"X","value":""}]}`, 422},
		{"the name of another server", `{"name":"files",` + valid + `}`, 409},
	} {
		if status, text := mcpRequest(t, server, http.MethodPost, mcpServersPath, test.body); status != test.want {
			t.Errorf("POST with %s: status %d, want %d: %s", test.name, status, test.want, text)
		}
	}
	if status, text := mcpRequest(t, server, http.MethodPut, mcpServersPath+"/"+itoa(first.ID), `{"name":"files",`+valid+`}`); status != http.StatusConflict {
		t.Errorf("PUT with the name of another server: status %d, want 409: %s", status, text)
	}
	if status, text := mcpRequest(t, server, http.MethodPut, mcpServersPath+"/"+itoa(first.ID), `{"name":"a","kind":"http","url":"/mcp"}`); status != http.StatusUnprocessableEntity {
		t.Errorf("PUT with a relative url: status %d, want 422: %s", status, text)
	}
	if status, text := mcpRequest(t, server, http.MethodPut, mcpServersPath+"/999", `{"name":"a",`+valid+`}`); status != http.StatusNotFound {
		t.Errorf("PUT with an unknown id: status %d, want 404: %s", status, text)
	}
	if status, text := mcpRequest(t, server, http.MethodPut, mcpServersPath+"/"+itoa(other.ID), `{"name":"a",`+valid+`}`); status != http.StatusNotFound {
		t.Errorf("PUT with an id of another organization: status %d, want 404: %s", status, text)
	}
	if status, text := mcpRequest(t, server, http.MethodDelete, mcpServersPath+"/"+itoa(other.ID), ""); status != http.StatusNotFound {
		t.Errorf("DELETE with an id of another organization: status %d, want 404: %s", status, text)
	}
	if status, text := mcpRequest(t, server, http.MethodDelete, mcpServersPath+"/999", ""); status != http.StatusNotFound {
		t.Errorf("DELETE with an unknown id: status %d, want 404: %s", status, text)
	}
}

func TestTheSelectionOfAWorkstreamIsInTheDetailsAndADeleteOfAServerRemovesIt(t *testing.T) {
	server := startWithSettings(t)
	makeadir := addMcpServer(t, server, "owner", makeadirBody)
	files := addMcpServer(t, server, "owner", filesBody)
	other := addMcpServer(t, server, "other", `{"name":"makeadir","kind":"http","url":"https://other.example.com/mcp"}`)
	if got := selectedMcpIDs(t, server); len(got) != 0 {
		t.Fatalf("details before the selection: mcpServers = %v", got)
	}

	body := `{"ids":[` + itoa(makeadir.ID) + `,` + itoa(files.ID) + `]}`
	if status, text := mcpRequest(t, server, http.MethodPut, selectionPath, body); status != http.StatusNoContent {
		t.Fatalf("PUT selection: status %d: %s", status, text)
	}

	if got, want := selectedMcpIDs(t, server), []int64{files.ID, makeadir.ID}; !slices.Equal(got, want) {
		t.Errorf("mcpServers = %v, want %v (name order)", got, want)
	}
	if status, text := mcpRequest(t, server, http.MethodPut, selectionPath, `{"ids":[`+itoa(files.ID)+`,`+itoa(other.ID)+`]}`); status != http.StatusUnprocessableEntity {
		t.Errorf("PUT selection with a server of another organization: status %d, want 422: %s", status, text)
	}
	if got, want := selectedMcpIDs(t, server), []int64{files.ID, makeadir.ID}; !slices.Equal(got, want) {
		t.Errorf("mcpServers after the refused PUT = %v, want %v", got, want)
	}
	for _, path := range []string{"/api/workstreams/owner/shop/41/mcp-servers", "/api/workstreams/owner/garden/12/mcp-servers"} {
		if status, text := mcpRequest(t, server, http.MethodPut, path, `{"ids":[]}`); status != http.StatusNotFound {
			t.Errorf("PUT %s: status %d, want 404: %s", path, status, text)
		}
	}
	if status, text := mcpRequest(t, server, http.MethodPut, selectionPath, `{"ids":[`+itoa(files.ID)+`]}`); status != http.StatusNoContent {
		t.Fatalf("PUT selection: status %d: %s", status, text)
	}
	if got, want := selectedMcpIDs(t, server), []int64{files.ID}; !slices.Equal(got, want) {
		t.Errorf("mcpServers after a new selection = %v, want %v", got, want)
	}

	if status, text := mcpRequest(t, server, http.MethodDelete, mcpServersPath+"/"+itoa(files.ID), ""); status != http.StatusNoContent {
		t.Fatalf("DELETE: status %d: %s", status, text)
	}

	if got := selectedMcpIDs(t, server); len(got) != 0 {
		t.Errorf("mcpServers after the delete = %v, want none", got)
	}
}

func TestWorkstreamMcpServersGivesTheHttpServersBeforeTheStdioServersWithTheSecrets(t *testing.T) {
	server := startWithSettings(t)
	var ids []string
	for _, body := range []string{
		`{"name":"b-files","kind":"stdio","command":"b-server","args":["-x"],"env":[{"name":"KEY","value":"env-secret"}]}`,
		`{"name":"z-site","kind":"http","url":"https://z.example.com/mcp","headers":[{"name":"Authorization","value":"Bearer z-secret"}]}`,
		`{"name":"a-files","kind":"stdio","command":"a-server"}`,
		`{"name":"c-site","kind":"http","url":"https://c.example.com/mcp"}`,
		`{"name":"unselected","kind":"http","url":"https://u.example.com/mcp"}`,
	} {
		added := addMcpServer(t, server, "owner", body)
		if added.Name != "unselected" {
			ids = append(ids, itoa(added.ID))
		}
	}
	if status, text := mcpRequest(t, server, http.MethodPut, selectionPath, `{"ids":[`+strings.Join(ids, ",")+`]}`); status != http.StatusNoContent {
		t.Fatalf("PUT selection: status %d: %s", status, text)
	}

	servers, err := server.Engine.WorkstreamMcpServers(t.Context(), shop, 12)

	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, found := range servers {
		names = append(names, found.Name)
	}
	if want := []string{"c-site", "z-site", "a-files", "b-files"}; !slices.Equal(names, want) {
		t.Fatalf("servers = %v, want %v", names, want)
	}
	if want := []engine.McpPair{{Name: "Authorization", Value: "Bearer z-secret"}}; !slices.Equal(servers[1].Headers, want) || servers[1].URL != "https://z.example.com/mcp" {
		t.Errorf("z-site = %+v", servers[1])
	}
	if want := []engine.McpPair{{Name: "KEY", Value: "env-secret"}}; !slices.Equal(servers[3].Env, want) || servers[3].Command != "b-server" || !slices.Equal(servers[3].Args, []string{"-x"}) {
		t.Errorf("b-files = %+v", servers[3])
	}
}

func TestAPairOfTheOtherKindIsIgnoredAndAServerWithNoArgsGivesEmptyLists(t *testing.T) {
	server := startWithSettings(t)
	stdio := addMcpServer(t, server, "owner", `{"name":"files","kind":"stdio","command":"files-server"}`)
	remote := addMcpServer(t, server, "owner", `{"name":"makeadir","kind":"http","url":"https://makeadir.example.com/mcp"}`)

	status, text := mcpRequest(t, server, http.MethodPut, mcpServersPath+"/"+itoa(stdio.ID),
		`{"name":"files","kind":"stdio","command":"files-server","headers":[{"name":"X","value":""}]}`)
	if status != http.StatusOK {
		t.Fatalf("PUT stdio with a header with no value: status %d: %s", status, text)
	}
	if !strings.Contains(text, `"args":[]`) || !strings.Contains(text, `"headers":[]`) || !strings.Contains(text, `"env":[]`) {
		t.Errorf("stdio server with no args = %s, want empty lists", text)
	}
	status, text = mcpRequest(t, server, http.MethodPut, mcpServersPath+"/"+itoa(remote.ID),
		`{"name":"makeadir","kind":"http","url":"https://makeadir.example.com/mcp","env":[{"name":"X","value":""}]}`)
	if status != http.StatusOK {
		t.Fatalf("PUT http with an env variable with no value: status %d: %s", status, text)
	}
	status, text = mcpRequest(t, server, http.MethodGet, mcpServersPath, "")
	if status != http.StatusOK || strings.Contains(text, "null") {
		t.Errorf("GET: status %d, body %s, want no null", status, text)
	}
}
