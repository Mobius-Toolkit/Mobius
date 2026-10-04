package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const valid = `
access_password = "correct horse"
trusted_users = ["owner"]

[roles]
lead        = { harness = "claude-code", model = "opus",    effort = "high" }
triager     = { harness = "claude-code", model = "sonnet",  effort = "medium" }
implementer = { harness = "devin",       model = "swe-1.5", effort = "high" }
researcher  = { harness = "antigravity", model = "gemini-3-pro" }
reviewer    = { harness = "claude-code", model = "opus",    effort = "high" }
judge       = { harness = "claude-code", model = "haiku",   effort = "low" }
`

func parse(t *testing.T, text string) *Config {
	t.Helper()
	config, err := Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func parseError(t *testing.T, text string) string {
	t.Helper()
	_, err := Parse([]byte(text))
	if err == nil {
		t.Fatal("Parse gave no error")
	}
	return err.Error()
}

func TestParseGivesTheDefaults(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	config := parse(t, valid)

	checks := []struct {
		key       string
		got, want any
	}{
		{"access_password", config.AccessPassword, "correct horse"},
		{"trusted_bots", len(config.TrustedBots), 0},
		{"data_dir", config.DataDir, filepath.Join(home, ".mobius")},
		{"max_agents", config.MaxAgents, 4},
		{"max_checks", config.MaxChecks, 1},
		{"max_fix_rounds", config.MaxFixRounds, 7},
		{"max_check_attempts", config.MaxCheckAttempts, 3},
		{"max_worker_restarts", config.MaxWorkerRestarts, 3},
		{"check_timeout", config.CheckTimeout, 15 * time.Minute},
		{"review_quiet_period", config.ReviewQuietPeriod, 10 * time.Minute},
		{"stale_pr_age", config.StalePRAge, 7 * 24 * time.Hour},
		{"lead_idle_timeout", config.LeadIdleTimeout, time.Hour},
		{"poll_interval", config.PollInterval, 30 * time.Second},
		{"housekeeper_interval", config.HousekeeperInterval, time.Hour},
		{"roles.researcher.harness", config.Roles.Researcher.Harness, Antigravity},
		{"roles.researcher.effort", config.Roles.Researcher.Effort, ""},
		{"roles.implementer.model", config.Roles.Implementer.Model, "swe-1.5"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.key, c.got, c.want)
		}
	}
	if !slices.Equal(config.TrustedUsers, []string{"owner"}) {
		t.Errorf("trusted_users = %v", config.TrustedUsers)
	}
	for _, b := range config.Roles.Bindings() {
		wantMax, wantCounts := 2, true
		switch b.Role {
		case "lead":
			wantMax, wantCounts = 8, false
		case "triager":
			wantCounts = false
		}
		if b.Binding.Max != wantMax || b.Binding.CountsInMaxAgents != wantCounts {
			t.Errorf("roles.%s: max = %d, counts_in_max_agents = %v; want %d, %v",
				b.Role, b.Binding.Max, b.Binding.CountsInMaxAgents, wantMax, wantCounts)
		}
	}
}

func TestParseReadsEachKey(t *testing.T) {
	config := parse(t, `
trusted_bots = ["renovate[bot]"]
data_dir = "/srv/mobius"
max_agents = 1
max_checks = 2
max_fix_rounds = 3
max_check_attempts = 4
max_worker_restarts = 5
check_timeout = "20m"
review_quiet_period = "5m"
stale_pr_age = "72h"
lead_idle_timeout = "30m"
poll_interval = "1m30s"
housekeeper_interval = "2h"
`+strings.Replace(strings.Replace(valid,
		`implementer = { harness = "devin",       model = "swe-1.5", effort = "high" }`,
		`implementer = { harness = "devin", model = "swe-1.5", effort = "high", max = 3 }`, 1),
		`judge       = { harness = "claude-code", model = "haiku",   effort = "low" }`,
		`judge       = { harness = "claude-code", model = "haiku", effort = "low", counts_in_max_agents = false }`, 1))

	checks := []struct {
		key       string
		got, want any
	}{
		{"data_dir", config.DataDir, "/srv/mobius"},
		{"max_agents", config.MaxAgents, 1},
		{"max_checks", config.MaxChecks, 2},
		{"max_fix_rounds", config.MaxFixRounds, 3},
		{"max_check_attempts", config.MaxCheckAttempts, 4},
		{"max_worker_restarts", config.MaxWorkerRestarts, 5},
		{"check_timeout", config.CheckTimeout, 20 * time.Minute},
		{"review_quiet_period", config.ReviewQuietPeriod, 5 * time.Minute},
		{"stale_pr_age", config.StalePRAge, 72 * time.Hour},
		{"lead_idle_timeout", config.LeadIdleTimeout, 30 * time.Minute},
		{"poll_interval", config.PollInterval, 90 * time.Second},
		{"housekeeper_interval", config.HousekeeperInterval, 2 * time.Hour},
		{"roles.implementer.max", config.Roles.Implementer.Max, 3},
		{"roles.implementer.counts_in_max_agents", config.Roles.Implementer.CountsInMaxAgents, true},
		{"roles.judge.max", config.Roles.Judge.Max, 2},
		{"roles.judge.counts_in_max_agents", config.Roles.Judge.CountsInMaxAgents, false},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.key, c.got, c.want)
		}
	}
	if !slices.Equal(config.TrustedBots, []string{"renovate[bot]"}) {
		t.Errorf("trusted_bots = %v", config.TrustedBots)
	}
}

func TestParseRefusesAnUnknownKey(t *testing.T) {
	checks := []struct{ text, want string }{
		{"pool_interval = \"1s\"\n" + valid, "line 1: unknown key pool_interval"},
		{"max_workers_total = 1\n" + valid, "line 1: unknown key max_workers_total"},
		{strings.Replace(valid, `model = "haiku"`, `models = "haiku"`, 1), "line 11: unknown key models"},
	}
	for _, c := range checks {
		if got := parseError(t, c.text); got != c.want {
			t.Errorf("error = %q, want %q", got, c.want)
		}
	}
}

func TestParseRefusesAWrongValue(t *testing.T) {
	got := parseError(t, "max_checks = \"one\"\n"+valid)
	if !strings.HasPrefix(got, "line 1: toml: cannot decode TOML string into struct field") {
		t.Errorf("error = %q", got)
	}
	got = parseError(t, "poll_interval = \"soon\"\n"+valid)
	if want := `poll_interval: time: invalid duration "soon"`; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

func TestParseRefusesASyntaxError(t *testing.T) {
	got := parseError(t, valid+"\n[roles")
	if want := "line 13: toml: expected ']' to close table name"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

func TestParseRefusesAWrongRoleOrPassword(t *testing.T) {
	checks := []struct{ text, want string }{
		{strings.Replace(valid, "access_password = \"correct horse\"\n", "", 1),
			"access_password: must have at least 8 characters"},
		{strings.Replace(valid, "correct horse", "short", 1),
			"access_password: must have at least 8 characters"},
		{strings.Replace(valid, `["owner"]`, "[]", 1),
			"trusted_users: must not be empty"},
		{strings.Replace(valid, "judge ", "#judge ", 1),
			"roles.judge.harness: must be claude-code, antigravity or devin"},
		{strings.Replace(valid, `"devin"`, `"codex"`, 1),
			"roles.implementer.harness: must be claude-code, antigravity or devin"},
		{strings.Replace(valid, `model = "haiku",`, "", 1),
			"roles.judge.model: must not be empty"},
		{strings.Replace(valid, `,   effort = "low"`, "", 1),
			"roles.judge.effort: is required for claude-code"},
		{strings.Replace(valid, `"swe-1.5", effort = "high"`, `"swe-1.5"`, 1),
			"roles.implementer.effort: is required for devin"},
		{strings.Replace(valid, `"gemini-3-pro"`, `"gemini-3-pro", effort = "high"`, 1),
			"roles.researcher.effort: is not allowed for antigravity"},
		{strings.Replace(valid,
			`reviewer    = { harness = "claude-code", model = "opus",    effort = "high" }`,
			`reviewer    = { harness = "devin", model = "swe-1.5", effort = "low" }`, 1),
			"roles.reviewer: must not have the same harness and model as roles.implementer"},
	}
	for _, c := range checks {
		if got := parseError(t, c.text); got != c.want {
			t.Errorf("error = %q, want %q", got, c.want)
		}
	}
}

func TestLoadRefusesAFileThatDoesNotExist(t *testing.T) {
	_, err := Load("/nonexistent/config.toml")

	if err == nil || !strings.HasPrefix(err.Error(), "/nonexistent/config.toml: cannot read:") {
		t.Errorf("error = %v", err)
	}
}

func TestLoadNamesTheFileInAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(strings.Replace(valid, "correct horse", "short", 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)

	if want := path + ": access_password: must have at least 8 characters"; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %s", err, want)
	}
}
