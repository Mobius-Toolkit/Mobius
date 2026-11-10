package engine_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	oauthUIURL  = "https://mobius.example.com"
	oauthCode   = "the-code"
	siteHeader  = "X-Team"
	siteTeam    = "shop-team"
	oauthServer = "files"
)

var oauthTokens = []string{"access-1", "refresh-1", "access-2", "refresh-2", "access-3", "refresh-3"}

type refreshRequest struct {
	form  url.Values
	basic bool
}

// fakeOAuth is an MCP server at /mcp that asks for authorization, and the authorization server of that server. Its
// token endpoint gives access-1 and refresh-1 for the code, and access-N and refresh-N for the Nth refresh, unless
// answer sets another answer for a refresh.
type fakeOAuth struct {
	*httptest.Server
	// registers makes the authorization server give a client ID with a secret at its registration endpoint, and not
	// support the Client ID Metadata Document.
	registers bool
	// postOnly makes the token endpoint refuse a refresh with the client in the header Authorization.
	postOnly bool

	mu        sync.Mutex
	refreshes []refreshRequest
	status    int
	body      string
	gate      chan struct{}
}

func newFakeOAuth(t *testing.T, registers bool) *fakeOAuth {
	t.Helper()
	fake := &fakeOAuth{registers: registers}
	mux := http.NewServeMux()
	fake.Server = httptest.NewServer(mux)
	t.Cleanup(fake.Close)
	mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp", scope="files:read"`, fake.URL))
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, _ *http.Request) {
		fake.json(w, http.StatusOK, map[string]any{"resource": fake.URL + "/mcp", "authorization_servers": []string{fake.URL}})
	})
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		metadata := map[string]any{
			"issuer": fake.URL, "authorization_endpoint": fake.URL + "/authorize", "token_endpoint": fake.URL + "/token",
			"response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"},
			"client_id_metadata_document_supported": !registers,
		}
		if registers {
			metadata["registration_endpoint"] = fake.URL + "/register"
		}
		fake.json(w, http.StatusOK, metadata)
	})
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, _ *http.Request) {
		fake.json(w, http.StatusCreated, map[string]any{"client_id": "registered-client", "client_secret": "registered-secret"})
	})
	mux.HandleFunc("POST /token", fake.token(t))
	return fake
}

func (f *fakeOAuth) token(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.PostForm.Get("grant_type") == "authorization_code" {
			f.json(w, http.StatusOK, map[string]any{"access_token": "access-1", "refresh_token": "refresh-1", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		_, _, basic := r.BasicAuth()
		f.mu.Lock()
		f.refreshes = append(f.refreshes, refreshRequest{form: r.PostForm, basic: basic})
		n, status, body, gate := len(f.refreshes), f.status, f.body, f.gate
		f.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		switch {
		case f.postOnly && basic:
			f.json(w, http.StatusUnauthorized, map[string]any{"error": "invalid_client"})
		case status != 0:
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		default:
			f.json(w, http.StatusOK, map[string]any{"access_token": fmt.Sprintf("access-%d", n+1), "refresh_token": fmt.Sprintf("refresh-%d", n+1), "token_type": "Bearer", "expires_in": 3600})
		}
	}
}

func (f *fakeOAuth) json(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// answer makes the token endpoint answer each next refresh with the status and the body.
func (f *fakeOAuth) answer(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.body = status, body
}

func (f *fakeOAuth) refreshed() []refreshRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.refreshes)
}

func withOAuthUIURL(cfg *config.Config) { cfg.UIURL = oauthUIURL }

// connectOAuthServer adds the http server "files" with the header X-Team to the organization, connects it with OAuth
// through the fake authorization server, and selects it for the Workstreams. It gives the id of the server.
func connectOAuthServer(t *testing.T, server *testserver.Server, fake *fakeOAuth, workstreams ...int64) int64 {
	t.Helper()
	ctx := t.Context()
	added, err := server.Engine.AddMcpServer(ctx, "owner", engine.McpServer{
		Name: oauthServer, Kind: "http", URL: fake.URL + "/mcp", Headers: []engine.McpPair{{Name: siteHeader, Value: siteTeam}},
	})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := server.Engine.StartMcpOAuth(ctx, "owner", added.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	address, err := url.Parse(authorization)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Engine.FinishMcpOAuth(ctx, oauthCode, address.Query().Get("state"), fake.URL, ""); err != nil {
		t.Fatal(err)
	}
	for _, workstream := range workstreams {
		if err := server.Engine.SetWorkstreamMcpServers(ctx, shop, workstream, []int64{added.ID}); err != nil {
			t.Fatal(err)
		}
	}
	return added.ID
}

type storedToken struct {
	access, refresh, failure string
	expires                  sql.NullString
}

func storedTokens(t *testing.T, server *testserver.Server, id int64) storedToken {
	t.Helper()
	var row storedToken
	err := server.DB.QueryRow("SELECT access_token, refresh_token, failure, expires_at FROM mcp_server_oauth WHERE server = ?", id).
		Scan(&row.access, &row.refresh, &row.failure, &row.expires)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// listedOAuth gives the OAuth state and the reason of the server "files" as the API of the MCP servers lists them.
func listedOAuth(t *testing.T, server *testserver.Server) (string, string) {
	t.Helper()
	reply, err := server.Client.Get(server.URL + "/api/organizations/owner/mcp-servers")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reply.Body.Close() }()
	var body struct {
		Data []struct {
			OAuth       string `json:"oauth"`
			OAuthReason string `json:"oauthReason"`
		} `json:"data"`
	}
	if err := json.NewDecoder(reply.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if reply.StatusCode != http.StatusOK || len(body.Data) != 1 {
		t.Fatalf("GET mcp-servers: status %d, data %+v", reply.StatusCode, body.Data)
	}
	return body.Data[0].OAuth, body.Data[0].OAuthReason
}

// tokenFreeTexts gives the texts of the Transcripts of the sessions and the prompts of the Lead, where no token may be.
func tokenFreeTexts(t *testing.T, server *testserver.Server, sessions []store.Session) []string {
	t.Helper()
	var texts []string
	for _, session := range sessions {
		for _, line := range transcript(t, server, session.ID) {
			texts = append(texts, line.Text, line.Body, line.Raw)
		}
	}
	return append(texts, leadPrompts(t, server)...)
}

func requireNoToken(t *testing.T, texts []string) {
	t.Helper()
	for _, text := range texts {
		for _, token := range oauthTokens {
			if strings.Contains(text, token) {
				t.Errorf("%s is in %s", token, text)
			}
		}
	}
}

// sentAuthorizations gives the header Authorization of the server "files" in each session/new request of the fake agents.
func sentAuthorizations(t *testing.T, dataDir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dataDir, "harnesses", "mcp_servers.*"))
	if err != nil {
		t.Fatal(err)
	}
	var values []string
	for _, file := range files {
		for _, sent := range readSentServers(t, file) {
			if sent.Name != oauthServer {
				continue
			}
			for _, header := range sent.Headers {
				if header.Name == "Authorization" {
					values = append(values, header.Value)
				}
			}
		}
	}
	return values
}

// startOperator makes the Lead start an Operator, and gives the ended Operator sessions after it.
func startOperator(t *testing.T, server *testserver.Server) []store.Session {
	t.Helper()
	sendChat(t, server, leadChat, "Start the operator.")
	return endedOperators(t, server, 1)
}

func TestAnOperatorRefreshesTheTokenOfAnOAuthServerAndSendsItAfterTheOtherHeaders(t *testing.T) {
	t.Parallel()
	fake := newFakeOAuth(t, false)
	server, dataDir := connectOperatorWith(t, testkit.NewFakeGitHub(t), operatorLead+operatorWork, withOAuthUIURL)
	id := connectOAuthServer(t, server, fake, 12)

	sessions := startOperator(t, server)

	servers := sentServers(t, dataDir)
	if want := []string{"mobius", oauthServer}; !slices.Equal(serverNames(servers), want) {
		t.Fatalf("servers = %+v, want %v", servers, want)
	}
	if want := []engine.McpPair{{Name: siteHeader, Value: siteTeam}, {Name: "Authorization", Value: "Bearer access-2"}}; !slices.Equal(servers[1].Headers, want) {
		t.Errorf("headers = %+v, want %+v", servers[1].Headers, want)
	}
	refreshes := fake.refreshed()
	if len(refreshes) != 1 {
		t.Fatalf("refreshes = %v, want one", refreshes)
	}
	form := refreshes[0].form
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "refresh-1" || form.Get("resource") != fake.URL+"/mcp" || form.Get("client_id") != oauthUIURL+"/api/mcp-oauth/client" {
		t.Errorf("refresh form = %v", form)
	}
	row := storedTokens(t, server, id)
	if row.access != "access-2" || row.refresh != "refresh-2" || row.failure != "" || !row.expires.Valid {
		t.Errorf("row = %+v, want the new tokens", row)
	}
	if state, _ := listedOAuth(t, server); state != "connected" {
		t.Errorf("oauth = %q, want connected", state)
	}
	waitForLeadPrompt(t, server, fmt.Sprintf("Report of the Operator %d", sessions[0].ID))
	requireNoToken(t, tokenFreeTexts(t, server, sessions))
}

func TestAnOperatorSendsAClientSecretInTheFormWhenTheServerRefusesItInTheHeader(t *testing.T) {
	t.Parallel()
	fake := newFakeOAuth(t, true)
	fake.postOnly = true
	server, dataDir := connectOperatorWith(t, testkit.NewFakeGitHub(t), operatorLead+operatorWork, withOAuthUIURL)
	connectOAuthServer(t, server, fake, 12)

	startOperator(t, server)

	refreshes := fake.refreshed()
	if len(refreshes) != 2 || !refreshes[0].basic || refreshes[1].basic {
		t.Fatalf("refreshes = %+v, want one in the header and one in the form", refreshes)
	}
	if form := refreshes[1].form; form.Get("client_id") != "registered-client" || form.Get("client_secret") != "registered-secret" {
		t.Errorf("refresh form = %v", form)
	}
	servers := sentServers(t, dataDir)
	if len(servers) != 2 || !slices.Contains(servers[1].Headers, engine.McpPair{Name: "Authorization", Value: "Bearer access-3"}) {
		t.Errorf("servers = %+v", servers)
	}
}

func TestTwoOperatorsOfTwoWorkstreamsRefreshTheSameTokenOneTimeAndGetTheSameToken(t *testing.T) {
	t.Parallel()
	fake := newFakeOAuth(t, false)
	fake.gate = make(chan struct{})
	github := testkit.NewFakeGitHub(t)
	github.AddIssue(shop, 13, "Shop API")
	github.AddLabel(shop, 13, "mobius:workstream", "owner")
	server, dataDir := connectOperatorWith(t, github, operatorLead+operatorWork, withOAuthUIURL)
	connectOAuthServer(t, server, fake, 12, 13)
	second := engine.ChatKey{Organization: "owner", Repository: shop, Workstream: 13}

	sendChat(t, server, leadChat, "Start the operator.")
	sendChat(t, server, second, "Start the operator.")
	testkit.WaitFor(t, func() bool {
		return len(chatSessions(t, server, leadChat, engine.OperatorRole)) == 1 && len(chatSessions(t, server, second, engine.OperatorRole)) == 1
	})
	close(fake.gate)

	testkit.WaitFor(t, func() bool {
		for _, key := range []engine.ChatKey{leadChat, second} {
			sessions := chatSessions(t, server, key, engine.OperatorRole)
			if len(sessions) != 1 || !sessions[0].EndedAt.Valid {
				return false
			}
		}
		return true
	})
	if refreshes := fake.refreshed(); len(refreshes) != 1 {
		t.Errorf("refreshes = %v, want one", refreshes)
	}
	tokens := sentAuthorizations(t, dataDir)
	if !slices.Equal(tokens, []string{"Bearer access-2", "Bearer access-2"}) {
		t.Errorf("tokens = %v, want the same new token for both Operators", tokens)
	}
}

func TestAnOperatorDoesNotStartWhenTheTokenEndpointRefusesTheRefreshTokenAndTheServerAsksForANewConnect(t *testing.T) {
	t.Parallel()
	fake := newFakeOAuth(t, false)
	fake.answer(http.StatusBadRequest, `{"error":"invalid_grant","error_description":"refresh-1 is revoked"}`)
	server, dataDir := connectOperatorWith(t, testkit.NewFakeGitHub(t), operatorLead+operatorWork, withOAuthUIURL)
	id := connectOAuthServer(t, server, fake, 12)

	sessions := startOperator(t, server)

	const reason = "the MCP server files needs a new Connect: The authorization server refused the refresh token."
	if sessions[0].EndReason.String != "failed" {
		t.Errorf("session = %+v", sessions[0])
	}
	waitForLeadPrompt(t, server, fmt.Sprintf("Report of the Operator %d:\n\nThe Operator %d failed: %s", sessions[0].ID, sessions[0].ID, reason))
	if sent := sentAuthorizations(t, dataDir); len(sent) != 0 {
		t.Errorf("a session got the server: %v", sent)
	}
	state, text := listedOAuth(t, server)
	if state != "reconnect" || text != "The authorization server refused the refresh token." {
		t.Errorf("oauth = %q, reason = %q", state, text)
	}
	if row := storedTokens(t, server, id); row.failure != text {
		t.Errorf("row = %+v", row)
	}

	sendChat(t, server, leadChat, "Start the operator.")
	endedOperators(t, server, 2)

	if refreshes := fake.refreshed(); len(refreshes) != 1 {
		t.Errorf("refreshes = %v, want only the first", refreshes)
	}
	requireNoToken(t, tokenFreeTexts(t, server, sessions))
}

func TestAnOperatorDoesNotStartWhenTheTokenEndpointFailsAndTheServerStaysConnected(t *testing.T) {
	t.Parallel()
	fake := newFakeOAuth(t, false)
	fake.answer(http.StatusServiceUnavailable, `refresh-1 access-1`)
	server, _ := connectOperatorWith(t, testkit.NewFakeGitHub(t), operatorLead+operatorWork, withOAuthUIURL)
	id := connectOAuthServer(t, server, fake, 12)

	sessions := startOperator(t, server)

	if sessions[0].EndReason.String != "failed" {
		t.Errorf("session = %+v", sessions[0])
	}
	waitForLeadPrompt(t, server, fmt.Sprintf("Report of the Operator %d:\n\nThe Operator %d failed: the MCP server files gave no access token: the authorization server answered with status 503", sessions[0].ID, sessions[0].ID))
	if state, text := listedOAuth(t, server); state != "connected" || text != "" {
		t.Errorf("oauth = %q, reason = %q", state, text)
	}
	if row := storedTokens(t, server, id); row.access != "access-1" || row.refresh != "refresh-1" || row.failure != "" {
		t.Errorf("row = %+v, want the old tokens and no failure", row)
	}
	requireNoToken(t, tokenFreeTexts(t, server, sessions))
}

// logBuffer is a log output that tests can read while other goroutines write to it.
type logBuffer struct {
	mu   sync.Mutex
	text bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.String()
}

// Serial: the test sets the output of the standard logger.
func TestNoTokenIsInTheTranscriptTheReportOrTheLogOfAnOperatorWhoseRefreshSucceedsOrFails(t *testing.T) {
	logged := &logBuffer{}
	log.SetOutput(logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	fake := newFakeOAuth(t, false)
	server, _ := connectOperatorWith(t, testkit.NewFakeGitHub(t), operatorLead+operatorWork, withOAuthUIURL)
	connectOAuthServer(t, server, fake, 12)
	startOperator(t, server)
	server.Engine.SetClock(func() time.Time { return time.Now().Add(2 * time.Hour) })
	fake.answer(http.StatusBadGateway, "refresh-2 access-2 refresh-3 access-3")

	sendChat(t, server, leadChat, "Start the operator.")
	endedOperators(t, server, 2)
	fake.answer(http.StatusBadRequest, `{"error":"invalid_grant","error_description":"refresh-2 access-2 is revoked"}`)
	sendChat(t, server, leadChat, "Start the operator.")
	sessions := endedOperators(t, server, 3)

	if state, _ := listedOAuth(t, server); state != "reconnect" {
		t.Fatalf("oauth = %q, want reconnect", state)
	}
	waitForLeadPrompt(t, server, fmt.Sprintf("Report of the Operator %d:\n\nThe Operator %d failed: the MCP server files needs a new Connect", sessions[2].ID, sessions[2].ID))
	if !strings.Contains(logged.String(), "needs a new Connect") || !strings.Contains(logged.String(), "gave no access token") {
		t.Errorf("log = %s, want the two failures", logged.String())
	}
	requireNoToken(t, append(tokenFreeTexts(t, server, sessions), logged.String()))
}
