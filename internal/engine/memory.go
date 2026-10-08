package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// maxMemoryLines is the largest number of lines of a memory file.
const maxMemoryLines = 200

// memoryPath gives the path of the memory file of the repository.
func (e *Engine) memoryPath(repository string) string {
	return filepath.Join(e.config.DataDir, "memory", repository+".md")
}

// readMemory gives the text of the memory file of the repository, and the empty text when the file does not exist.
func (e *Engine) readMemory(repository string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(e.memoryPath(repository)))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

// countLines gives the number of lines of text.
func countLines(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1
}

// SaveMemory writes text to the memory file of the repository and adds a version with author and reason. It refuses a
// text of more than maxMemoryLines lines. It does nothing when text is the text of the file.
func (e *Engine) SaveMemory(ctx context.Context, repository, author, reason, text string) error {
	if lines := countLines(text); lines > maxMemoryLines {
		return fmt.Errorf("memory of %s has %d lines, the maximum is %d", repository, lines, maxMemoryLines)
	}
	current, err := e.readMemory(repository)
	if err != nil || current == text {
		return err
	}
	return e.inTx(ctx, func(queries *store.Queries) error {
		err := queries.AddMemoryVersion(ctx, store.AddMemoryVersionParams{Repository: repository, Time: now(), Author: author, Reason: reason, Text: text})
		if err != nil {
			return err
		}
		path := e.memoryPath(repository)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(text), 0o600)
	})
}
