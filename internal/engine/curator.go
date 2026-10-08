package engine

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

//go:embed prompts/curator.md
var curatorPrompt string

// curatorEvery is the number of ended sessions of a repository, with no Curator session, that start a Curator.
const curatorEvery = 10

// curatorKey is the key in e.stops of the Worker of the Curator with this session id.
type curatorKey int64

type editMemoryInput struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// startCuratorAfterEnds starts a Curator for repository when curatorEvery sessions of the repository ended since the
// last Curator session.
func (e *Engine) startCuratorAfterEnds(ctx context.Context, repository string) error {
	e.curatorsMu.Lock()
	defer e.curatorsMu.Unlock()
	ended, err := e.queries.CountSessionsEndedSinceCurator(ctx, repository)
	if err != nil || ended < curatorEvery {
		return err
	}
	return e.startCuratorLocked(ctx, repository)
}

// startCurator starts a Curator for repository. While a Curator of repository runs, it makes one more Curator wait
// for the end of the running Curator.
func (e *Engine) startCurator(ctx context.Context, repository string) error {
	e.curatorsMu.Lock()
	defer e.curatorsMu.Unlock()
	return e.startCuratorLocked(ctx, repository)
}

// startCuratorLocked is startCurator for a caller that holds curatorsMu.
func (e *Engine) startCuratorLocked(ctx context.Context, repository string) error {
	if _, running := e.curators[repository]; running {
		e.curators[repository] = true
		return nil
	}
	e.curators[repository] = false
	return e.runCurator(ctx, repository)
}

// runCurator adds a Curator session for repository and runs it in the background. At its end, the Curator that
// waits starts. The caller holds curatorsMu and has set e.curators[repository].
func (e *Engine) runCurator(ctx context.Context, repository string) error {
	organization, _, _ := strings.Cut(repository, "/")
	a, err := e.newAgent(ctx, Spec{Role: CuratorRole, Organization: organization, Repository: repository})
	if err != nil {
		delete(e.curators, repository)
		return err
	}
	key := curatorKey(a.id)
	started := e.startWorker(key, func(ctx context.Context) {
		defer e.stop(key)
		if err := e.curate(ctx, a); err != nil {
			log.Printf("Curator %d of %s: %v", a.id, repository, err)
		}
		e.curatorsMu.Lock()
		defer e.curatorsMu.Unlock()
		if !e.curators[repository] || ctx.Err() != nil {
			delete(e.curators, repository)
			return
		}
		e.curators[repository] = false
		if err := e.runCurator(ctx, repository); err != nil {
			log.Printf("start the next Curator of %s: %v", repository, err)
		}
	})
	if !started {
		delete(e.curators, repository)
		return errors.Join(refuse("Mobius stops, so no Curator starts now."), a.End(context.WithoutCancel(ctx), "declined"))
	}
	return nil
}

// curate runs the session a of the Curator in a worktree that is detached at the default branch.
func (e *Engine) curate(ctx context.Context, a *Agent) error {
	if err := a.waitForSlot(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	repository := a.spec.Repository
	a.spec.Dir = runner.CuratorDir(e.config.DataDir, repository, a.id)
	err := e.curateTurn(ctx, a)
	a.closeHarness()
	ended := context.WithoutCancel(ctx)
	err = errors.Join(err, e.removeCopy(ended, repository, a.spec.Dir))
	switch {
	case ctx.Err() != nil:
		return a.End(ended, "stopped")
	case errors.Is(err, errHung):
		return a.endHung(ended)
	case err != nil:
		return a.Fail(ended, err)
	}
	return a.End(ended, "done")
}

// curateTurn makes the worktree of the Curator a and runs its turn.
func (e *Engine) curateTurn(ctx context.Context, a *Agent) error {
	repository, err := e.repository(a.spec.Repository)
	if err != nil {
		return err
	}
	if err := e.addCopy(ctx, repository, a.spec.Dir); err != nil {
		return err
	}
	sections, err := e.repositorySections(ctx, repository, CuratorRole)
	if err != nil {
		return err
	}
	leads, err := e.leadMemories(repository.FullName)
	if err != nil {
		return err
	}
	if err := a.open(ctx); err != nil {
		return err
	}
	return a.Prompt(ctx, curatorPrompt+"\n"+sections+leads, nil)
}

// leadMemories gives the MEMORY.md of each Lead of repository as prompt sections.
func (e *Engine) leadMemories(repository string) (string, error) {
	dir := filepath.Join(e.config.DataDir, "leads", repository)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	sections := ""
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		text, err := readMemory(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", err
		}
		if text = strings.TrimSpace(text); text != "" {
			sections += fmt.Sprintf("# MEMORY.md of the Lead of Workstream %s\n\n%s\n\n", entry.Name(), text)
		}
	}
	return sections, nil
}

// editMemory replaces the one occurrence of input.Old in the memory file of the repository of c with input.New. An
// empty input.Old adds input.New at the end of the file.
func (e *Engine) editMemory(ctx context.Context, c caller, _ github.Repository, input editMemoryInput) (string, error) {
	text, err := e.readMemory(c.repository)
	if err != nil {
		return "", err
	}
	if input.Old == "" {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += input.New
	} else {
		switch count := strings.Count(text, input.Old); count {
		case 0:
			return "", refuse("The old text does not occur in the memory file.")
		case 1:
		default:
			return "", refuse("The old text occurs %d times in the memory file. Make it longer, so that it occurs one time.", count)
		}
		text = strings.Replace(text, input.Old, input.New, 1)
	}
	if err := e.SaveMemory(ctx, c.repository, "curator", text); err != nil {
		return "", err
	}
	return "Saved the memory file.", nil
}
