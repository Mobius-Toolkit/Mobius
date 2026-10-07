package engine_test

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "gh" {
		os.Exit(runner.GH(os.Args[1:], os.Getenv))
	}
	*engine.ToolsTimeout = 3 * time.Second
	*engine.RestartDelays = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond}
	testkit.Main(m)
}

// options are the options of each fake Harness. They have the models and the efforts of the Role bindings of testserver.Config.
const options = `
[options]
model = ["sonnet", "opus", "haiku", "swe-1.5", "gemini-3-pro"]
thought_level = ["low", "medium", "high"]
mode = ["default", "bypassPermissions", "yolo"]
`

// connect starts a server with the Workstream #12 in owner/shop. Each Harness plays the fake agent with prompts.
// connect waits for the first poll, so the engine has the repository.
func connect(t *testing.T, fake *testkit.FakeGitHub, prompts string) (*testserver.Server, string) {
	t.Helper()
	return connectWith(t, fake, prompts, func(*config.Config) {})
}

// connectWith is connect with the config of testserver.Config after adjust.
func connectWith(t *testing.T, fake *testkit.FakeGitHub, prompts string, adjust func(*config.Config)) (*testserver.Server, string) {
	t.Helper()
	return connectScript(t, fake, options+prompts, adjust)
}

// connectScript is connectWith with the whole script of the fake Harness.
func connectScript(t *testing.T, fake *testkit.FakeGitHub, script string, adjust func(*config.Config)) (*testserver.Server, string) {
	t.Helper()
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	dataDir := t.TempDir()
	testkit.InstallFakeAgent(t, dataDir, script)
	cfg := testserver.Config(t, dataDir)
	adjust(cfg)
	server := startServerWith(t, fake, cfg, "")
	server.WaitForFirstPoll(t, shop)
	return server, dataDir
}

func leadSpec(t *testing.T) engine.Spec {
	t.Helper()
	return engine.Spec{Role: engine.LeadRole, Organization: "owner", Repository: shop, Workstream: 12, Dir: t.TempDir()}
}

func start(t *testing.T, server *testserver.Server, spec engine.Spec) *engine.Agent {
	t.Helper()
	agent, err := server.Engine.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return agent
}

// run starts the session of spec, sends each prompt, and ends the session. It gives the id of the session.
func run(t *testing.T, server *testserver.Server, spec engine.Spec, prompts ...string) int64 {
	t.Helper()
	agent := start(t, server, spec)
	for _, prompt := range prompts {
		if err := agent.Prompt(t.Context(), prompt, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}
	return agent.ID()
}

// leadReply runs a Lead session with one prompt, and gives the id of the session and the text of its agent messages.
func leadReply(t *testing.T, server *testserver.Server) (int64, string) {
	t.Helper()
	session := run(t, server, leadSpec(t), "Read the work")
	return session, reply(t, server, session)
}

func transcript(t *testing.T, server *testserver.Server, session int64) []engine.Line {
	t.Helper()
	lines, err := server.Engine.Transcript(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// rows gives the JSON of each Transcript row of kind of the session.
func rows(t *testing.T, server *testserver.Server, session int64, kind string) []map[string]any {
	t.Helper()
	var found []map[string]any
	for _, line := range transcript(t, server, session) {
		if line.Kind != kind {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line.Raw), &row); err != nil {
			t.Fatal(err)
		}
		found = append(found, row)
	}
	return found
}

// reply gives the text of the agent messages of the session.
func reply(t *testing.T, server *testserver.Server, session int64) string {
	t.Helper()
	var text strings.Builder
	for _, row := range rows(t, server, session, "update") {
		update := row["update"].(map[string]any)
		if update["sessionUpdate"] == "agent_message_chunk" {
			text.WriteString(update["content"].(map[string]any)["text"].(string))
		}
	}
	return text.String()
}

func tree(t *testing.T, server *testserver.Server) []engine.Node {
	t.Helper()
	nodes, err := server.Engine.Tree(t.Context(), shop, 12)
	if err != nil {
		t.Fatal(err)
	}
	return nodes
}

func TestALeadSessionIsALeadNodeThatIsLiveUntilItEnds(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nhang = true\n")
	agent := start(t, server, leadSpec(t))
	go func() { _ = agent.Prompt(t.Context(), "Plan the loyalty API", nil) }()

	testkit.WaitFor(t, func() bool { return len(rows(t, server, agent.ID(), "prompt")) == 1 })

	nodes := tree(t, server)
	if len(nodes) != 1 {
		t.Fatalf("nodes = %+v", nodes)
	}
	node := nodes[0]
	if node.Name != "Lead" || node.Title != "chat session" || node.Session.Harness != "claude-code" || node.Session.Model != "opus" ||
		node.Session.EndedAt.Valid || node.Session.AcpSessionID.String != "fake-session" {
		t.Errorf("node = %+v", node)
	}

	if err := agent.End(t.Context(), "stopped"); err != nil {
		t.Fatal(err)
	}

	nodes = tree(t, server)
	if len(nodes) != 1 || !nodes[0].Session.EndedAt.Valid || nodes[0].Session.EndReason.String != "stopped" {
		t.Errorf("nodes = %+v", nodes)
	}
}

// readEvent gives the event name and the data of the next event of the server-sent event stream r.
func readEvent(t *testing.T, r *bufio.Reader) (string, string) {
	t.Helper()
	name, data, err := nextEvent(r)
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	return name, data
}

// nextEvent gives the event name and the data of the next event of the server-sent event stream r.
func nextEvent(r *bufio.Reader) (string, string, error) {
	var name, data string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", "", err
		}
		switch {
		case line == "\n":
			return name, data, nil
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimSuffix(strings.TrimPrefix(line, "event: "), "\n")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimSuffix(strings.TrimPrefix(line, "data: "), "\n")
		}
	}
}

type agentEvent struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	EndedAt   *string `json:"endedAt"`
	EndReason string  `json:"endReason"`
}

type lineEvent struct {
	ID      int64  `json:"id"`
	Session int64  `json:"session"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	Body    string `json:"body"`
}

func TestTheLiveEventsGiveTheAgentAtTheStartAndAtTheEndAndEachTranscriptLine(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Hel\", \"lo\"]\n")
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	events := bufio.NewReader(response.Body)

	session, _ := leadReply(t, server)

	var agents []agentEvent
	var lines []lineEvent
	for len(agents) == 0 || agents[len(agents)-1].EndedAt == nil {
		name, data := readEvent(t, events)
		switch name {
		case "agent":
			var agent agentEvent
			if err := json.Unmarshal([]byte(data), &agent); err != nil {
				t.Fatal(err)
			}
			agents = append(agents, agent)
		case "transcript":
			var line lineEvent
			if err := json.Unmarshal([]byte(data), &line); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, line)
		}
	}
	// The session comes at its addition, at its slot and at its end.
	if len(agents) != 3 {
		t.Fatalf("agents = %+v", agents)
	}
	for _, start := range agents[:2] {
		if start.ID != session || start.Name != "Lead" || start.EndedAt != nil {
			t.Errorf("start = %+v", start)
		}
	}
	if agents[2].ID != session || agents[2].EndReason != "done" {
		t.Errorf("end = %+v", agents[2])
	}
	var message []string
	for _, line := range lines {
		if line.Session != session {
			t.Errorf("line = %+v", line)
		}
		if line.Text == "message" {
			message = append(message, line.Body)
		}
	}
	// The second chunk joins the row of the first chunk, so the line comes again with more text.
	if !reflect.DeepEqual(message, []string{"Hel", "Hello"}) {
		t.Errorf("message lines = %q", message)
	}
}

func TestTheTranscriptFoldsTheFirstPromptAndKeepsTheOthersOpen(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\nreply = [\"Hello\"]\n")
	first := "You are the Lead of one Workstream. The Owner talks to you in this chat.\n\n# Owner message\n\nRead the work"

	session := run(t, server, leadSpec(t), first, "Save in the Workstream memory what the next session needs.")

	var prompts []engine.Line
	var message engine.Line
	for _, line := range transcript(t, server, session) {
		switch {
		case line.Kind == "prompt":
			prompts = append(prompts, line)
		case line.Text == "message":
			message = line
		}
	}
	if len(prompts) != 2 {
		t.Fatalf("prompts = %+v", prompts)
	}
	if prompts[0].Text != "You are the Lead of one Workstream. The Owner talks to you in this chat.…" || !prompts[0].Folded || prompts[0].Body != first {
		t.Errorf("first prompt = %+v", prompts[0])
	}
	if prompts[1].Text != "Save in the Workstream memory what the next session needs." || prompts[1].Folded || prompts[1].Body != "" {
		t.Errorf("second prompt = %+v", prompts[1])
	}
	if message.Kind != "update" || message.Body != "Hello" {
		t.Errorf("message = %+v", message)
	}
}

func TestChunksOfOneKindJoinOneRow(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
updates = [
  '{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"Read "}}',
  '{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"the issue."}}',
]
reply = ["One", " reply."]
`)

	session, text := leadReply(t, server)

	if text != "One reply." {
		t.Errorf("reply = %q", text)
	}
	var updates []string
	for _, line := range transcript(t, server, session) {
		if line.Kind == "update" && line.Text != "config_option_update" {
			updates = append(updates, line.Text+": "+line.Body)
		}
	}
	if !reflect.DeepEqual(updates, []string{"thought: Read the issue.", "message: One reply."}) {
		t.Errorf("updates = %q", updates)
	}
}

func TestAMobiusCallShowsTheMobiusNameAndTheShortResult(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\ncall = { tool = \"list_tasks\" }\n")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)

	session, _ := leadReply(t, server)

	for _, line := range transcript(t, server, session) {
		if line.Kind == "mcp_call" {
			if line.Text != "mobius · list_tasks" || line.Body != "{} → #41 Add plan model: open" || line.Error {
				t.Errorf("line = %+v", line)
			}
			return
		}
	}
	t.Error("the Transcript has no mcp_call")
}

func TestAValidationErrorIsAnErrorLine(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "[[prompts]]\ncall = { tool = \"read_issue\", arguments = { n = 0 } }\n")

	session, _ := leadReply(t, server)

	for _, line := range transcript(t, server, session) {
		if line.Kind == "mcp_call" {
			if line.Text != "mobius · read_issue" || line.Body != `{"n":0} → n must be 1 or more.` || !line.Error {
				t.Errorf("line = %+v", line)
			}
			return
		}
	}
	t.Error("the Transcript has no mcp_call")
}

type toolLine struct {
	text, harnessToolName, body string
}

func toolLines(t *testing.T, server *testserver.Server, session int64, prefix string) []toolLine {
	t.Helper()
	var found []toolLine
	for _, line := range transcript(t, server, session) {
		if strings.HasPrefix(line.Text, prefix) {
			found = append(found, toolLine{line.Text, line.HarnessToolName, line.Body})
		}
	}
	return found
}

func TestAMobiusToolCallOfEachHarnessShowsTheMobiusNameAndTheHarnessName(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
updates = [
  '{"sessionUpdate":"tool_call","toolCallId":"toolu_1","title":"mcp__mobius__echo","kind":"other","status":"pending","rawInput":{},"content":[],"_meta":{"claudeCode":{"toolName":"mcp__mobius__echo"}}}',
  '{"sessionUpdate":"tool_call","toolCallId":"16bf","title":"mobius_echo","kind":"other","status":"pending","rawInput":{"arguments":{"text":"probe"},"text":"probe"},"content":[],"_meta":{"mcp":{"tool":"echo","server":"mobius"},"is_mcp_tool_call":true}}',
  '{"sessionUpdate":"tool_call","toolCallId":"toolu_2","title":"Calling echo from mobius","rawInput":{"text":"probe"},"_meta":{"cognition.ai/toolName":"mcp__mobius__echo","cognition.ai/eventType":"mcp_tool_call","cognition.ai/inferenceToolName":"mcp__mobius__echo"}}',
  '{"sessionUpdate":"tool_call_update","toolCallId":"toolu_2","status":"completed","content":[{"type":"content","content":{"type":"text","text":"echo: probe"}}],"_meta":{"cognition.ai/inferenceToolName":"mcp__mobius__echo"}}',
]
`)

	session, _ := leadReply(t, server)

	want := []toolLine{
		{"mobius · echo", "mcp__mobius__echo", ""},
		{"mobius · echo", "mobius_echo", ""},
		{"mobius · echo", "mcp__mobius__echo", ""},
		{"mobius · echo", "mcp__mobius__echo", "echo: probe"},
	}
	if got := toolLines(t, server, session, "mobius · "); !reflect.DeepEqual(got, want) {
		t.Errorf("lines = %+v", got)
	}
}

func TestAnotherToolCallShowsItsTitleAndAShortOutput(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
updates = [
  '{"sessionUpdate":"tool_call","toolCallId":"toolu_3","title":"Read src/main.rs","kind":"read","status":"pending"}',
  '{"sessionUpdate":"tool_call_update","toolCallId":"toolu_3","status":"completed","content":[{"type":"content","content":{"type":"text","text":"one\ntwo\nthree\nfour"}}]}',
]
`)

	session, _ := leadReply(t, server)

	want := []toolLine{
		{"tool call · Read src/main.rs", "", ""},
		{"tool call update", "", "one\ntwo\nthree…"},
	}
	if got := toolLines(t, server, session, "tool call"); !reflect.DeepEqual(got, want) {
		t.Errorf("lines = %+v", got)
	}
}

// The ACP SDK has no type for an update of a new kind, so the runner reads each update as raw JSON.
func TestTheAPIGivesTheFullTranscriptWithAnUpdateOfAnUnknownKind(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	unknown := `{"sessionId":"fake-session","update":{"sessionUpdate":"a_future_kind","items":[1,2.5,"three"],"_meta":{"x":true}}}`
	server, _ := connect(t, fake, `
[[prompts]]
updates = ['{"sessionUpdate":"a_future_kind","items":[1,2.5,"three"],"_meta":{"x":true}}']
reply = ["Done."]
`)
	session, _ := leadReply(t, server)

	response, err := server.Client.Get(server.URL + "/api/agents/" + strconv.FormatInt(session, 10) + "/transcript")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body struct {
		Data []struct {
			ID      int64  `json:"id"`
			Session int64  `json:"session"`
			Time    string `json:"time"`
			Kind    string `json:"kind"`
			Text    string `json:"text"`
			Body    string `json:"body"`
			Folded  bool   `json:"folded"`
			Raw     string `json:"raw"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, line := range body.Data {
		if line.Session != session {
			t.Errorf("line = %+v", line)
		}
		if line.Text != "config_option_update" {
			got = append(got, line.Kind+" "+line.Text+": "+line.Body)
		}
	}
	want := []string{"prompt Read the work: ", "update a_future_kind: ", "update message: Done."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lines = %q", got)
	}
	var raw string
	for _, line := range body.Data {
		if line.Text == "a_future_kind" {
			raw = line.Raw
		}
	}
	if raw != unknown {
		t.Errorf("raw = %s", raw)
	}
}

func TestAFailedStartEndsTheSessionWithTheError(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake, "", func(cfg *config.Config) { cfg.Roles.Lead.Model = "gpt-5" })

	_, err := server.Engine.Start(t.Context(), leadSpec(t))

	want := `claude-agent-acp refuses model "gpt-5", it has: sonnet, opus, haiku, swe-1.5, gemini-3-pro`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v", err)
	}
	nodes := tree(t, server)
	if len(nodes) != 1 || nodes[0].Session.EndReason.String != "failed" {
		t.Fatalf("nodes = %+v", nodes)
	}
	lines := transcript(t, server, nodes[0].Session.ID)
	last := lines[len(lines)-1]
	if last.Kind != "error" || last.Text != want || !last.Error {
		t.Errorf("last line = %+v", last)
	}
}

func TestTheAgentsOfAWorkstreamComeWithTheirParents(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	leadID := run(t, server, leadSpec(t))
	implementer := leadSpec(t)
	implementer.Role = engine.ImplementerRole
	implementer.Issue = sql.NullInt64{Int64: 41, Valid: true}
	implementer.Parent = sql.NullInt64{Int64: leadID, Valid: true}
	child := run(t, server, implementer)
	other := leadSpec(t)
	other.Workstream = 20
	run(t, server, other)

	response, err := server.Client.Get(server.URL + "/api/workstreams/owner/shop/12/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body struct {
		Data []struct {
			ID         int64  `json:"id"`
			Role       string `json:"role"`
			Name       string `json:"name"`
			Title      string `json:"title"`
			Harness    string `json:"harness"`
			Repository string `json:"repository"`
			Workstream int64  `json:"workstream"`
			Issue      *int64 `json:"issue"`
			Parent     *int64 `json:"parent"`
			StartedAt  string `json:"startedAt"`
			EndedAt    string `json:"endedAt"`
			EndReason  string `json:"endReason"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if len(body.Data) != 2 {
		t.Fatalf("agents = %+v", body.Data)
	}
	first, second := body.Data[0], body.Data[1]
	if first.ID != leadID || first.Role != "lead_chat" || first.Name != "Lead" || first.Title != "chat session" || first.Parent != nil || first.Issue != nil {
		t.Errorf("first = %+v", first)
	}
	if second.ID != child || second.Role != "implementer" || second.Name != "implementer" || second.Harness != "devin" ||
		second.Parent == nil || *second.Parent != leadID || second.Issue == nil || *second.Issue != 41 || second.EndReason != "done" {
		t.Errorf("second = %+v", second)
	}
	for _, agent := range body.Data {
		started, err := time.Parse(time.RFC3339Nano, agent.StartedAt)
		if err != nil || time.Since(started) > time.Minute || agent.EndedAt == "" {
			t.Errorf("agent = %+v: %v", agent, err)
		}
	}
}

func TestTheTreeShowsEachAgentBelowTheAgentThatStartedIt(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, noChange)
	fake.SetCheck(shop, "grep -q cents plan.txt")

	fake.AddLabel(shop, 41, "mobius:ready", "owner")

	nodes := testkit.WaitForValue(t, func() ([]engine.Node, bool) {
		nodes := tree(t, server)
		return nodes, slices.ContainsFunc(nodes, func(node engine.Node) bool { return node.Session.Role == engine.ReviewerRole })
	})
	parent := func(node engine.Node) engine.Node {
		index := slices.IndexFunc(nodes, func(other engine.Node) bool {
			return node.Session.Parent == sql.NullInt64{Int64: other.Session.ID, Valid: true}
		})
		if index < 0 {
			t.Fatalf("the parent of %+v is not in the tree %+v", node, nodes)
		}
		return nodes[index]
	}
	reviewer := nodes[slices.IndexFunc(nodes, func(node engine.Node) bool { return node.Session.Role == engine.ReviewerRole })]
	implementer := parent(reviewer)
	lead := parent(implementer)
	if implementer.Session.Role != engine.ImplementerRole || lead.Session.Role != engine.LeadRole || lead.Session.Parent.Valid {
		t.Errorf("tree = %+v", nodes)
	}
}
