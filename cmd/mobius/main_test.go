package main

import (
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestLogLineGoesToFile(t *testing.T) {
	dataDir := t.TempDir()
	file := newLogFile(dataDir)
	t.Cleanup(func() { _ = file.Close() })

	log.New(file, "", 0).Print("poll failed")

	got, err := os.ReadFile(filepath.Join(dataDir, "logs", "mobius.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "poll failed\n" {
		t.Fatalf("log file = %q", got)
	}
}

func TestLogRotationKeepsFixedCountOfOldFiles(t *testing.T) {
	dataDir := t.TempDir()
	file := newLogFile(dataDir)
	t.Cleanup(func() { _ = file.Close() })
	logger := log.New(file, "", 0)

	for i := 0; i < logMaxBackups+6; i++ {
		logger.Print("line")
		if err := file.Rotate(); err != nil {
			t.Fatal(err)
		}
		// Backup names have a resolution of one millisecond.
		last := time.Now().UnixMilli()
		testkit.WaitFor(t, func() bool { return time.Now().UnixMilli() > last })
	}
	logger.Print("line")

	logs := filepath.Join(dataDir, "logs")
	testkit.WaitFor(t, func() bool {
		entries, err := os.ReadDir(logs)
		return err == nil && len(entries) == logMaxBackups+1
	})
	if _, err := os.Stat(filepath.Join(logs, "mobius.log")); err != nil {
		t.Fatal(err)
	}
}
