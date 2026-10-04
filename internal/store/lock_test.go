package store

import (
	"path/filepath"
	"testing"
)

func TestLockRefusesASecondServer(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	lock, err := Lock(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Lock(dataDir)

	if want := "a Mobius server already uses the data directory " + dataDir; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %s", err, want)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	lock, err = Lock(dataDir)
	if err != nil {
		t.Fatalf("lock after the close: %v", err)
	}
	_ = lock.Close()
}
