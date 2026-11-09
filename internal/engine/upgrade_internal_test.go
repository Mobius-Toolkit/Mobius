package engine

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	gh "github.com/google/go-github/v92/github"

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

// fakeRelease gives a GitHub of the fake with the release v0.3.0, and the asset of that release.
func fakeRelease(t *testing.T) (*testkit.FakeGitHub, *github.GitHub, *gh.ReleaseAsset) {
	t.Helper()
	fake := testkit.NewFakeGitHub(t)
	fake.SetLatestRelease("v0.3.0", "mobius-x86_64-unknown-linux-gnu.tar.gz")
	source, err := github.New(nil, fake.URL, fake.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	release, err := source.LatestRelease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return fake, source, release.Assets[0]
}

// program gives the program "old" in a new directory.
func program(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "mobius")
	if err := os.WriteFile(exe, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	return exe
}

func requireNoStaging(t *testing.T, exe string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(exe))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the directory of the program has %d entries, want 1", len(entries))
	}
}

func TestTheDownloadAndTheSwapInstallTheReleaseFileOfMobiusGo(t *testing.T) {
	t.Parallel()
	fake, source, asset := fakeRelease(t)
	exe := program(t)

	staging, err := download(t.Context(), source, "v0.3.0", asset, exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := swap(staging, exe); err != nil {
		t.Fatal(err)
	}

	if got := content(t, exe); got != "v0.3.0/mobius-x86_64-unknown-linux-gnu.tar.gz" {
		t.Errorf("program = %q", got)
	}
	var paths []string
	for _, request := range fake.Requests() {
		paths = append(paths, request.Path)
	}
	want := []string{"/repos/Mobius-Toolkit/Mobius/releases/latest", "/repos/Mobius-Toolkit/Mobius/releases/assets/1", "/release-storage/1"}
	if !slices.Equal(paths, want) {
		t.Errorf("requests = %q, want %q", paths, want)
	}
}

func TestADownloadThatGetsAnErrorStatusFailsWithTheTagAndTheAssetAndLeavesNoStagingDirectory(t *testing.T) {
	t.Parallel()
	fake, source, asset := fakeRelease(t)
	fake.FailReleaseAssets(http.StatusBadGateway)
	exe := program(t)

	_, err := download(t.Context(), source, "v0.3.0", asset, exe)

	if err == nil || !strings.Contains(err.Error(), "v0.3.0") || !strings.Contains(err.Error(), "mobius-x86_64-unknown-linux-gnu.tar.gz") {
		t.Errorf("error = %v", err)
	}
	requireNoStaging(t, exe)
}

// Serial: the test sets the stall limit, which is a package variable.
func TestADownloadThatGetsNoByteWithinTheStallLimitFails(t *testing.T) {
	fake, source, asset := fakeRelease(t)
	github.DownloadStallLimit = 100 * time.Millisecond
	t.Cleanup(func() { github.DownloadStallLimit = time.Minute })
	hung := fake.HangNext("GET /release-storage/{id}")
	exe := program(t)

	_, err := download(t.Context(), source, "v0.3.0", asset, exe)

	select {
	case <-hung:
	default:
		t.Error("the download did not reach the storage route")
	}
	if err == nil || !strings.Contains(err.Error(), "no byte") {
		t.Errorf("error = %v", err)
	}
	requireNoStaging(t, exe)
}
