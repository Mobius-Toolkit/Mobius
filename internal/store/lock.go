package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock creates dataDir and locks mobius.lock in it with flock, as the Rust
// version does, so that only one Mobius server uses the data directory.
// Go opens each file with O_CLOEXEC, so agent processes do not inherit the lock.
// Close the file to release the lock.
func Lock(dataDir string) (*os.File, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "mobius.lock")
	file, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		_ = file.Close()
		return nil, fmt.Errorf("a Mobius server already uses the data directory %s", dataDir)
	}
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return file, nil
}
