package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

const dshHeader = `{"type":"session","version":4,"id":"fake-session","cwd":"/tmp"}`

func dshMessage(turn int, provider, model string, input, output, cacheRead, cacheWrite int) string {
	return fmt.Sprintf(`{"type":"assistant/message","time":1,"data":{"turn":%d,"message":{"source":{"provider":%q,"model":%q}},"usage":{"inputTokens":%d,"outputTokens":%d,"cacheReadTokens":%d,"cacheWriteTokens":%d,"totalTokens":0}}}`,
		turn, provider, model, input, output, cacheRead, cacheWrite)
}

// writeDSHLog writes one zstd frame of lines for each element of frames.
func writeDSHLog(t *testing.T, path string, frames ...[]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, lines := range frames {
		encoder, err := zstd.NewWriter(file, zstd.WithEncoderConcurrency(1))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range lines {
			if _, err := io.WriteString(encoder, line+"\n"); err != nil {
				t.Fatal(err)
			}
		}
		if err := encoder.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTheDSHProjectKeyUsesTheRulesOfDSH(t *testing.T) {
	t.Parallel()
	for cwd, want := range map[string]string{
		"/Users/bohdan/.mobius/worktrees/Mobius-Toolkit/Mobius/task-1357": "--Users-bohdan-.mobius-worktrees-Mobius-Toolkit-Mobius-task-1357--",
		"/Users/bohdan/My Project/.cache":                                 "--Users-bohdan-My~0020Project-.cache--",
		`C:\Users\name\a:b`:                                               "--C-Users-name-a-b--",
		"/a~b":                                                            "--a~007Eb--",
		"/":                                                               "--root--",
	} {
		if got := dshProjectKey(cwd); got != want {
			t.Errorf("dshProjectKey(%q) = %q, want %q", cwd, got, want)
		}
	}
}

func TestTheDSHSessionSegmentUsesTheRulesOfDSH(t *testing.T) {
	t.Parallel()
	for session, want := range map[string]string{
		"fea97f6d-0383-47ec-bde3-aa9251fae704": "fea97f6d-0383-47ec-bde3-aa9251fae704",
		".":                                    "~002E",
		"..":                                   "~002E~002E",
		"a/b":                                  "a~002Fb",
	} {
		if got := dshEncodeSegment(session); got != want {
			t.Errorf("dshEncodeSegment(%q) = %q, want %q", session, got, want)
		}
	}
}

func TestReadDSHUsageSumsTheNewRecordsOfTwoFrames(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cwd := "/Users/bohdan/My Project/.cache"
	session := "session-1"
	path := dshLogPath(home, cwd, session)
	writeDSHLog(t, path, []string{
		dshHeader,
		dshMessage(1, "deepseek-account", "deepseek-v4-pro", 100, 10, 1000, 5),
		dshMessage(1, "deepseek-account", "deepseek-v4-pro", 200, 20, 2000, 0),
		`{"type":"turn/end","time":2,"data":{}}`,
	}, []string{
		`{"type":"other","time":3,"data":{}}`,
		dshMessage(2, "deepseek-account", "deepseek-flash", 300, 30, 3000, 7),
	})

	all, err := readDSHUsage(home, cwd, session, 0)
	if err != nil {
		t.Fatal(err)
	}
	if all.records != 3 || all.input != 600 || all.output != 60 || all.cacheRead != 6000 || all.cacheWrite != 12 || !all.tokens ||
		all.model != "deepseek-account/deepseek-flash" {
		t.Errorf("all = %+v", all)
	}

	afterTwo, err := readDSHUsage(home, cwd, session, 2)
	if err != nil {
		t.Fatal(err)
	}
	if afterTwo.records != 3 || afterTwo.input != 300 || afterTwo.output != 30 || afterTwo.cacheRead != 3000 ||
		afterTwo.cacheWrite != 7 || !afterTwo.tokens || afterTwo.model != "deepseek-account/deepseek-flash" {
		t.Errorf("after two = %+v", afterTwo)
	}

	none, err := readDSHUsage(home, cwd, session, 3)
	if err != nil {
		t.Fatal(err)
	}
	if none.records != 3 || none.tokens || none.model != "" {
		t.Errorf("none = %+v", none)
	}
}

func TestReadDSHUsageSkipsARecordWithNoUsage(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cwd := "/work/task"
	session := "session-2"
	path := dshLogPath(home, cwd, session)
	writeDSHLog(t, path, []string{
		dshHeader,
		`{"type":"assistant/message","time":1,"data":{"turn":1,"message":{"source":{"provider":"deepseek-account","model":"deepseek-v4-pro"}}}}`,
		dshMessage(1, "deepseek-account", "deepseek-v4-pro", 10, 2, 30, 4),
	})

	usage, err := readDSHUsage(home, cwd, session, 0)
	if err != nil {
		t.Fatal(err)
	}
	if usage.records != 2 || usage.input != 10 || usage.output != 2 || usage.cacheRead != 30 || usage.cacheWrite != 4 || !usage.tokens {
		t.Errorf("usage = %+v", usage)
	}

	writeDSHLog(t, path, []string{
		dshHeader,
		`{"type":"assistant/message","time":1,"data":{"turn":1,"message":{"source":{"provider":"deepseek-account","model":"deepseek-v4-pro"}}}}`,
	})
	noUsage, err := readDSHUsage(home, cwd, session, 0)
	if err != nil {
		t.Fatal(err)
	}
	if noUsage.records != 1 || noUsage.tokens || noUsage.model != "deepseek-account/deepseek-v4-pro" {
		t.Errorf("no usage = %+v", noUsage)
	}
}

func TestReadDSHUsageRefusesAnotherVersion(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cwd := "/work/task"
	session := "session-3"
	path := dshLogPath(home, cwd, session)
	writeDSHLog(t, path, []string{
		`{"type":"session","version":5,"id":"fake-session","cwd":"/work/task"}`,
		dshMessage(1, "deepseek-account", "deepseek-v4-pro", 10, 2, 30, 4),
	})

	usage, err := readDSHUsage(home, cwd, session, 0)
	if err == nil {
		t.Fatal("the version 5 log gave no error")
	}
	if usage.tokens || usage.records != 0 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestReadDSHUsageGivesAnErrorForAMissingOrBrokenLog(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cwd := "/work/task"
	if _, err := readDSHUsage(home, cwd, "missing", 0); err == nil {
		t.Error("the missing log gave no error")
	}
	path := dshLogPath(home, cwd, "broken")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a zstd frame"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDSHUsage(home, cwd, "broken", 0); err == nil {
		t.Error("the broken log gave no error")
	}
}

func TestTheDSHHomeComesFromTheEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	if got := dshHome(); got != home {
		t.Errorf("dshHome() = %q, want %q", got, home)
	}
	t.Setenv("DSH_HOME", "  ")
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := dshHome(); got != filepath.Join(userHome, ".dsh") {
		t.Errorf("dshHome() with a blank DSH_HOME = %q", got)
	}
}
