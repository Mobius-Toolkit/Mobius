package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

// repositorySections gives the facts file, the instruction file of role and the memory file as prompt sections. The
// facts file and the instruction file come from the default branch, so a pull request cannot change them for its own
// agents. The memory file comes from the data directory and is the same for each role.
func (e *Engine) repositorySections(ctx context.Context, repository github.Repository, role string) (string, error) {
	token, err := repository.Token(ctx)
	if err != nil {
		return "", err
	}
	e.gitMu.Lock()
	defer e.gitMu.Unlock()
	dataDir := e.config.DataDir
	if err := runner.Fetch(ctx, dataDir, repository.FullName, repository.CloneURL, token); err != nil {
		return "", err
	}
	sections := ""
	for _, file := range []struct{ heading, path string }{
		{"Repository facts", "AGENTS.md"},
		{"Role instructions", ".mobius/roles/" + role + ".md"},
	} {
		text, ok, err := runner.Show(ctx, dataDir, repository.FullName, repository.DefaultBranch, file.path)
		if err != nil {
			return "", err
		}
		if ok {
			sections += fmt.Sprintf("# %s\n\n%s\n\n", file.heading, text)
		}
	}
	memory, err := e.readMemory(repository.FullName)
	if err != nil {
		return "", err
	}
	if memory = strings.TrimSpace(memory); memory != "" {
		sections += fmt.Sprintf("# Memory\n\n%s\n\n", memory)
	}
	return sections, nil
}
