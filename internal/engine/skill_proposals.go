package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/mcp"
)

// The kind of the Lead event that carries a proposal for the skill repository.
const skillProposalKind = "skill_proposal"

// maxSkillProposalSize is the maximum number of characters of all paths and contents of a proposal. A GitHub issue body
// has a maximum of 65,535 characters, and the Lead puts the files into one issue body.
const maxSkillProposalSize = 50000

var skillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type skillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type proposeSkillInput struct {
	Name       string      `json:"name"`
	Reason     string      `json:"reason"`
	Repository string      `json:"repository"`
	Files      []skillFile `json:"files"`
}

func (e *Engine) proposeSkillTool(c caller) mcp.Tool {
	return toolOf(e, c, noRepository, "propose_skill",
		"Propose a new skill or a change of a skill for the skill repository of the organization. Call it after you wrote a script or a command that later sessions can use again. To change a skill, read its copy in the skill folder of your working directory, change the copy, and send the full folder: a file that you do not send goes away. Mobius sends the proposal to the Lead of the Workstream \"Skills\". The tool writes nothing to GitHub. A change reaches the agents only after the Owner approves its pull request. Before you propose a new skill, read the name and the description of each skill in your skill folder. When one has the same purpose, send a change of that skill with its name.",
		map[string]any{
			"name":       map[string]any{"type": "string", "pattern": skillName.String(), "description": "The name of the skill folder. It has lowercase letters, digits and hyphens."},
			"reason":     map[string]any{"type": "string", "minLength": 1, "description": "What the skill does, and why later sessions need it."},
			"repository": map[string]any{"type": "string", "description": "Optional. The full name of the repository (owner/name) in the organization. Give it when the skill is only for the code of that repository. Else give an empty string."},
			"files": map[string]any{
				"type":        "array",
				"minItems":    1,
				"description": "The full new content of the skill folder. It must have SKILL.md.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string", "minLength": 1, "description": "The path of the file, relative to the skill folder."},
						"content": map[string]any{"type": "string", "description": "The full content of the file."},
					},
					"required":             []string{"path", "content"},
					"additionalProperties": false,
				},
			},
		},
		e.proposeSkill)
}

func checkSkillProposal(input proposeSkillInput) error {
	if !skillName.MatchString(input.Name) {
		return refuse("name must match %s.", skillName)
	}
	if empty(input.Reason) {
		return refuse("reason must not be empty.")
	}
	size := 0
	var paths []string
	for _, file := range input.Files {
		if empty(file.Path) || path.IsAbs(file.Path) || slices.Contains(strings.Split(file.Path, "/"), "..") {
			return refuse("The path %q must be relative to the skill folder and must not have a .. part.", file.Path)
		}
		if slices.Contains(paths, file.Path) {
			return refuse("The path %q occurs two times.", file.Path)
		}
		paths = append(paths, file.Path)
		size += utf8.RuneCountInString(file.Path) + utf8.RuneCountInString(file.Content)
	}
	if !slices.Contains(paths, "SKILL.md") {
		return refuse("files must have SKILL.md.")
	}
	if size > maxSkillProposalSize {
		return refuse("The paths and the contents of files have %d characters. The maximum is %d.", size, maxSkillProposalSize)
	}
	return nil
}

func (e *Engine) proposeSkill(ctx context.Context, c caller, _ github.Repository, input proposeSkillInput) (string, error) {
	if err := checkSkillProposal(input); err != nil {
		return "", err
	}
	if slices.Contains(c.agent.skills, input.Name) {
		return "", refuse("You already proposed the skill %q in this session. Do not call propose_skill again for it.", input.Name)
	}
	if input.Repository != "" {
		if _, err := e.organizationRepository(c.organization, input.Repository); err != nil {
			return "", err
		}
	}
	workstream, err := e.queries.GetSkillsWorkstream(ctx, c.organization)
	if errors.Is(err, sql.ErrNoRows) {
		return "", refuse("%s has no open Workstream with the label %s.", c.organization, skillsLabel)
	}
	if err != nil {
		return "", err
	}
	if err := e.addLeadEvent(ctx, workstream.Repository, workstream.Number, "", sql.NullInt64{}, skillProposalKind, skillProposalText(c, input)); err != nil {
		return "", err
	}
	c.agent.skills = append(c.agent.skills, input.Name)
	return fmt.Sprintf("Sent the proposal to the Lead of the Workstream \"Skills\" (%s#%d). A change reaches the agents only after the Owner approves its pull request.", workstream.Repository, workstream.Number), nil
}

func skillProposalText(c caller, input proposeSkillInput) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Proposal for the skill %q.\n\nReason: %s\n\nSession %d, role %s", input.Name, input.Reason, c.session, c.role)
	if c.repository != "" {
		fmt.Fprintf(&text, ", repository %s", c.repository)
	}
	if c.issue != 0 {
		fmt.Fprintf(&text, ", issue %s#%d", c.repository, c.issue)
	}
	text.WriteString(".")
	if input.Repository == "" {
		fmt.Fprintf(&text, "\n\nFolder: %s/", input.Name)
	} else {
		fmt.Fprintf(&text, "\n\nFolder: repos/%s/%s/", path.Base(input.Repository), input.Name)
	}
	for _, file := range input.Files {
		fence := codeFence(file.Content)
		fmt.Fprintf(&text, "\n\nFile %s:\n%s\n%s\n%s", file.Path, fence, file.Content, fence)
	}
	return text.String()
}

// codeFence gives a Markdown fence that is longer than each run of backticks in content.
func codeFence(content string) string {
	longest, run := 0, 0
	for _, r := range content {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}
