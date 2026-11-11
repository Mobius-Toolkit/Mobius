package engine

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

// copySkillsWaitsForALock tells if a goroutine waits for a mutex inside copySkills.
func copySkillsWaitsForALock() bool {
	stacks := make([]byte, 1<<20)
	stacks = stacks[:runtime.Stack(stacks, true)]
	for stack := range strings.SplitSeq(string(stacks), "\n\n") {
		if strings.Contains(stack, "(*Agent).copySkills") && strings.Contains(stack, "sync.Mutex.Lock") {
			return true
		}
	}
	return false
}

func TestASkillCopyIntoAWorktreeWaitsForTheGitLockBeforeItConfiguresTheWorktree(t *testing.T) {
	t.Parallel()
	e := researchEngine(t)
	binding, _ := e.roleBinding(ResearcherRole)
	e.setAccess(keyOf(binding), nil)
	dir := t.TempDir()
	initialize := exec.CommandContext(t.Context(), "git", "init", "-q")
	initialize.Dir = dir
	initialize.WaitDelay = 5 * time.Second
	if out, err := initialize.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	a, err := e.newAgent(t.Context(), Spec{Role: ResearcherRole, Organization: "owner", Repository: "owner/shop", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	e.gitMu.Lock()
	finished := make(chan error, 1)
	go func() { finished <- a.copySkills(t.Context()) }()

	testkit.WaitFor(t, func() bool { return len(finished) > 0 || copySkillsWaitsForALock() })

	if len(finished) > 0 {
		t.Errorf("copySkills ended while the git lock was held: %v", <-finished)
		return
	}
	e.gitMu.Unlock()
	if err := <-finished; err != nil {
		t.Errorf("error = %v", err)
	}
}
