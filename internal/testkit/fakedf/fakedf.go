// Package fakedf is the program df of the tests: it gives the free space of a file as the output of df -Pk.
package fakedf

import (
	"fmt"
	"os"
	"path/filepath"
)

// Run writes the output of df -Pk with the free space in KiB that the file at path holds.
func Run(path string) error {
	free, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	_, err = fmt.Printf("Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/disk1 100 100 %s 100%% /\n", free)
	return err
}
