package engine_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/runner"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestTheEventReadyForLeadApprovalHasTheLineWithTheNameAndTheConclusionOfEachCheckRunOfTheHeadAndTheLineWithTheChangedFiles(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "success"))
	fake.AddCheckRun(shop, checkRun("lint", sha, "completed", "neutral"))

	prompt := waitForLeadPrompt(t, server, " ready for Lead approval of #41 \"Add plan model\"")
	wantCI := fmt.Sprintf("CI of head %.8s: build success, lint neutral", sha)
	if !strings.Contains(prompt, wantCI) {
		t.Errorf("prompt missing CI line %q: %s", wantCI, prompt)
	}
	wantFiles := "Changed files (1): plan.txt"
	if !strings.Contains(prompt, wantFiles) {
		t.Errorf("prompt missing changed files line %q: %s", wantFiles, prompt)
	}
}

func TestAPullRequestWith101ChangedFilesGives100PathsAndAnd1More(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	filesScript := "[[prompts]]\n" +
		"shell = '''for i in $(seq 1 101); do echo cents > file_$i.txt && git add file_$i.txt; done && git commit -q -m \"Add 101 files $(basename $PWD)\"'''\n"
	server, _ := connectTask(t, fake, leadStarts, filesScript, longGrace)
	sha := checksHead(t, server, fake)
	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "success"))

	prompt := waitForLeadPrompt(t, server, " ready for Lead approval of #41 \"Add plan model\"")
	if !strings.Contains(prompt, "Changed files (101): ") {
		t.Fatalf("prompt missing 'Changed files (101): ': %s", prompt)
	}
	if !strings.Contains(prompt, ", and 1 more") {
		t.Fatalf("prompt missing ', and 1 more': %s", prompt)
	}
	line := prompt[strings.Index(prompt, "Changed files (101): "):]
	if idx := strings.Index(line, "\n"); idx >= 0 {
		line = line[:idx]
	}
	rest := strings.TrimPrefix(line, "Changed files (101): ")
	beforeMore, found := strings.CutSuffix(rest, ", and 1 more")
	if !found {
		t.Fatalf("line missing suffix ', and 1 more': %q", line)
	}
	paths := strings.Split(beforeMore, ", ")
	if len(paths) != 100 {
		t.Errorf("got %d paths before 'and 1 more', want 100", len(paths))
	}
}

func TestANewCommitOnTheDefaultBranchThenALeadEventGivesTheCommitInTheBareCloneAndPrompt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)

	fake.CommitFile(shop, "extra.txt", "content\n", "New commit on main")
	commit := head(t, fake, "main")

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "success"))

	prompt := waitForLeadPrompt(t, server, " ready for Lead approval of #41 \"Add plan model\"")
	wantPrefix := fmt.Sprintf("Mobius fetched the bare clone of %s at ", shop)
	if !strings.Contains(prompt, wantPrefix) {
		t.Errorf("prompt missing fetch prefix %q: %s", wantPrefix, prompt)
	}
	wantCommit := fmt.Sprintf("origin/main is at %.8s.", commit)
	if !strings.Contains(prompt, wantCommit) {
		t.Errorf("prompt missing commit line %q: %s", wantCommit, prompt)
	}
	bareCommit, err := runner.BareRevParse(context.Background(), dataDir, shop, "origin/main")
	if err != nil {
		t.Fatalf("bare rev-parse failed: %v", err)
	}
	if bareCommit != commit {
		t.Errorf("bare clone has commit %s, want %s", bareCommit, commit)
	}
}

func TestAFailedFetchRunsTheTurnWithNoFetchLineInItsPrompt(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, dataDir := connectTask(t, fake, leadStarts, commits, longGrace)
	sha := checksHead(t, server, fake)

	testkit.Git(t, filepath.Join(dataDir, "repos", shop+".git"), "remote", "set-url", "origin", t.TempDir()+"/missing")

	fake.AddCheckRun(shop, checkRun("build", sha, "completed", "success"))

	prompt := waitForLeadPrompt(t, server, " ready for Lead approval of #41 \"Add plan model\"")
	if strings.Contains(prompt, "Mobius fetched the bare clone of") {
		t.Errorf("prompt contains fetch line despite failed fetch: %s", prompt)
	}
}
