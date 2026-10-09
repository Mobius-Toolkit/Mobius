package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestANewerReleaseTagCountsEachNumber(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.57", "v0.2.0", true},
		{"v1.9.9", "v2.0.0", true},
		{"v0.1.9", "v0.1.10", true},
		{"v0.1.57", "v0.1.57", false},
		{"v0.1.10", "v0.1.9", false},
		{"v2.0.0", "v1.9.9", false},
		{"v0.1.57", "latest", false},
		{"v0.1.57", "v0.1", false},
		{"local", "v0.2.0", false},
	} {
		if got := newerRelease(c.current, c.latest); got != c.want {
			t.Errorf("newerRelease(%s, %s) = %v", c.current, c.latest, got)
		}
	}
}

// staged gives the program "old" in a new directory, and a directory next to it with the new program "new".
func staged(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "mobius")
	if err := os.WriteFile(exe, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	staging, err := os.MkdirTemp(dir, ".mobius-upgrade-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "mobius"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	return staging, exe
}

func content(t *testing.T, path string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func TestASwapReplacesTheProgram(t *testing.T) {
	t.Parallel()
	staging, exe := staged(t)

	if err := swap(staging, exe); err != nil {
		t.Fatal(err)
	}

	if got := content(t, exe); got != "new" {
		t.Errorf("program = %q", got)
	}
}

func TestAFailedSwapRestoresTheOldProgram(t *testing.T) {
	t.Parallel()
	staging, exe := staged(t)
	// The directory staging has no new program, so the second rename of the swap fails.
	if err := os.Remove(filepath.Join(staging, "mobius")); err != nil {
		t.Fatal(err)
	}

	if err := swap(staging, exe); err == nil {
		t.Fatal("the swap did not fail")
	}

	if got := content(t, exe); got != "old" {
		t.Errorf("program = %q", got)
	}
}

func TestTheDownloadAndTheSwapInstallTheReleaseFileOfMobiusGo(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.SetLatestRelease("v0.3.0", "mobius-x86_64-unknown-linux-gnu.tar.gz")
	gh, err := github.New(nil, fake.URL, fake.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "mobius")
	if err := os.WriteFile(exe, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	staging, err := download(t.Context(), gh.ReleaseURL("v0.3.0", "mobius-x86_64-unknown-linux-gnu.tar.gz"), exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := swap(staging, exe); err != nil {
		t.Fatal(err)
	}

	if got := content(t, exe); got != "v0.3.0/mobius-x86_64-unknown-linux-gnu.tar.gz" {
		t.Errorf("program = %q", got)
	}
}
