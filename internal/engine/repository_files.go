package engine

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

// serverSection gives the operating system of the server as a prompt section.
func serverSection() string {
	text := fmt.Sprintf("The server runs %s.", runtime.GOOS)
	if runtime.GOOS == "darwin" {
		text = "The server runs macOS. The shell tools are the BSD tools.\n" +
			"The option -i of sed needs an argument for the suffix of the backup file. For no backup, use an empty suffix: sed -i '' 's|old|new|' file\n" +
			"The commands a, i and c of the BSD sed need a backslash and a new line before the text."
	}
	return fmt.Sprintf("# Server\n\n%s\n\n", text)
}

// repositorySections gives the server section, the facts file, the instruction file of role and the memory files as
// prompt sections. The facts file and the instruction file come from the default branch, so a pull request cannot change
// them for its own agents. The memory files come from the data directory.
func (e *Engine) repositorySections(ctx context.Context, repository github.Repository, role string) (string, error) {
	if err := e.fetchRepository(ctx, repository); err != nil {
		if role != "lead" {
			return "", err
		}
		log.Printf("fetch of %s for the Lead failed, use the last fetch: %v", repository.FullName, err)
	}
	dataDir := e.config.DataDir
	e.gitMu.Lock()
	defer e.gitMu.Unlock()
	sections := serverSection()
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
	memory, err := e.memorySections(repository.FullName, role)
	if err != nil {
		return "", err
	}
	return sections + memory, nil
}

// memorySections gives the shared memory file and the memory files of the role as prompt sections. The Curator gets each
// role file in the section "Memory of the roles". Each other role gets only its own file in the section "Memory of the
// role". An empty file gives no section.
func (e *Engine) memorySections(repository, role string) (string, error) {
	shared, err := e.readMemory(repository, MemoryFileShared)
	if err != nil {
		return "", err
	}
	var sections strings.Builder
	if shared = strings.TrimSpace(shared); shared != "" {
		fmt.Fprintf(&sections, "# Memory\n\n%s\n\n", shared)
	}
	if role == CuratorRole {
		var roles strings.Builder
		for _, file := range memoryRoleFiles {
			text, err := e.readMemory(repository, file)
			if err != nil {
				return "", err
			}
			if text = strings.TrimSpace(text); text == "" {
				continue
			}
			fmt.Fprintf(&roles, "## %s\n\n%s\n\n", file, text)
		}
		if roles.Len() > 0 {
			fmt.Fprintf(&sections, "# Memory of the roles\n\n%s", roles.String())
		}
		return sections.String(), nil
	}
	text, err := e.readMemory(repository, role)
	if err != nil {
		return "", err
	}
	if text = strings.TrimSpace(text); text != "" {
		fmt.Fprintf(&sections, "# Memory of the role\n\n%s\n\n", text)
	}
	return sections.String(), nil
}

func (e *Engine) fetchRepository(ctx context.Context, repository github.Repository) error {
	token, err := repository.Token(ctx)
	if err != nil {
		return err
	}
	e.gitMu.Lock()
	defer e.gitMu.Unlock()
	return e.fetch(ctx, repository, token)
}

// fetch fetches the bare clone of repository. When the repository has no branch at all, it pushes the first commit to
// the default branch as the bot of the App. The caller holds e.gitMu.
func (e *Engine) fetch(ctx context.Context, repository github.Repository, token string) error {
	dataDir, name := e.config.DataDir, repository.FullName
	if err := runner.Fetch(ctx, dataDir, name, repository.CloneURL, token); err != nil {
		return err
	}
	found, err := runner.HasRemoteBranches(ctx, dataDir, name)
	if err != nil || found {
		return err
	}
	login := appLogin(repository.AppSlug)
	id, err := repository.UserID(ctx, login)
	if err != nil {
		return err
	}
	email := fmt.Sprintf("%d+%s@users.noreply.github.com", id, login)
	return runner.StartBranch(ctx, dataDir, name, repository.CloneURL, token, repository.DefaultBranch, login, email)
}
