package api_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	oauthUIURL = "https://mobius.example.com"
	oauthCode  = "the-code"
)

// fakeOAuth is an MCP server at /mcp that asks for authorization, and the authorization server of that MCP server.
type fakeOAuth struct {
	*httptest.Server
	documents bool
	register  bool
	issuer    bool
	// newClients makes each registration give a new client ID.
	newClients bool
	// badRegistration makes the registration endpoint answer with a body that does not decode.
	badRegistration bool

	mu            sync.Mutex
	registrations []string
	tokenForm     url.Values
	tokenUser     string
	tokenPassword string
}

func newFakeOAuth(t *testing.T, documents, register, issuer bool) *fakeOAuth {
	t.Helper()
	fake := &fakeOAuth{documents: documents, register: register, issuer: issuer}
	mux := http.NewServeMux()
	fake.Server = httptest.NewServer(mux)
	t.Cleanup(fake.Close)
	mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp", scope="files:read"`, fake.URL))
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, _ *http.Request) {
		fake.json(w, http.StatusOK, map[string]any{"resource": fake.URL + "/mcp", "authorization_servers": []string{fake.URL}, "scopes_supported": []string{"files:read", "files:write"}})
	})
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		metadata := map[string]any{
			"issuer": fake.URL, "authorization_endpoint": fake.URL + "/authorize", "token_endpoint": fake.URL + "/token",
			"response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"},
			"scopes_supported": []string{"files:read", "offline_access"}, "client_id_metadata_document_supported": fake.documents,
			"authorization_response_iss_parameter_supported": fake.issuer,
		}
		if fake.register {
			metadata["registration_endpoint"] = fake.URL + "/register"
		}
		fake.json(w, http.StatusOK, metadata)
	})
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fake.mu.Lock()
		fake.registrations = append(fake.registrations, string(body))
		fake.mu.Unlock()
		if fake.badRegistration {
			fake.json(w, http.StatusCreated, map[string]any{"client_id": "c", "client_secret": "s3cret", "client_id_issued_at": "2026-11-09"})
			return
		}
		clientID := "registered-client"
		if fake.newClients {
			clientID = fmt.Sprintf("client-%d", len(fake.registered()))
		}
		fake.json(w, http.StatusCreated, map[string]any{"client_id": clientID, "client_secret": "registered-secret"})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		fake.mu.Lock()
		fake.tokenForm = r.PostForm
		fake.tokenUser, fake.tokenPassword, _ = r.BasicAuth()
		fake.mu.Unlock()
		if r.PostForm.Get("code") != oauthCode {
			fake.json(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
			return
		}
		fake.json(w, http.StatusOK, map[string]any{"access_token": "access-1", "refresh_token": "refresh-1", "token_type": "Bearer", "expires_in": 3600})
	})
	return fake
}

func (f *fakeOAuth) json(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeOAuth) registered() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.registrations...)
}

func (f *fakeOAuth) token() (url.Values, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenForm, f.tokenUser, f.tokenPassword
}

func startForOAuth(t *testing.T, uiURL string) *testserver.Server {
	t.Helper()
	dataDir := t.TempDir()
	cfg := testserver.Config(t, dataDir)
	cfg.UIURL = uiURL
	return testserver.StartWith(t, cfg, testkit.NewFakeGitHub(t).URL)
}

func addOAuthServer(t *testing.T, server *testserver.Server, fake *fakeOAuth) mcpServer {
	t.Helper()
	return addMcpServer(t, server, "owner", `{"name":"files","kind":"http","url":"`+fake.URL+`/mcp"}`)
}

func oauthPath(id int64) string {
	return mcpServersPath + "/" + itoa(id) + "/oauth"
}

// startOAuthFlow starts the flow and gives the query of the authorization URL.
func startOAuthFlow(t *testing.T, server *testserver.Server, id int64, body string) url.Values {
	t.Helper()
	status, text := mcpRequest(t, server, http.MethodPost, oauthPath(id), body)
	if status != http.StatusOK {
		t.Fatalf("POST oauth: status %d: %s", status, text)
	}
	address, err := url.Parse(decodeMcp[struct {
		URL string `json:"url"`
	}](t, text).URL)
	if err != nil {
		t.Fatal(err)
	}
	return address.Query()
}

// callback sends the browser of the Owner (with the login when client is the client of the server) to the callback and
// gives the status and the Location.
func callback(t *testing.T, client *http.Client, server *testserver.Server, query url.Values) (int, string) {
	t.Helper()
	once := *client
	once.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	reply, err := once.Get(server.URL + "/api/mcp-oauth/callback?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	_ = reply.Body.Close()
	return reply.StatusCode, reply.Header.Get("Location")
}

func answer(fake *fakeOAuth, authorization url.Values) url.Values {
	return url.Values{"code": {oauthCode}, "state": {authorization.Get("state")}, "iss": {fake.URL}}
}

type oauthRow struct {
	AccessToken, RefreshToken, State, CodeVerifier, Failure string
	ExpiresAt                                               sql.NullString
}

func storedOAuth(t *testing.T, server *testserver.Server, id int64) (oauthRow, bool) {
	t.Helper()
	var row oauthRow
	err := server.DB.QueryRow("SELECT access_token, refresh_token, state, code_verifier, failure, expires_at FROM mcp_server_oauth WHERE server = ?", id).
		Scan(&row.AccessToken, &row.RefreshToken, &row.State, &row.CodeVerifier, &row.Failure, &row.ExpiresAt)
	if err == sql.ErrNoRows {
		return row, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return row, true
}

type oauthState struct {
	OAuth       string `json:"oauth"`
	OAuthReason string `json:"oauthReason"`
}

func listedOAuth(t *testing.T, server *testserver.Server) (oauthState, string) {
	t.Helper()
	status, text := mcpRequest(t, server, http.MethodGet, mcpServersPath, "")
	if status != http.StatusOK {
		t.Fatalf("GET: status %d: %s", status, text)
	}
	listed := decodeMcp[[]oauthState](t, text)
	if len(listed) != 1 {
		t.Fatalf("listed = %s, want one server", text)
	}
	return listed[0], text
}

func TestTheCallbackWithTheCodeStoresTheTokensAndTheListGivesConnectedWithNoToken(t *testing.T) {
	fake := newFakeOAuth(t, false, true, true)
	server := startForOAuth(t, oauthUIURL)
	added := addOAuthServer(t, server, fake)
	if added.OAuth != "none" {
		t.Errorf("oauth of a new server = %q, want none", added.OAuth)
	}

	authorization := startOAuthFlow(t, server, added.ID, `{}`)

	if authorization.Get("code_challenge_method") != "S256" || authorization.Get("code_challenge") == "" || authorization.Get("state") == "" {
		t.Errorf("authorization query = %v, want S256, a challenge and a state", authorization)
	}
	if authorization.Get("resource") != fake.URL+"/mcp" || authorization.Get("redirect_uri") != oauthUIURL+"/api/mcp-oauth/callback" {
		t.Errorf("authorization query = %v, want the resource and the redirect URI", authorization)
	}
	if authorization.Get("client_id") != "registered-client" || authorization.Get("response_type") != "code" {
		t.Errorf("authorization query = %v, want the registered client and response_type code", authorization)
	}
	if authorization.Get("scope") != "files:read offline_access" {
		t.Errorf("scope = %q, want the scope of the challenge and offline_access", authorization.Get("scope"))
	}
	registered := fake.registered()
	if len(registered) != 1 || !strings.Contains(registered[0], oauthUIURL+"/api/mcp-oauth/callback") {
		t.Errorf("registrations = %v, want one with the redirect URI", registered)
	}
	if state, _ := listedOAuth(t, server); state.OAuth != "none" {
		t.Errorf("oauth while the flow waits = %q, want none", state.OAuth)
	}

	status, location := callback(t, server.Client, server, answer(fake, authorization))

	if status != http.StatusSeeOther || location != "/settings/mcp-servers?oauth=connected&server="+itoa(added.ID) {
		t.Errorf("callback: status %d, Location %q", status, location)
	}
	row, found := storedOAuth(t, server, added.ID)
	if !found || row.AccessToken != "access-1" || row.RefreshToken != "refresh-1" || row.State != "" || row.CodeVerifier != "" || row.Failure != "" || !row.ExpiresAt.Valid {
		t.Errorf("stored row = %+v, found %v", row, found)
	}
	form, user, password := fake.token()
	verifier := sha256.Sum256([]byte(form.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(verifier[:]) != authorization.Get("code_challenge") || form.Get("code_verifier") == "" {
		t.Errorf("token form = %v, want the verifier of the challenge", form)
	}
	if form.Get("grant_type") != "authorization_code" || form.Get("resource") != fake.URL+"/mcp" || form.Get("redirect_uri") != oauthUIURL+"/api/mcp-oauth/callback" {
		t.Errorf("token form = %v", form)
	}
	if form.Get("client_id") != "registered-client" && user != "registered-client" {
		t.Errorf("token request has no client id: form %v, user %q", form, user)
	}
	if form.Get("client_secret") != "registered-secret" && password != "registered-secret" {
		t.Error("token request has no client secret")
	}
	state, text := listedOAuth(t, server)
	if state.OAuth != "connected" || state.OAuthReason != "" {
		t.Errorf("listed = %+v, want connected", state)
	}
	for _, secret := range []string{"access-1", "refresh-1", "registered-secret", authorization.Get("state"), form.Get("code_verifier"), oauthCode} {
		if strings.Contains(text, secret) || strings.Contains(location, secret) {
			t.Errorf("the list or the redirect shows %q", secret)
		}
	}
}

func TestWithTheDocumentSupportTheClientIDIsTheClientDocumentOfMobiusThatNeedsNoLogin(t *testing.T) {
	fake := newFakeOAuth(t, true, true, false)
	server := startForOAuth(t, oauthUIURL)
	added := addOAuthServer(t, server, fake)

	authorization := startOAuthFlow(t, server, added.ID, `{}`)

	if authorization.Get("client_id") != oauthUIURL+"/api/mcp-oauth/client" {
		t.Errorf("client_id = %q, want the client document", authorization.Get("client_id"))
	}
	if registered := fake.registered(); len(registered) != 0 {
		t.Errorf("registrations = %v, want none", registered)
	}
	anonymous := &http.Client{Transport: server.Client.Transport}
	reply, err := anonymous.Get(server.URL + "/api/mcp-oauth/client")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reply.Body.Close() }()
	var document struct {
		ClientID                string   `json:"client_id"`
		ClientName              string   `json:"client_name"`
		RedirectURIs            []string `json:"redirect_uris"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(reply.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%s|Mobius|%s|authorization_code refresh_token|code|none", oauthUIURL+"/api/mcp-oauth/client", oauthUIURL+"/api/mcp-oauth/callback")
	got := fmt.Sprintf("%s|%s|%s|%s|%s|%s", document.ClientID, document.ClientName, strings.Join(document.RedirectURIs, " "),
		strings.Join(document.GrantTypes, " "), strings.Join(document.ResponseTypes, " "), document.TokenEndpointAuthMethod)
	if reply.StatusCode != http.StatusOK || got != want {
		t.Errorf("client document: status %d, %s, want %s", reply.StatusCode, got, want)
	}
}

func TestACallbackWithNoDeviceLoginGives401AndStoresNoToken(t *testing.T) {
	fake := newFakeOAuth(t, false, true, true)
	server := startForOAuth(t, oauthUIURL)
	added := addOAuthServer(t, server, fake)
	authorization := startOAuthFlow(t, server, added.ID, `{}`)

	status, _ := callback(t, &http.Client{Transport: server.Client.Transport}, server, answer(fake, authorization))

	if status != http.StatusUnauthorized {
		t.Errorf("callback with no login: status %d, want 401", status)
	}
	if row, _ := storedOAuth(t, server, added.ID); row.AccessToken != "" || row.State != authorization.Get("state") {
		t.Errorf("stored row = %+v, want no token and the waiting flow", row)
	}
	if form, _, _ := fake.token(); form != nil {
		t.Errorf("the token endpoint got a request: %v", form)
	}
}

func TestAClientIDInTheRequestIsUsedAndNothingIsRegistered(t *testing.T) {
	fake := newFakeOAuth(t, true, true, true)
	server := startForOAuth(t, oauthUIURL)
	added := addOAuthServer(t, server, fake)

	authorization := startOAuthFlow(t, server, added.ID, `{"clientId":"owner-client","clientSecret":"owner-secret"}`)
	status, location := callback(t, server.Client, server, answer(fake, authorization))

	if authorization.Get("client_id") != "owner-client" {
		t.Errorf("client_id = %q, want owner-client", authorization.Get("client_id"))
	}
	if registered := fake.registered(); len(registered) != 0 {
		t.Errorf("registrations = %v, want none", registered)
	}
	if status != http.StatusSeeOther || !strings.Contains(location, "oauth=connected") {
		t.Errorf("callback: status %d, Location %q", status, location)
	}
	form, user, password := fake.token()
	if form.Get("client_id") != "owner-client" && user != "owner-client" {
		t.Errorf("token request has no client id: form %v, user %q", form, user)
	}
	if form.Get("client_secret") != "owner-secret" && password != "owner-secret" {
		t.Error("token request has no client secret")
	}
	if _, text := listedOAuth(t, server); strings.Contains(text, "owner-secret") {
		t.Errorf("the list shows the client secret: %s", text)
	}
}

func TestAServerWithNoWayToGetAClientIDGives422(t *testing.T) {
	fake := newFakeOAuth(t, false, false, true)
	server := startForOAuth(t, oauthUIURL)
	added := addOAuthServer(t, server, fake)

	status, text := mcpRequest(t, server, http.MethodPost, oauthPath(added.ID), `{}`)

	if status != http.StatusUnprocessableEntity || !strings.Contains(text, "client ID") {
		t.Errorf("start: status %d, body %s, want 422 with the reason", status, text)
	}
	if _, found := storedOAuth(t, server, added.ID); found {
		t.Error("a row was stored for a flow that did not start")
	}
}

func TestACallbackWithAWrongStateAnOldFlowOrAWrongIssStoresNoTokenAndRedirectsWithFailed(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(server *testserver.Server, query url.Values)
		reason string
	}{
		{"a wrong state", func(_ *testserver.Server, query url.Values) { query.Set("state", "wrong") }, "No connection waits"},
		{"an old flow", func(server *testserver.Server, _ url.Values) {
			server.Engine.SetClock(func() time.Time { return time.Now().Add(11 * time.Minute) })
		}, "more than 10 minutes"},
		{"a wrong iss", func(_ *testserver.Server, query url.Values) { query.Set("iss", "https://evil.example.com") }, "wrong issuer"},
		{"no iss from a server that sends it", func(_ *testserver.Server, query url.Values) { query.Del("iss") }, "wrong issuer"},
		{"an error of the authorization server", func(_ *testserver.Server, query url.Values) {
			query.Del("code")
			query.Set("error", "access_denied")
		}, "did not allow"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newFakeOAuth(t, false, true, true)
			server := startForOAuth(t, oauthUIURL)
			added := addOAuthServer(t, server, fake)
			authorization := startOAuthFlow(t, server, added.ID, `{}`)
			query := answer(fake, authorization)
			test.change(server, query)

			status, location := callback(t, server.Client, server, query)

			address, err := url.Parse(location)
			if err != nil {
				t.Fatal(err)
			}
			if status != http.StatusSeeOther || address.Path != "/settings/mcp-servers" || address.Query().Get("oauth") != "failed" || !strings.Contains(address.Query().Get("reason"), test.reason) {
				t.Errorf("callback: status %d, Location %q, want oauth=failed with the reason %q", status, location, test.reason)
			}
			if row, _ := storedOAuth(t, server, added.ID); row.AccessToken != "" || row.RefreshToken != "" {
				t.Errorf("stored row = %+v, want no token", row)
			}
			if form, _, _ := fake.token(); form != nil {
				t.Errorf("the token endpoint got a request: %v", form)
			}
			if test.name == "a wrong state" {
				if address.Query().Get("server") != "" {
					t.Errorf("Location %q names a server for an unknown state", location)
				}
				return
			}
			if address.Query().Get("server") != itoa(added.ID) {
				t.Errorf("Location %q, want server=%d", location, added.ID)
			}
			if state, _ := listedOAuth(t, server); state.OAuth != "reconnect" || !strings.Contains(state.OAuthReason, test.reason) {
				t.Errorf("listed = %+v, want reconnect with the reason %q", state, test.reason)
			}
		})
	}
}

func TestTheStartRefusesAStdioServerAnEmptyUIURLAnHTTPUIURLAndAnAuthorizationHeader(t *testing.T) {
	fake := newFakeOAuth(t, false, true, true)
	server := startForOAuth(t, oauthUIURL)
	stdio := addMcpServer(t, server, "owner", filesBody)
	withHeader := addMcpServer(t, server, "owner", `{"name":"keyed","kind":"http","url":"`+fake.URL+`/mcp","headers":[{"name":"authorization","value":"Bearer x"}]}`)
	for name, id := range map[string]int64{"a stdio server": stdio.ID, "a server with a header authorization": withHeader.ID} {
		if status, text := mcpRequest(t, server, http.MethodPost, oauthPath(id), `{}`); status != http.StatusUnprocessableEntity {
			t.Errorf("start for %s: status %d, want 422: %s", name, status, text)
		}
		if _, found := storedOAuth(t, server, id); found {
			t.Errorf("a row was stored for %s", name)
		}
	}
	if status, text := mcpRequest(t, server, http.MethodPost, oauthPath(999), `{}`); status != http.StatusNotFound {
		t.Errorf("start for an unknown id: status %d, want 404: %s", status, text)
	}
	for _, uiURL := range []string{"", "http://mobius.example.com"} {
		server := startForOAuth(t, uiURL)
		added := addOAuthServer(t, server, fake)
		if status, text := mcpRequest(t, server, http.MethodPost, oauthPath(added.ID), `{}`); status != http.StatusUnprocessableEntity || !strings.Contains(text, "ui_url") {
			t.Errorf("start with ui_url %q: status %d, want 422 with ui_url: %s", uiURL, status, text)
		}
		if _, found := storedOAuth(t, server, added.ID); found {
			t.Errorf("a row was stored with ui_url %q", uiURL)
		}
	}
}

func TestTheDeleteOfTheOAuthRemovesTheTokensAndTheServerGivesNone(t *testing.T) {
	fake := newFakeOAuth(t, false, true, true)
	server := startForOAuth(t, oauthUIURL)
	added := addOAuthServer(t, server, fake)
	authorization := startOAuthFlow(t, server, added.ID, `{}`)
	if status, location := callback(t, server.Client, server, answer(fake, authorization)); status != http.StatusSeeOther || !strings.Contains(location, "oauth=connected") {
		t.Fatalf("callback: status %d, Location %q", status, location)
	}
	if state, _ := listedOAuth(t, server); state.OAuth != "connected" {
		t.Fatalf("oauth before the delete = %q, want connected", state.OAuth)
	}

	if status, text := mcpRequest(t, server, http.MethodDelete, oauthPath(added.ID), ""); status != http.StatusNoContent {
		t.Fatalf("DELETE oauth: status %d: %s", status, text)
	}

	if _, found := storedOAuth(t, server, added.ID); found {
		t.Error("the row of the tokens stays after the delete")
	}
	if state, _ := listedOAuth(t, server); state.OAuth != "none" {
		t.Errorf("oauth after the delete = %q, want none", state.OAuth)
	}
	if status, text := mcpRequest(t, server, http.MethodDelete, oauthPath(999), ""); status != http.StatusNotFound {
		t.Errorf("DELETE oauth for an unknown id: status %d, want 404: %s", status, text)
	}
}

func TestANewStartKeepsTheTokensUntilTheNewFlowStoresNewTokens(t *testing.T) {
	fake := newFakeOAuth(t, false, true, true)
	server := startForOAuth(t, oauthUIURL)
	added := addOAuthServer(t, server, fake)
	first := startOAuthFlow(t, server, added.ID, `{}`)
	if status, location := callback(t, server.Client, server, answer(fake, first)); status != http.StatusSeeOther || !strings.Contains(location, "oauth=connected") {
		t.Fatalf("callback: status %d, Location %q", status, location)
	}

	second := startOAuthFlow(t, server, added.ID, `{}`)

	row, _ := storedOAuth(t, server, added.ID)
	if row.AccessToken != "access-1" || row.RefreshToken != "refresh-1" || row.State != second.Get("state") || row.State == first.Get("state") {
		t.Errorf("stored row after the second start = %+v, want the old tokens and the new state", row)
	}
	if state, _ := listedOAuth(t, server); state.OAuth != "connected" {
		t.Errorf("oauth while the second flow waits = %q, want connected", state.OAuth)
	}
	if status, location := callback(t, server.Client, server, answer(fake, first)); status != http.StatusSeeOther || !strings.Contains(location, "oauth=failed") {
		t.Errorf("callback of the replaced flow: status %d, Location %q, want oauth=failed", status, location)
	}
}

func TestAChangeOfTheURLOrANewAuthorizationHeaderRemovesTheOAuthConnection(t *testing.T) {
	for _, test := range []struct{ name, body string }{
		{"a new URL", `{"name":"files","kind":"http","url":"https://other.example.com/mcp"}`},
		{"a header Authorization", `{"name":"files","kind":"http","url":"%s/mcp","headers":[{"name":"authorization","value":"Bearer x"}]}`},
		{"a stdio kind", `{"name":"files","kind":"stdio","command":"files-server"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newFakeOAuth(t, false, true, true)
			server := startForOAuth(t, oauthUIURL)
			added := addOAuthServer(t, server, fake)
			authorization := startOAuthFlow(t, server, added.ID, `{}`)
			if status, location := callback(t, server.Client, server, answer(fake, authorization)); status != http.StatusSeeOther || !strings.Contains(location, "oauth=connected") {
				t.Fatalf("callback: status %d, Location %q", status, location)
			}

			status, text := mcpRequest(t, server, http.MethodPut, mcpServersPath+"/"+itoa(added.ID), strings.ReplaceAll(test.body, "%s", fake.URL))

			if status != http.StatusOK || decodeMcp[mcpServer](t, text).OAuth != "none" {
				t.Errorf("PUT: status %d, body %s, want oauth none", status, text)
			}
			if _, found := storedOAuth(t, server, added.ID); found {
				t.Error("the row of the tokens stays after the change")
			}
			if state, _ := listedOAuth(t, server); state.OAuth != "none" {
				t.Errorf("oauth after the change = %q, want none", state.OAuth)
			}
		})
	}
}

func TestAChangeThatKeepsTheURLKeepsTheOAuthConnection(t *testing.T) {
	fake := newFakeOAuth(t, false, true, true)
	server := startForOAuth(t, oauthUIURL)
	added := addOAuthServer(t, server, fake)
	authorization := startOAuthFlow(t, server, added.ID, `{}`)
	if status, location := callback(t, server.Client, server, answer(fake, authorization)); status != http.StatusSeeOther || !strings.Contains(location, "oauth=connected") {
		t.Fatalf("callback: status %d, Location %q", status, location)
	}

	status, text := mcpRequest(t, server, http.MethodPut, mcpServersPath+"/"+itoa(added.ID), `{"name":"renamed","kind":"http","url":"`+fake.URL+`/mcp","headers":[{"name":"X-Site","value":"site"}]}`)

	if status != http.StatusOK || decodeMcp[mcpServer](t, text).OAuth != "connected" {
		t.Errorf("PUT: status %d, body %s, want oauth connected", status, text)
	}
	if row, found := storedOAuth(t, server, added.ID); !found || row.AccessToken != "access-1" {
		t.Errorf("stored row = %+v, found %v, want the tokens", row, found)
	}
}

func TestARegistrationThatDoesNotDecodeGives422WithNoSecret(t *testing.T) {
	fake := newFakeOAuth(t, false, true, true)
	fake.badRegistration = true
	server := startForOAuth(t, oauthUIURL)
	added := addOAuthServer(t, server, fake)

	status, text := mcpRequest(t, server, http.MethodPost, oauthPath(added.ID), `{}`)

	if status != http.StatusUnprocessableEntity || !strings.Contains(text, "registration") {
		t.Errorf("start: status %d, body %s, want 422 with the reason", status, text)
	}
	if strings.Contains(text, "s3cret") {
		t.Errorf("the 422 text shows the client secret: %s", text)
	}
	if _, found := storedOAuth(t, server, added.ID); found {
		t.Error("a row was stored for a flow that did not start")
	}
}

func TestANewStartUsesTheClientOfTheStoredTokensAndAnotherClientRemovesThem(t *testing.T) {
	fake := newFakeOAuth(t, false, true, true)
	fake.newClients = true
	server := startForOAuth(t, oauthUIURL)
	added := addOAuthServer(t, server, fake)
	first := startOAuthFlow(t, server, added.ID, `{}`)
	if status, location := callback(t, server.Client, server, answer(fake, first)); status != http.StatusSeeOther || !strings.Contains(location, "oauth=connected") {
		t.Fatalf("callback: status %d, Location %q", status, location)
	}

	second := startOAuthFlow(t, server, added.ID, `{}`)

	if first.Get("client_id") != "client-1" || second.Get("client_id") != "client-1" {
		t.Errorf("client_id of the first and the second start = %q, %q, want client-1 twice", first.Get("client_id"), second.Get("client_id"))
	}
	if registered := fake.registered(); len(registered) != 1 {
		t.Errorf("registrations = %d, want 1", len(registered))
	}
	var clientID string
	if err := server.DB.QueryRow("SELECT client_id FROM mcp_server_oauth WHERE server = ?", added.ID).Scan(&clientID); err != nil || clientID != "client-1" {
		t.Errorf("stored client_id = %q (%v), want client-1", clientID, err)
	}
	if row, _ := storedOAuth(t, server, added.ID); row.AccessToken != "access-1" {
		t.Errorf("stored row = %+v, want the old tokens", row)
	}

	third := startOAuthFlow(t, server, added.ID, `{"clientId":"owner-client"}`)

	if third.Get("client_id") != "owner-client" {
		t.Errorf("client_id = %q, want owner-client", third.Get("client_id"))
	}
	if row, _ := storedOAuth(t, server, added.ID); row.AccessToken != "" || row.RefreshToken != "" || row.State != third.Get("state") {
		t.Errorf("stored row = %+v, want no token and the new flow", row)
	}
	if state, _ := listedOAuth(t, server); state.OAuth != "none" {
		t.Errorf("oauth after the start with another client = %q, want none", state.OAuth)
	}
}

func TestAFailedNewFlowKeepsAConnectedServerConnected(t *testing.T) {
	fake := newFakeOAuth(t, false, true, true)
	server := startForOAuth(t, oauthUIURL)
	added := addOAuthServer(t, server, fake)
	first := startOAuthFlow(t, server, added.ID, `{}`)
	if status, location := callback(t, server.Client, server, answer(fake, first)); status != http.StatusSeeOther || !strings.Contains(location, "oauth=connected") {
		t.Fatalf("callback: status %d, Location %q", status, location)
	}
	second := startOAuthFlow(t, server, added.ID, `{}`)

	status, location := callback(t, server.Client, server, url.Values{"state": {second.Get("state")}, "error": {"access_denied"}})

	if status != http.StatusSeeOther || !strings.Contains(location, "oauth=failed") || !strings.Contains(location, "server="+itoa(added.ID)) {
		t.Errorf("callback: status %d, Location %q, want oauth=failed", status, location)
	}
	if state, _ := listedOAuth(t, server); state.OAuth != "connected" || state.OAuthReason != "" {
		t.Errorf("listed = %+v, want connected", state)
	}
	row, _ := storedOAuth(t, server, added.ID)
	if row.AccessToken != "access-1" || row.RefreshToken != "refresh-1" || row.State != "" || row.CodeVerifier != "" || row.Failure != "" {
		t.Errorf("stored row = %+v, want the tokens, no flow and no failure", row)
	}
}

func TestAStartWithTheStoredClientIDAndANewSecretUsesTheNewSecretAndKeepsTheTokens(t *testing.T) {
	fake := newFakeOAuth(t, false, true, true)
	server := startForOAuth(t, oauthUIURL)
	added := addOAuthServer(t, server, fake)
	first := startOAuthFlow(t, server, added.ID, `{"clientId":"owner-client","clientSecret":"old"}`)
	if status, location := callback(t, server.Client, server, answer(fake, first)); status != http.StatusSeeOther || !strings.Contains(location, "oauth=connected") {
		t.Fatalf("callback: status %d, Location %q", status, location)
	}

	second := startOAuthFlow(t, server, added.ID, `{"clientId":"owner-client","clientSecret":"new"}`)

	if row, _ := storedOAuth(t, server, added.ID); row.AccessToken != "access-1" || row.State != second.Get("state") {
		t.Errorf("stored row = %+v, want the tokens and the new flow", row)
	}
	if status, location := callback(t, server.Client, server, answer(fake, second)); status != http.StatusSeeOther || !strings.Contains(location, "oauth=connected") {
		t.Fatalf("callback: status %d, Location %q", status, location)
	}
	form, _, password := fake.token()
	if form.Get("client_secret") != "new" && password != "new" {
		t.Errorf("token request has no new secret: form %v", form)
	}
}
