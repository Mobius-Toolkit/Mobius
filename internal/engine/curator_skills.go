package engine

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

//go:embed prompts/curator_skills.md
var curatorSkillsPrompt string

// curatorMaxInput is the maximum number of characters of the input of a tool call in a session file.
const curatorMaxInput = 4000

// startSkillsCuratorLocked starts the Curator of the skill repository of organization when curatorEvery sessions of the
// organization ended since the last Curator session of the skill repository. It does nothing when the organization has
// no skill repository or when start has it. The caller holds curatorsMu.
func (e *Engine) startSkillsCuratorLocked(ctx context.Context, organization string, start []string) error {
	skills, ok := e.github.SkillRepository(organization)
	if !ok || slices.Contains(start, skills.FullName) {
		return nil
	}
	ended, err := e.queries.CountOrganizationSessionsEndedSinceCurator(ctx, store.CountOrganizationSessionsEndedSinceCuratorParams{
		Organization:      organization,
		CuratorRepository: skills.FullName,
	})
	if err != nil || ended < curatorEvery {
		return err
	}
	return e.startCuratorLocked(ctx, skills.FullName)
}

// skillsSection writes the session files of the Curator a to a directory below scratch/ and gives the prompt section
// for them. It gives "" when repository is not the skill repository of its organization.
func (e *Engine) skillsSection(ctx context.Context, a *Agent, since time.Time) (string, error) {
	skills, ok := e.github.SkillRepository(a.spec.Organization)
	if !ok || skills.FullName != a.spec.Repository {
		return "", nil
	}
	sessions, err := e.queries.ListOrganizationSessionsEndedSince(ctx, store.ListOrganizationSessionsEndedSinceParams{
		Organization: a.spec.Organization,
		Since:        since.Format(time.RFC3339Nano),
	})
	if err != nil {
		return "", err
	}
	directory := filepath.Join(curatorScratch(e.config.DataDir, a.id), "sessions")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", err
	}
	for _, session := range sessions {
		text, err := e.sessionFile(ctx, session)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(directory, strconv.FormatInt(session.ID, 10)+".txt"), []byte(text), 0o600); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%s\nThe directory of the session files is %s. It has %d files.\n\n", curatorSkillsPrompt, directory, len(sessions)), nil
}

// curatorScratch gives the directory of the files of the Curator session id.
func curatorScratch(dataDir string, id int64) string {
	return filepath.Join(dataDir, "scratch", strconv.FormatInt(id, 10))
}

type toolCall struct {
	title, kind string
	input       json.RawMessage
}

// sessionFile gives the text of the session file of session: its role, its repository, its issue and its tool calls.
func (e *Engine) sessionFile(ctx context.Context, session store.ListOrganizationSessionsEndedSinceRow) (string, error) {
	rows, err := e.queries.ListToolCallUpdates(ctx, session.ID)
	if err != nil {
		return "", err
	}
	var ids []string
	calls := map[string]*toolCall{}
	for _, row := range rows {
		var notification struct {
			Update struct {
				ID       string          `json:"toolCallId"`
				Title    string          `json:"title"`
				Kind     string          `json:"kind"`
				RawInput json.RawMessage `json:"rawInput"`
			} `json:"update"`
		}
		if err := json.Unmarshal([]byte(row), &notification); err != nil {
			return "", err
		}
		update := notification.Update
		call, seen := calls[update.ID]
		if !seen {
			call = &toolCall{}
			calls[update.ID] = call
			ids = append(ids, update.ID)
		}
		if update.Title != "" {
			call.title = update.Title
		}
		if update.Kind != "" {
			call.kind = update.Kind
		}
		if len(update.RawInput) > 0 && string(update.RawInput) != "null" {
			call.input = update.RawInput
		}
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Role: %s\nRepository: %s\n", session.Role, session.Repository)
	if session.Issue.Valid {
		fmt.Fprintf(&text, "Issue: %d\n", session.Issue.Int64)
	}
	for _, id := range ids {
		call := calls[id]
		fmt.Fprintf(&text, "\nTitle: %s\nKind: %s\nInput: %s\n", call.title, call.kind, clipInput(call.input))
	}
	return text.String(), nil
}

// clipInput gives the compact JSON of input, cut to curatorMaxInput characters with the mark of the cut.
func clipInput(input json.RawMessage) string {
	var compact bytes.Buffer
	if json.Compact(&compact, input) != nil {
		compact.Reset()
		compact.Write(input)
	}
	runes := []rune(compact.String())
	if len(runes) <= curatorMaxInput {
		return string(runes)
	}
	return string(runes[:curatorMaxInput]) + curatorCut
}
