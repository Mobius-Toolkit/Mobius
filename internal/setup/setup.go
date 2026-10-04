// Package setup writes a new config file with the answers of the operator.
package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"

	"github.com/Mobius-Toolkit/mobius-go/internal/config"
	"github.com/Mobius-Toolkit/mobius-go/internal/runner"
)

const (
	startTimeout = 60 * time.Second
	// Antigravity waits 300 s for the browser.
	loginTimeout = 360 * time.Second
	// The Harness gets no Mobius tools here, so the MCP URL has no server.
	noMCPURL = "http://127.0.0.1:1/mcp/init"
)

type offer struct {
	harness config.Harness
	models  runner.Choices
	efforts runner.Choices
}

type binding struct {
	Harness config.Harness `toml:"harness"`
	Model   string         `toml:"model"`
	Effort  string         `toml:"effort,omitempty"`
}

type configFile struct {
	AccessPassword string   `toml:"access_password"`
	TrustedUsers   []string `toml:"trusted_users"`
	Roles          struct {
		Lead        binding `toml:"lead,inline"`
		Triager     binding `toml:"triager,inline"`
		Implementer binding `toml:"implementer,inline"`
		Researcher  binding `toml:"researcher,inline"`
		Reviewer    binding `toml:"reviewer,inline"`
		Judge       binding `toml:"judge,inline"`
	} `toml:"roles"`
}

var titles = map[config.Harness]string{
	config.ClaudeCode:  "Claude Code",
	config.Antigravity: "Antigravity",
	config.Devin:       "Devin",
}

type terminal struct {
	in  *bufio.Reader
	out io.Writer
}

func (t terminal) say(format string, args ...any) {
	_, _ = fmt.Fprintf(t.out, format, args...)
}

// Run asks for the values of a new config file and writes it to configPath.
// It offers each Harness on path with the models and efforts that the Harness gives.
func Run(ctx context.Context, in io.Reader, out io.Writer, configPath, path string) error {
	t := terminal{in: bufio.NewReader(in), out: out}
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("%s exists: `mobius init` writes only a new file", configPath)
	}
	var file configFile
	for {
		first, err := t.ask("Access password (8 characters or more): ")
		if err != nil {
			return err
		}
		if utf8.RuneCountInString(first) < 8 {
			t.say("The password has less than 8 characters.\n")
			continue
		}
		again, err := t.ask("Access password again: ")
		if err != nil {
			return err
		}
		if again == first {
			file.AccessPassword = first
			break
		}
		t.say("The two passwords are not the same.\n")
	}
	for len(file.TrustedUsers) == 0 {
		line, err := t.ask("GitHub logins of the trusted users, with spaces between them: ")
		if err != nil {
			return err
		}
		file.TrustedUsers = strings.Fields(line)
		if len(file.TrustedUsers) == 0 {
			t.say("Give one login or more.\n")
		}
	}
	offers, err := offers(ctx, t, path)
	if err != nil {
		return err
	}
	models := 0
	for _, o := range offers {
		models += len(o.models.Values)
	}
	if models < 2 {
		return errors.New("the Harnesses gave less than 2 models, but the Reviewer needs another Harness or another model than the Implementer")
	}
	roles := []struct {
		title   string
		binding *binding
	}{
		{"Lead", &file.Roles.Lead},
		{"Triager", &file.Roles.Triager},
		{"Implementer", &file.Roles.Implementer},
		{"Researcher", &file.Roles.Researcher},
		{"Reviewer", &file.Roles.Reviewer},
		{"Judge", &file.Roles.Judge},
	}
	for _, role := range roles {
		for {
			b, err := t.choose(offers, role.title)
			if err != nil {
				return err
			}
			implementer := file.Roles.Implementer
			if role.title == "Reviewer" && b.Harness == implementer.Harness && b.Model == implementer.Model {
				t.say("The Reviewer needs another Harness or another model than the Implementer.\n")
				continue
			}
			*role.binding = b
			break
		}
	}
	text, err := toml.Marshal(file)
	if err != nil {
		return err
	}
	if _, err := config.Parse(text); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		return err
	}
	// The file holds the access password.
	if err := os.WriteFile(configPath, text, 0o600); err != nil {
		return err
	}
	t.say("Mobius wrote %s.\nStart Mobius with `mobius`.\n", configPath)
	return nil
}

func offers(ctx context.Context, t terminal, path string) ([]offer, error) {
	dir, err := os.MkdirTemp("", "mobius-init-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	var offers []offer
	for _, harness := range config.Harnesses {
		title := titles[harness]
		program := runner.Program(harness)
		if len(runner.Missing(path, program)) > 0 {
			t.say("%s: `%s` is not on PATH\n", title, program)
			continue
		}
		if harness == config.Antigravity {
			loginCtx, cancel := context.WithTimeout(ctx, loginTimeout)
			loggedIn, err := runner.LogInAntigravity(loginCtx, dir, dir, path)
			timedOut := loginCtx.Err() != nil
			cancel()
			switch {
			case timedOut:
				t.say("%s: the login did not end\n", title)
				continue
			case err != nil:
				t.say("%s: %v\n", title, err)
				continue
			case loggedIn:
				t.say("%s: logged in\n", title)
			}
		}
		startCtx, cancel := context.WithTimeout(ctx, startTimeout)
		session, err := runner.Start(startCtx, harness, dir, dir, path, noMCPURL, "", func(json.RawMessage) {})
		timedOut := startCtx.Err() != nil
		cancel()
		switch {
		case timedOut:
			t.say("%s: the Harness gave no answer to session/new\n", title)
			continue
		case err != nil:
			t.say("%s: %v\n", title, err)
			continue
		}
		o := offer{harness: harness, models: session.Models(), efforts: session.Efforts()}
		session.Close()
		if len(o.models.Values) == 0 {
			t.say("%s: the Harness gave no model option\n", title)
			continue
		}
		if harness.HasEffortLevels() && len(o.efforts.Values) == 0 {
			t.say("%s: the Harness gave no thought_level option\n", title)
			continue
		}
		offers = append(offers, o)
	}
	return offers, nil
}

func (t terminal) choose(offers []offer, role string) (binding, error) {
	t.say("\n%s\n", role)
	names := make([]string, 0, len(offers))
	for _, o := range offers {
		names = append(names, string(o.harness))
	}
	index, err := t.pick("Harness", names, 0)
	if err != nil {
		return binding{}, err
	}
	o := offers[index]
	model, err := t.pickCurrent("Model", o.models)
	if err != nil {
		return binding{}, err
	}
	b := binding{Harness: o.harness, Model: model}
	if o.harness.HasEffortLevels() {
		if b.Effort, err = t.pickCurrent("Effort", o.efforts); err != nil {
			return binding{}, err
		}
	}
	return b, nil
}

func (t terminal) pickCurrent(label string, choices runner.Choices) (string, error) {
	current := max(0, slices.Index(choices.Values, choices.Current))
	index, err := t.pick(label, choices.Values, current)
	if err != nil {
		return "", err
	}
	return choices.Values[index], nil
}

func (t terminal) pick(label string, values []string, def int) (int, error) {
	for i, value := range values {
		t.say("  %d) %s\n", i+1, value)
	}
	for {
		line, err := t.ask(fmt.Sprintf("%s [%d]: ", label, def+1))
		if err != nil {
			return 0, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return def, nil
		}
		if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(values) {
			return n - 1, nil
		}
		t.say("Give a number from 1 to %d.\n", len(values))
	}
}

func (t terminal) ask(question string) (string, error) {
	t.say("%s", question)
	line, err := t.in.ReadString('\n')
	if errors.Is(err, io.EOF) && line == "" {
		return "", errors.New("the input ended before the last answer")
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	// The password can start or end with a space.
	return strings.TrimRight(line, "\r\n"), nil
}
