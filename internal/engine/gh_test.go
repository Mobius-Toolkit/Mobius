package engine_test

import (
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// connectGH starts a server whose agent runs "gh GH_TOKEN". The real gh is printenv, so the reply has the token that the
// real gh gets.
func connectGH(t *testing.T, fake *testkit.FakeGitHub) (*testserver.Server, string) {
	t.Helper()
	server, dataDir := connect(t, fake, "[[prompts]]\nshell = \"gh GH_TOKEN\"\n")
	printenv, err := exec.LookPath("printenv")
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec " + printenv + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dataDir, "harnesses", "gh"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	chmod := exec.Command("chmod", "+x", "gh")
	chmod.Dir = filepath.Join(dataDir, "harnesses")
	if err := chmod.Run(); err != nil {
		t.Fatal(err)
	}
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

func TestTheGHTokenRouteGivesTheLeadAnInstallationTokenWithAllPermissionsAndServesOnlyALiveSession(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectGH(t, fake)

	_, text := leadReply(t, server)

	token, ok := strings.CutSuffix(text, "\nexit 0")
	if !ok || !strings.HasPrefix(token, "ghs_") {
		t.Fatalf("reply = %q", text)
	}
	if got := fake.InstallationTokenPermissions(token); len(got) != 0 {
		t.Errorf("permissions = %v", got)
	}
	ended := ghTokenURL(mcpURL(t, dataDir))
	triager := start(t, server, engine.Spec{Role: engine.TriagerRole, Organization: "owner", Repository: shop, Dir: t.TempDir()})
	defer func() { _ = triager.End(t.Context(), "done") }()
	if status := get(t, ghTokenURL(mcpURL(t, dataDir))); status != http.StatusOK {
		t.Errorf("live session: status %d", status)
	}
	for _, url := range []string{ended, ended[:strings.LastIndex(ended, "/")] + "/0123"} {
		if status := get(t, url); status != http.StatusNotFound {
			t.Errorf("%s: status %d", url, status)
		}
	}
}

func TestAnAgentThatIsNotTheLeadGetsAReadOnlyInstallationToken(t *testing.T) {
	t.Parallel()
	for name, spec := range map[string]engine.Spec{
		"Triager":     {Role: engine.TriagerRole, Organization: "owner", Repository: shop},
		"Coordinator": {Role: engine.CoordinatorRole, Organization: "owner"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, _ := connectGH(t, fake)
			spec.Dir = t.TempDir()

			text := reply(t, server, run(t, server, spec, "Read the work"))

			token, ok := strings.CutSuffix(text, "\nexit 0")
			if !ok || !strings.HasPrefix(token, "ghs_") {
				t.Fatalf("reply = %q", text)
			}
			want := map[string]string{"contents": "read", "issues": "read", "pull_requests": "read", "checks": "read", "actions": "read", "metadata": "read"}
			if got := fake.InstallationTokenPermissions(token); !maps.Equal(got, want) {
				t.Errorf("permissions = %v", got)
			}
		})
	}
}
