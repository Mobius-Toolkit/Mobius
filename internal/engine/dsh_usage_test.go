package engine_test

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

const dshLogHeader = `{"type":"session","version":4,"id":"fake-session","cwd":"/tmp"}`

func dshLogMessage(turn int, model string, input, output, cacheRead, cacheWrite int) string {
	return fmt.Sprintf(`{"type":"assistant/message","time":1,"data":{"turn":%d,"message":{"source":{"provider":"deepseek-account","model":%q}},"usage":{"inputTokens":%d,"outputTokens":%d,"cacheReadTokens":%d,"cacheWriteTokens":%d,"totalTokens":0}}}`,
		turn, model, input, output, cacheRead, cacheWrite)
}

// writeDSHLog writes one zstd frame of lines to the dsh session log at path.
func writeDSHLog(t *testing.T, path string, appendFrame bool, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendFrame {
		flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	file, err := root.OpenFile(filepath.Base(path), flag, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(file, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines {
		if _, err := io.WriteString(encoder, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// Serial: it sets DSH_HOME, which dsh and the reader of the dsh session log read.
func TestADSHTurnReadsTheTokensOfItsOwnSessionLogRecords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	fake := testkit.NewFakeGitHub(t)
	model := "deepseek-account/deepseek-v4-pro"
	script := "[options]\nmodel = ['[\"deepseek-account\",\"deepseek-v4-pro\"]']\nthought_level = [\"high\"]\n" + `
[[prompts]]
reply = ["Done."]

[[prompts]]
reply = ["Done again."]
`
	server, _ := connectScript(t, fake, script, func(cfg *config.Config) {
		cfg.Roles.Researcher = config.RoleBinding{Harness: config.DSH, Model: model, Effort: "high", Max: 2}
	})
	spec := leadSpec(t)
	spec.Role = engine.ResearcherRole
	agent := start(t, server, spec)

	var acpSession sql.NullString
	if err := server.DB.QueryRowContext(t.Context(), `SELECT acp_session_id FROM sessions WHERE id = ?`, agent.ID()).Scan(&acpSession); err != nil {
		t.Fatal(err)
	}
	if !acpSession.Valid {
		t.Fatal("the session has no ACP session id")
	}
	logPath := engine.DSHLogPath(home, spec.Dir, acpSession.String)
	writeDSHLog(t, logPath, false,
		dshLogHeader,
		dshLogMessage(1, "deepseek-v4-pro", 100, 10, 1000, 5),
		dshLogMessage(1, "deepseek-v4-pro", 20, 10, 0, 5),
	)
	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}
	writeDSHLog(t, logPath, true,
		dshLogMessage(2, "deepseek-flash", 30, 3, 200, 0),
	)
	if err := agent.Prompt(t.Context(), "Two", nil); err != nil {
		t.Fatal(err)
	}
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}

	rows := usageRows(t, server, agent.ID())
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	first, second := rows[0], rows[1]
	if first.Harness != "dsh" || first.Model != model || first.ReportedModel.String != "deepseek-account/deepseek-v4-pro" {
		t.Errorf("first = %+v", first)
	}
	if first.Input != nullInt(120) || first.Output != nullInt(20) || first.CacheRead != nullInt(1000) || first.CacheWrite != nullInt(10) {
		t.Errorf("first tokens = %+v", first)
	}
	if first.Cost.Valid {
		t.Errorf("first cost = %+v", first.Cost)
	}
	if second.ReportedModel.String != "deepseek-account/deepseek-flash" {
		t.Errorf("second = %+v", second)
	}
	if second.Input != nullInt(30) || second.Output != nullInt(3) || second.CacheRead != nullInt(200) || second.CacheWrite != nullInt(0) {
		t.Errorf("second tokens = %+v", second)
	}
	if second.Cost.Valid {
		t.Errorf("second cost = %+v", second.Cost)
	}
}
