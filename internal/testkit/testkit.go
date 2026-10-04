// Package testkit holds the fake GitHub, the fake agent as a Harness command, and the waits of the tests.
// It imports no other Mobius package, so the tests of each package can use it.
package testkit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/mobius-go/internal/testkit/fakeagent"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit/fakedf"
)

// Main runs the tests of the package. When the test binary runs as a Harness command
// of InstallFakeHarness, Main runs the fake agent in place of the tests, and as the df of SetFreeSpace, it gives the
// free space of SetFreeSpace.
func Main(m *testing.M) {
	if filepath.Base(os.Args[0]) == "df" {
		if err := fakedf.Run(filepath.Clean(os.Args[0] + ".free")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if slices.Contains(harnessPrograms, filepath.Base(os.Args[0])) {
		if err := fakeagent.Run(filepath.Clean(os.Args[0] + ".toml")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

// The Harness commands of the Roles.
var harnessPrograms = []string{"claude-agent-acp", "agy_acp_server", "devin"}

// InstallFakeHarness makes the Harness command program in dataDir/harnesses run the fake agent with script.
// The program is "claude-agent-acp", "agy_acp_server" or "devin". The command is a link to the test binary,
// so the test package must call Main from its TestMain.
// The fake agent writes the files env, pwd and mcp_url to dataDir/harnesses.
func InstallFakeHarness(t testing.TB, dataDir, program, script string) {
	t.Helper()
	harnesses := filepath.Join(dataDir, "harnesses")
	if err := os.MkdirAll(harnesses, 0o750); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(harnesses, program)
	if err := os.WriteFile(command+".toml", []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, command); err != nil && !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
}

// SetFreeSpace makes the program df in dataDir/harnesses give kibibytes KiB of free space, also as the text of the file
// dataDir/harnesses/df.free. The program is a link to the test binary, so the test package must call Main from its
// TestMain.
func SetFreeSpace(t testing.TB, dataDir string, kibibytes int64) {
	t.Helper()
	harnesses := filepath.Join(dataDir, "harnesses")
	if err := os.MkdirAll(harnesses, 0o750); err != nil {
		t.Fatal(err)
	}
	df := filepath.Join(harnesses, "df")
	if err := os.WriteFile(df+".free", []byte(strconv.FormatInt(kibibytes, 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, df); err != nil && !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
}

// InstallFakeAgent installs the fake agent with script as each Harness command.
func InstallFakeAgent(t testing.TB, dataDir, script string) {
	t.Helper()
	for _, program := range harnessPrograms {
		InstallFakeHarness(t, dataDir, program, script)
	}
}

// WaitFor waits until condition is true. The test fails after one minute.
func WaitFor(t testing.TB, condition func() bool) {
	t.Helper()
	WaitForValue(t, func() (struct{}, bool) { return struct{}{}, condition() })
}

// WaitForValue waits until check gives true, and gives its value. The test fails after one minute.
func WaitForValue[T any](t testing.TB, check func() (T, bool)) T {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		if value, ok := check(); ok {
			return value
		}
		if time.Now().After(deadline) {
			t.Fatal("the condition is not true after one minute")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
