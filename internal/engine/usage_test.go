package engine_test

import (
	"database/sql"
	"math"
	"strconv"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type usageRow struct {
	Session, Workstream                     int64
	Task, Issue                             sql.NullInt64
	Organization, Repository, Role, Harness string
	Model                                   string
	ReportedModel, Effort                   sql.NullString
	StartedAt, EndedAt                      string
	Input, Output, CacheRead, CacheWrite    sql.NullInt64
	Cost                                    sql.NullFloat64
}

func usageRows(t *testing.T, server *testserver.Server, session int64) []usageRow {
	t.Helper()
	rows, err := server.DB.QueryContext(t.Context(), `SELECT session, task, issue, workstream, organization, repository, role, harness, model,
		reported_model, effort, started_at, ended_at, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd
		FROM turn_usage WHERE session = ? ORDER BY id`, session)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var found []usageRow
	for rows.Next() {
		var r usageRow
		if err := rows.Scan(&r.Session, &r.Task, &r.Issue, &r.Workstream, &r.Organization, &r.Repository, &r.Role, &r.Harness, &r.Model,
			&r.ReportedModel, &r.Effort, &r.StartedAt, &r.EndedAt, &r.Input, &r.Output, &r.CacheRead, &r.CacheWrite, &r.Cost); err != nil {
			t.Fatal(err)
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return found
}

func nullInt(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: true}
}

func costOf(t *testing.T, row usageRow, want float64) {
	t.Helper()
	if !row.Cost.Valid || math.Abs(row.Cost.Float64-want) > 1e-9 {
		t.Errorf("cost = %+v, want %v", row.Cost, want)
	}
}

// claudeTurn is a prompt of the fake Claude Code with a cost total, a model and the tokens of the turn.
func claudeTurn(total float64, input, output, cacheRead, cacheWrite int) string {
	return `
[[prompts]]
updates = ['{"sessionUpdate": "usage_update", "used": 1, "size": 2, "cost": {"amount": ` + strconv.FormatFloat(total, 'f', -1, 64) + `, "currency": "USD"}, "_meta": {"_claude/model": "claude-opus-5-5"}}']
usage = '{"inputTokens": ` + strconv.Itoa(input) + `, "outputTokens": ` + strconv.Itoa(output) + `, "cachedReadTokens": ` + strconv.Itoa(cacheRead) + `, "cachedWriteTokens": ` + strconv.Itoa(cacheWrite) + `, "totalTokens": 1}'
`
}

func TestAClaudeCodeTurnHasAUsageRowWithTheTokensTheModelAndTheCostOfTheTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, claudeTurn(0.6, 100, 20, 3000, 400)+claudeTurn(0.9, 50, 10, 4000, 0))
	spec := leadSpec(t)
	spec.Issue = sql.NullInt64{Int64: 41, Valid: true}

	session := run(t, server, spec, "One", "Two")

	rows := usageRows(t, server, session)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	first, second := rows[0], rows[1]
	if first.Session != session || first.Task.Valid || first.Issue != nullInt(41) || first.Workstream != 12 || first.Organization != "owner" ||
		first.Repository != shop || first.Role != engine.LeadRole || first.Harness != "claude-code" || first.Model != "opus" ||
		first.ReportedModel.String != "claude-opus-5-5" || first.Effort.String != "high" || first.StartedAt == "" || parseTime(t, first.EndedAt).Before(parseTime(t, first.StartedAt)) {
		t.Errorf("first = %+v", first)
	}
	if first.Input != nullInt(100) || first.Output != nullInt(20) || first.CacheRead != nullInt(3000) || first.CacheWrite != nullInt(400) {
		t.Errorf("first tokens = %+v", first)
	}
	costOf(t, first, 0.6)
	if second.Input != nullInt(50) || second.Output != nullInt(10) || second.CacheRead != nullInt(4000) || second.CacheWrite != nullInt(0) {
		t.Errorf("second tokens = %+v", second)
	}
	costOf(t, second, 0.3)
	if parseTime(t, second.StartedAt).Before(parseTime(t, first.EndedAt)) {
		t.Errorf("the second turn starts at %s, before the first ends at %s", second.StartedAt, first.EndedAt)
	}
}

func TestTheCostOfAnAutonomousTurnAfterTheLastPromptHasAUsageRowAtTheEndOfTheSession(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, claudeTurn(0.6, 1, 1, 1, 1)+`later = { after = "10ms", updates = ['{"sessionUpdate": "usage_update", "used": 1, "size": 2, "cost": {"amount": 0.9, "currency": "USD"}, "_meta": {"_claude/origin": {"kind": "task-notification"}}}'] }
`)
	agent := start(t, server, leadSpec(t))
	if err := agent.Prompt(t.Context(), "One", nil); err != nil {
		t.Fatal(err)
	}
	testkit.WaitFor(t, func() bool {
		var updates int
		if err := server.DB.QueryRowContext(t.Context(), `SELECT count(*) FROM transcript WHERE session = ? AND json LIKE '%task-notification%'`, agent.ID()).Scan(&updates); err != nil {
			t.Fatal(err)
		}
		return updates > 0
	})
	if err := agent.End(t.Context(), "done"); err != nil {
		t.Fatal(err)
	}

	rows := usageRows(t, server, agent.ID())
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	costOf(t, rows[0], 0.6)
	costOf(t, rows[1], 0.3)
}

func TestACostTotalThatFallsGivesTheNewTotalAsTheCostOfTheTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, claudeTurn(0.9, 1, 1, 1, 1)+claudeTurn(0.2, 1, 1, 1, 1))

	session := run(t, server, leadSpec(t), "One", "Two")

	rows := usageRows(t, server, session)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	costOf(t, rows[0], 0.9)
	costOf(t, rows[1], 0.2)
}

func TestATurnWithNoCostAndNoUsageFieldHasNoValueForThem(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
updates = ['{"sessionUpdate": "usage_update", "used": 1, "size": 2, "_meta": {"_claude/model": "claude-opus-5-5"}}']
usage = '{"inputTokens": 5, "outputTokens": 6}'
`)

	session := run(t, server, leadSpec(t), "One")

	rows := usageRows(t, server, session)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	row := rows[0]
	if row.Input != nullInt(5) || row.Output != nullInt(6) || row.CacheRead.Valid || row.CacheWrite.Valid || row.Cost.Valid {
		t.Errorf("row = %+v", row)
	}
}

func TestATurnWithNoDataHasAUsageRowWithNullValues(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
reply = ["Done."]
`)

	session := run(t, server, leadSpec(t), "One")

	rows := usageRows(t, server, session)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	row := rows[0]
	if row.Input.Valid || row.Output.Valid || row.CacheRead.Valid || row.CacheWrite.Valid || row.Cost.Valid || row.ReportedModel.Valid {
		t.Errorf("row = %+v", row)
	}
}

func TestATurnThatEndsWithAnErrorAndHasNoDataHasNoUsageRow(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
error = { code = -32603, message = "Internal error" }
`)
	agent := start(t, server, leadSpec(t))

	if err := agent.Prompt(t.Context(), "One", nil); err == nil {
		t.Fatal("the prompt gave no error")
	}

	if rows := usageRows(t, server, agent.ID()); len(rows) != 0 {
		t.Errorf("rows = %+v", rows)
	}
}

func TestATurnThatEndsWithAnErrorKeepsTheCostOfTheTurn(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, `
[[prompts]]
updates = ['{"sessionUpdate": "usage_update", "used": 1, "size": 2, "cost": {"amount": 0.25, "currency": "USD"}}']
error = { code = -32603, message = "Internal error" }
`)
	agent := start(t, server, leadSpec(t))

	if err := agent.Prompt(t.Context(), "One", nil); err == nil {
		t.Fatal("the prompt gave no error")
	}

	rows := usageRows(t, server, agent.ID())
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	costOf(t, rows[0], 0.25)
	if rows[0].Input.Valid || rows[0].ReportedModel.Valid {
		t.Errorf("row = %+v", rows[0])
	}
}

func TestADevinTurnAddsTheTokensOfEachModelCallAndSkipsTheCopies(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	call := func(input, output, cached int) string {
		meta := `"cognition.ai/inputTokens": ` + strconv.Itoa(input) + `, "cognition.ai/outputTokens": ` + strconv.Itoa(output) + `, "cognition.ai/cachedReadTokens": ` + strconv.Itoa(cached)
		return `'{"sessionUpdate": "usage_update", "used": 1, "size": 2, "_meta": {` + meta + `, "cognition.ai/subagent_context": {"parentAgentId": "root"}}}',
	'{"sessionUpdate": "usage_update", "used": 1, "size": 2, "_meta": {` + meta + `}}'`
	}
	server, _ := connect(t, fake, "[[prompts]]\nupdates = [\n\t"+call(42410, 376, 42260)+",\n\t"+call(100, 5, 50)+",\n]\n")
	spec := leadSpec(t)
	spec.Role = engine.ImplementerRole

	session := run(t, server, spec, "One")

	rows := usageRows(t, server, session)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	row := rows[0]
	if row.Harness != "devin" || row.Model != "swe-1.5" || row.Effort.String != "high" ||
		row.Input != nullInt(42510) || row.Output != nullInt(381) || row.CacheRead != nullInt(42310) {
		t.Errorf("row = %+v", row)
	}
	if row.CacheWrite.Valid || row.Cost.Valid || row.ReportedModel.Valid {
		t.Errorf("row = %+v", row)
	}
}

func TestASessionHasTheEffortOfItsRoleBinding(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	researcher := leadSpec(t)
	researcher.Role = engine.ResearcherRole

	lead := run(t, server, leadSpec(t))
	noEffort := run(t, server, researcher)

	for session, want := range map[int64]sql.NullString{lead: {String: "high", Valid: true}, noEffort: {}} {
		var effort sql.NullString
		if err := server.DB.QueryRowContext(t.Context(), `SELECT effort FROM sessions WHERE id = ?`, session).Scan(&effort); err != nil {
			t.Fatal(err)
		}
		if effort != want {
			t.Errorf("effort of %d = %+v, want %+v", session, effort, want)
		}
	}
}
