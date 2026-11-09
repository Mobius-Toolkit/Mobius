package engine_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const (
	headerValue = "Bearer header-secret-7f3a"
	envValue    = "env-secret-91bc"
)

// sentServer is a server of the mcpServers of a session/new request.
type sentServer struct {
	Type    string
	Name    string
	URL     string
	Command string
	Args    []string
	Headers []engine.McpPair
	Env     []engine.McpPair
}

// sentServers gives the mcpServers of the last session/new request that a fake agent of dataDir got.
func sentServers(t *testing.T, dataDir string) []sentServer {
	t.Helper()
	text, err := os.ReadFile(filepath.Clean(filepath.Join(dataDir, "harnesses", "mcp_servers")))
	if err != nil {
		t.Fatal(err)
	}
	var servers []sentServer
	if err := json.Unmarshal(text, &servers); err != nil {
		t.Fatal(err)
	}
	return servers
}

func serverNames(servers []sentServer) []string {
	var names []string
	for _, server := range servers {
		names = append(names, server.Name)
	}
	return names
}

// selectServers adds the stdio server "files" and the http server "makeadir" to the organization, and selects them for
// the Workstream #12.
func selectServers(t *testing.T, server *testserver.Server) {
	t.Helper()
	ctx := t.Context()
	files, err := server.Engine.AddMcpServer(ctx, "owner", engine.McpServer{
		Name: "files", Kind: "stdio", Command: "files-server", Args: []string{"--root", "/data"},
		Env: []engine.McpPair{{Name: "KEY", Value: envValue}},
	})
	if err != nil {
		t.Fatal(err)
	}
	site, err := server.Engine.AddMcpServer(ctx, "owner", engine.McpServer{
		Name: "makeadir", Kind: "http", URL: "https://makeadir.example.com/mcp",
		Headers: []engine.McpPair{{Name: "Authorization", Value: headerValue}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Engine.SetWorkstreamMcpServers(ctx, shop, 12, []int64{files.ID, site.ID}); err != nil {
		t.Fatal(err)
	}
}

func TestAnOperatorGetsTheSelectedMcpServersAfterMobiusWithTheHttpServerFirst(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectOperator(t, fake, operatorLead+operatorWork)
	selectServers(t, server)

	sendChat(t, server, leadChat, "Start the operator.")

	endedOperators(t, server, 1)
	servers := sentServers(t, dataDir)
	if want := []string{"mobius", "makeadir", "files"}; !slices.Equal(serverNames(servers), want) {
		t.Fatalf("servers = %+v, want %v", servers, want)
	}
	site, files := servers[1], servers[2]
	if site.Type != "http" || site.URL != "https://makeadir.example.com/mcp" || !slices.Equal(site.Headers, []engine.McpPair{{Name: "Authorization", Value: headerValue}}) {
		t.Errorf("makeadir = %+v", site)
	}
	if files.Command != "files-server" || !slices.Equal(files.Args, []string{"--root", "/data"}) || !slices.Equal(files.Env, []engine.McpPair{{Name: "KEY", Value: envValue}}) {
		t.Errorf("files = %+v", files)
	}
}

func TestAnOperatorOfAWorkstreamWithNoSelectionGetsOnlyMobius(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectOperator(t, fake, operatorLead+operatorWork)

	sendChat(t, server, leadChat, "Start the operator.")

	endedOperators(t, server, 1)
	if names := serverNames(sentServers(t, dataDir)); !slices.Equal(names, []string{"mobius"}) {
		t.Errorf("servers = %v", names)
	}
}

func TestTheNextOperatorGetsTheSelectionThatTheOwnerChanged(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectOperator(t, fake, operatorLead+operatorWork)
	selectServers(t, server)
	sendChat(t, server, leadChat, "Start the operator.")
	endedOperators(t, server, 1)
	if names := serverNames(sentServers(t, dataDir)); !slices.Equal(names, []string{"mobius", "makeadir", "files"}) {
		t.Fatalf("servers of the first Operator = %v", names)
	}
	if err := server.Engine.SetWorkstreamMcpServers(t.Context(), shop, 12, nil); err != nil {
		t.Fatal(err)
	}

	sendChat(t, server, leadChat, "Start the operator.")

	endedOperators(t, server, 2)
	if names := serverNames(sentServers(t, dataDir)); !slices.Equal(names, []string{"mobius"}) {
		t.Errorf("servers = %v", names)
	}
}

func TestALeadAndAResearcherOfAWorkstreamWithSelectedServersGetOnlyMobius(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectResearcher(t, fake, `
[[prompts]]
when = "# Researcher message"
reply = ["I have the report."]

[[prompts]]
when = "Start the researcher"
call = { tool = "start_researcher", arguments = { repository = "owner/shop", question = "Where are the plans?" } }

[[prompts]]
when = "Ping"
reply = ["Pong"]
`, quickResearcher)
	selectServers(t, server)

	sendChat(t, server, leadChat, "Ping")
	testkit.WaitFor(t, func() bool {
		sessions := chatSessions(t, server, leadChat, engine.LeadRole)
		return len(sessions) == 1 && strings.Contains(reply(t, server, sessions[0].ID), "Pong")
	})
	if names := serverNames(sentServers(t, dataDir)); !slices.Equal(names, []string{"mobius"}) {
		t.Errorf("Lead servers = %v", names)
	}

	sendChat(t, server, leadChat, "Start the researcher.")
	testkit.WaitFor(t, func() bool {
		sessions := roleSessions(t, server, engine.ResearcherRole)
		return len(sessions) == 1 && sessions[0].EndReason.Valid
	})
	if names := serverNames(sentServers(t, dataDir)); !slices.Equal(names, []string{"mobius"}) {
		t.Errorf("Researcher servers = %v", names)
	}
}

func TestTheTranscriptAndTheReportOfAnOperatorHaveNoHeaderValueAndNoEnvValue(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectOperator(t, fake, operatorLead+operatorWork)
	selectServers(t, server)

	sendChat(t, server, leadChat, "Start the operator.")

	session := endedOperators(t, server, 1)[0]
	if got := sentServers(t, dataDir); len(got) != 3 {
		t.Fatalf("servers = %+v", got)
	}
	waitForLeadPrompt(t, server, "Report of the Operator")
	var texts []string
	for _, line := range transcript(t, server, session.ID) {
		texts = append(texts, line.Text, line.Body, line.Raw)
	}
	texts = append(texts, leadPrompts(t, server)...)
	for _, text := range texts {
		for _, secret := range []string{headerValue, envValue} {
			if strings.Contains(text, secret) {
				t.Errorf("%s is in %s", secret, text)
			}
		}
	}
}
