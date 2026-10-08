package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// maxMemoryLines is the largest number of lines of a memory file.
const maxMemoryLines = 200

// ErrMemoryTooLong is the error of a text of more than maxMemoryLines lines.
var ErrMemoryTooLong = errors.New("the memory file has too many lines")

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

// SaveMemory writes text to the memory file of the repository and adds a version with author. It refuses a text of
// more than maxMemoryLines lines. It does nothing when text is the text of the file.
func (e *Engine) SaveMemory(ctx context.Context, repository, author, text string) error {
	if lines := strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1; lines > maxMemoryLines {
		return fmt.Errorf("%w: the text has %d lines and the maximum is %d", ErrMemoryTooLong, lines, maxMemoryLines)
	}
	current, err := e.readMemory(repository)
	if err != nil || current == text {
		return err
	}
	return e.inTx(ctx, func(queries *store.Queries) error {
		err := queries.AddMemoryVersion(ctx, store.AddMemoryVersionParams{Repository: repository, Time: now(), Author: author, Text: text})
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

// RevertMemory saves the text of the version before the version id of the repository as a new version of the author
// owner. The version before the first version is the empty text. It refuses an unknown version and a version of
// another repository.
func (e *Engine) RevertMemory(ctx context.Context, repository string, id int64) error {
	version, err := e.queries.GetMemoryVersion(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err != nil || version.Repository != repository {
		return refuse("The memory of %s has no version %d.", repository, id)
	}
	before, err := e.queries.GetMemoryVersionBefore(ctx, store.GetMemoryVersionBeforeParams{Repository: repository, ID: id})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return e.SaveMemory(ctx, repository, "owner", before)
}
