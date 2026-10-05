package engine

import (
	"context"
	"fmt"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

// repositorySections gives the facts file and the instruction file of role as prompt sections. Both come from the
// default branch, so a pull request cannot change them for its own agents.
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
	return sections, nil
}
