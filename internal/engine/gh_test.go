package engine_test

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// connectGH starts a server whose agent runs "gh GH_TOKEN", and authorizes the Owner. The real gh is printenv,
// so the reply has the token that the real gh gets.
func connectGH(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, string) {
	t.Helper()
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	server, dataDir := connect(t, fake, "[[prompts]]\nshell = \"gh GH_TOKEN\"\n")
	printenv, err := exec.LookPath("printenv")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(printenv, filepath.Join(dataDir, "harnesses", "gh")); err != nil {
		t.Fatal(err)
	}
	authorize(t, server)
	return server, dataDir
}

// authorize gives the user tokens of the code "user-code" to the server, as GitHub does after the Owner authorizes the App.
func authorize(t *testing.T, server *testserver.Server) {
	t.Helper()
	client := *server.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Get(server.URL + "/api/github/user-callback?code=user-code")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("user callback: status %d", response.StatusCode)
	}
}

func expireUserToken(t *testing.T, server *testserver.Server, refreshToken string) {
	t.Helper()
	expired := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	if _, err := server.DB.Exec("UPDATE github_apps SET refresh_token = ?, user_token_expires_at = ?", refreshToken, expired); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, url string) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}

// ghTokenURL gives the gh token URL of the key of the MCP server URL.
func ghTokenURL(mcpURL string) string {
	return strings.Replace(mcpURL, "/mcp/", "/gh-token/", 1)
}

func TestTheGHTokenRouteServesOnlyALiveLeadSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectGH(t, fake)

	_, text := leadReply(t, server)

	if text != "ghu_1\nexit 0" {
		t.Errorf("reply = %q", text)
	}
	ended := ghTokenURL(mcpURL(t, dataDir))
	triager := start(t, server, engine.Spec{Role: engine.TriagerRole, Organization: "owner", Repository: shop, Dir: t.TempDir()})
	defer func() { _ = triager.End(t.Context(), "done") }()
	for _, url := range []string{ended, ghTokenURL(mcpURL(t, dataDir)), ended[:strings.LastIndex(ended, "/")] + "/0123"} {
		if status := get(t, url); status != http.StatusNotFound {
			t.Errorf("%s: status %d", url, status)
		}
	}
}

func TestGHRefreshesAnExpiredUserToken(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectGH(t, fake)
	expireUserToken(t, server, "ghr_1")

	_, text := leadReply(t, server)

	if text != "ghu_2\nexit 0" {
		t.Errorf("reply = %q", text)
	}
	var token, refreshToken, expires string
	err := server.DB.QueryRow("SELECT user_token, refresh_token, user_token_expires_at FROM github_apps").Scan(&token, &refreshToken, &expires)
	if err != nil {
		t.Fatal(err)
	}
	expiry, err := time.Parse(time.RFC3339Nano, expires)
	if token != "ghu_2" || refreshToken != "ghr_2" || err != nil || time.Until(expiry) < 7*time.Hour {
		t.Errorf("tokens = %s, %s, %s: %v", token, refreshToken, expires, err)
	}
}

func TestGHPrintsTheAuthorizeURLWhenTheRefreshFails(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectGH(t, fake)
	expireUserToken(t, server, "ghr_wrong")

	_, text := leadReply(t, server)

	want := "The refresh token passed is incorrect or expired.: tell the Owner to open " + fake.URL + "/login/oauth/authorize?client_id=Iv23test and authorize the App\nexit 1"
	if !strings.HasSuffix(text, want) {
		t.Errorf("reply = %q", text)
	}
}
