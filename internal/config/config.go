// Package config reads config.toml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
)

// Harness is the agent program of a Role.
type Harness string

// The Harnesses that Mobius can run.
const (
	ClaudeCode  Harness = "claude-code"
	Antigravity Harness = "antigravity"
	Devin       Harness = "devin"
)

// Harnesses are all Harnesses, in the order that mobius init offers them.
var Harnesses = []Harness{ClaudeCode, Antigravity, Devin}

// HasEffortLevels tells if the Harness has the thought_level option. A Role
// on such a Harness must have an effort, and a Role on another Harness must not.
func (h Harness) HasEffortLevels() bool {
	return h == ClaudeCode || h == Devin
}

// Config is the content of config.toml.
type Config struct {
	AccessPassword      string        `toml:"access_password"`
	TrustedUsers        []string      `toml:"trusted_users"`
	TrustedBots         []string      `toml:"trusted_bots"`
	DataDir             string        `toml:"data_dir"`
	MaxAgents           int           `toml:"max_agents"`
	MaxChecks           int           `toml:"max_checks"`
	MaxFixRounds        int           `toml:"max_fix_rounds"`
	MaxCheckAttempts    int           `toml:"max_check_attempts"`
	MaxWorkerRestarts   int           `toml:"max_worker_restarts"`
	CheckTimeout        time.Duration `toml:"-"`
	ReviewQuietPeriod   time.Duration `toml:"-"`
	StalePRAge          time.Duration `toml:"-"`
	LeadIdleTimeout     time.Duration `toml:"-"`
	PollInterval        time.Duration `toml:"-"`
	HousekeeperInterval time.Duration `toml:"-"`
	Roles               Roles         `toml:"roles"`
}

// Roles binds each Role to a Harness and a model.
type Roles struct {
	Lead        RoleBinding `toml:"lead"`
	Triager     RoleBinding `toml:"triager"`
	Implementer RoleBinding `toml:"implementer"`
	Researcher  RoleBinding `toml:"researcher"`
	Reviewer    RoleBinding `toml:"reviewer"`
	Judge       RoleBinding `toml:"judge"`
	Curator     RoleBinding `toml:"curator"`
}

// RoleBinding is the Harness, the model and the limits of a Role.
type RoleBinding struct {
	Harness Harness `toml:"harness"`
	Model   string  `toml:"model"`
	// Effort is empty for a Harness with no effort levels.
	Effort            string `toml:"effort"`
	Max               int    `toml:"max"`
	CountsInMaxAgents bool   `toml:"counts_in_max_agents"`
}

// file holds the durations as text, because TOML has no duration type.
type file struct {
	Config
	CheckTimeout        string `toml:"check_timeout"`
	ReviewQuietPeriod   string `toml:"review_quiet_period"`
	StalePRAge          string `toml:"stale_pr_age"`
	LeadIdleTimeout     string `toml:"lead_idle_timeout"`
	PollInterval        string `toml:"poll_interval"`
	HousekeeperInterval string `toml:"housekeeper_interval"`
}

// Load reads and checks the config file at path.
func Load(path string) (*Config, error) {
	text, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("%s: cannot read: %w", path, err)
	}
	config, err := Parse(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return config, nil
}

// Parse reads and checks the text of a config file.
func Parse(text []byte) (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	f := file{
		Config: Config{
			DataDir:           filepath.Join(home, ".mobius"),
			MaxAgents:         4,
			MaxChecks:         1,
			MaxFixRounds:      7,
			MaxCheckAttempts:  3,
			MaxWorkerRestarts: 3,
			Roles: Roles{
				Lead:        RoleBinding{Max: 8},
				Triager:     RoleBinding{Max: 2},
				Implementer: RoleBinding{Max: 2, CountsInMaxAgents: true},
				Researcher:  RoleBinding{Max: 2, CountsInMaxAgents: true},
				Reviewer:    RoleBinding{Max: 2, CountsInMaxAgents: true},
				Judge:       RoleBinding{Max: 2, CountsInMaxAgents: true},
				Curator:     RoleBinding{Max: 2, CountsInMaxAgents: true},
			},
		},
		CheckTimeout:        "15m",
		ReviewQuietPeriod:   "10m",
		StalePRAge:          "168h",
		LeadIdleTimeout:     "1h",
		PollInterval:        "30s",
		HousekeeperInterval: "1h",
	}
	decoder := toml.NewDecoder(bytes.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&f); err != nil {
		return nil, describe(err)
	}
	config := f.Config
	durations := []struct {
		key   string
		text  string
		value *time.Duration
	}{
		{"check_timeout", f.CheckTimeout, &config.CheckTimeout},
		{"review_quiet_period", f.ReviewQuietPeriod, &config.ReviewQuietPeriod},
		{"stale_pr_age", f.StalePRAge, &config.StalePRAge},
		{"lead_idle_timeout", f.LeadIdleTimeout, &config.LeadIdleTimeout},
		{"poll_interval", f.PollInterval, &config.PollInterval},
		{"housekeeper_interval", f.HousekeeperInterval, &config.HousekeeperInterval},
	}
	for _, d := range durations {
		if *d.value, err = time.ParseDuration(d.text); err != nil {
			return nil, fmt.Errorf("%s: %w", d.key, err)
		}
	}
	if err := check(&config); err != nil {
		return nil, err
	}
	return &config, nil
}

func describe(err error) error {
	var missing *toml.StrictMissingError
	if errors.As(err, &missing) {
		lines := make([]string, 0, len(missing.Errors))
		for _, e := range missing.Errors {
			row, _ := e.Position()
			key := e.Key()
			lines = append(lines, fmt.Sprintf("line %d: unknown key %s", row, key[len(key)-1]))
		}
		return errors.New(strings.Join(lines, "\n"))
	}
	var decode *toml.DecodeError
	if errors.As(err, &decode) {
		row, _ := decode.Position()
		return fmt.Errorf("line %d: %w", row, err)
	}
	return err
}

// NamedBinding is a Role name with its binding.
type NamedBinding struct {
	Role    string
	Binding *RoleBinding
}

// Bindings gives each Role name with its binding.
func (r *Roles) Bindings() []NamedBinding {
	return []NamedBinding{
		{"lead", &r.Lead},
		{"triager", &r.Triager},
		{"implementer", &r.Implementer},
		{"researcher", &r.Researcher},
		{"reviewer", &r.Reviewer},
		{"judge", &r.Judge},
		{"curator", &r.Curator},
	}
}

func check(config *Config) error {
	if utf8.RuneCountInString(config.AccessPassword) < 8 {
		return errors.New("access_password: must have at least 8 characters")
	}
	if len(config.TrustedUsers) == 0 {
		return errors.New("trusted_users: must not be empty")
	}
	for _, b := range config.Roles.Bindings() {
		key := "roles." + b.Role
		harness := b.Binding.Harness
		switch {
		case harness != ClaudeCode && harness != Antigravity && harness != Devin:
			return fmt.Errorf("%s.harness: must be claude-code, antigravity or devin", key)
		case b.Binding.Model == "":
			return fmt.Errorf("%s.model: must not be empty", key)
		case harness.HasEffortLevels() && b.Binding.Effort == "":
			return fmt.Errorf("%s.effort: is required for %s", key, harness)
		case !harness.HasEffortLevels() && b.Binding.Effort != "":
			return fmt.Errorf("%s.effort: is not allowed for %s", key, harness)
		}
	}
	reviewer, implementer := config.Roles.Reviewer, config.Roles.Implementer
	if reviewer.Harness == implementer.Harness && reviewer.Model == implementer.Model {
		return errors.New("roles.reviewer: must not have the same harness and model as roles.implementer")
	}
	return nil
}
