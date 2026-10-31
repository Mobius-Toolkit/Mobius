package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// maxMemoryLines is the largest number of lines of a memory file.
const maxMemoryLines = 200

// MemoryFileShared is the name of the memory file that each role gets. The other memory files are the roles that get the
// memory section.
const MemoryFileShared = "shared"

// memoryRoleFiles are the names of the role memory files, in the order of the prompt section of the Curator.
var memoryRoleFiles = []string{"lead", "implementer", "reviewer", "judge", "researcher", "diagnoser", "curator"}

// memoryFileNames are the names of each memory file of a repository.
var memoryFileNames = slices.Concat([]string{MemoryFileShared}, memoryRoleFiles)

// ErrMemoryTooLong is the error of a text of more than maxMemoryLines lines.
var ErrMemoryTooLong = errors.New("the memory file has too many lines")

// ErrNoMemoryVersion is the error of a version that does not exist or belongs to another repository or file.
var ErrNoMemoryVersion = errors.New("the memory file has no such version")

// ValidMemoryFile tells if name is the name of a memory file.
func ValidMemoryFile(name string) bool {
	return slices.Contains(memoryFileNames, name)
}

// MemoryFileInfo is a memory file of a repository with the number of its lines.
type MemoryFileInfo struct {
	// File is the name of the file: MemoryFileShared or the name of a role.
	File string
	// Lines is the number of lines of the file, and 0 when the file is empty or missing.
	Lines int
}

// MemoryFiles gives each memory file of the repository with the number of its lines, in the order of the prompt
// sections.
func (e *Engine) MemoryFiles(repository string) ([]MemoryFileInfo, error) {
	files := make([]MemoryFileInfo, 0, len(memoryFileNames))
	for _, file := range memoryFileNames {
		text, err := e.readMemory(repository, file)
		if err != nil {
			return nil, err
		}
		lines := 0
		if text != "" {
			lines = countLines(text)
		}
		files = append(files, MemoryFileInfo{File: file, Lines: lines})
	}
	return files, nil
}

// countLines gives the number of lines of text.
func countLines(text string) int {
	return strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1
}

// memoryPath gives the path of the memory file of the repository with the name file.
func (e *Engine) memoryPath(repository, file string) string {
	if file == MemoryFileShared {
		return filepath.Join(e.config.DataDir, "memory", repository+".md")
	}
	return filepath.Join(e.config.DataDir, "memory", repository, file+".md")
}

// readMemory gives the text of the memory file of the repository with the name file, and the empty text when the file
// does not exist.
func (e *Engine) readMemory(repository, file string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(e.memoryPath(repository, file)))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

// SaveMemory writes text to the memory file of the repository with the name file and adds a version with author and
// reason. It refuses a text of more than maxMemoryLines lines. It does nothing when text is the text of the file.
func (e *Engine) SaveMemory(ctx context.Context, repository, file, author, reason, text string) error {
	e.memoryMu.Lock()
	defer e.memoryMu.Unlock()
	return e.saveMemory(ctx, repository, file, author, reason, text)
}

// saveMemory is SaveMemory for a caller that holds memoryMu.
func (e *Engine) saveMemory(ctx context.Context, repository, file, author, reason, text string) error {
	if lines := countLines(text); lines > maxMemoryLines {
		return fmt.Errorf("%w: the memory file %s has %d lines and the maximum is %d", ErrMemoryTooLong, file, lines, maxMemoryLines)
	}
	current, err := e.readMemory(repository, file)
	if err != nil || current == text {
		return err
	}
	return e.inTx(ctx, func(queries *store.Queries) error {
		err := queries.AddMemoryVersion(ctx, store.AddMemoryVersionParams{Repository: repository, File: file, Time: now(), Author: author, Reason: reason, Text: text})
		if err != nil {
			return err
		}
		path := e.memoryPath(repository, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(text), 0o600)
	})
}

// newestMemoryVersionID gives the newest id of a version of any memory file of the repository, and 0 when it has no
// version.
func (e *Engine) newestMemoryVersionID(ctx context.Context, repository string) (int64, error) {
	var newest int64
	for _, file := range memoryFileNames {
		id, err := e.queries.GetNewestMemoryVersionID(ctx, store.GetNewestMemoryVersionIDParams{Repository: repository, File: file})
		if err != nil {
			return 0, err
		}
		newest = max(newest, id)
	}
	return newest, nil
}

// EditMemory saves text like SaveMemory with the author owner, for an edit that started from the version base (0 when
// the file had no version). It refuses the edit when the newest version of the repository and file is another version.
func (e *Engine) EditMemory(ctx context.Context, repository, file string, base int64, text string) error {
	e.memoryMu.Lock()
	defer e.memoryMu.Unlock()
	newest, err := e.queries.GetNewestMemoryVersionID(ctx, store.GetNewestMemoryVersionIDParams{Repository: repository, File: file})
	if err != nil {
		return err
	}
	if newest != base {
		return refuse("The memory file changed after the start of your edit. Copy your text, then load the file again.")
	}
	return e.saveMemory(ctx, repository, file, "owner", "", text)
}

// splitLines gives the lines of text with their line ends.
func splitLines(text string) []string {
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}
	return lines
}

const revertConflict = "A later version changed this part. Edit the file."

// revertedMemory gives the text that results when the change of the version id is undone in the current text of the
// repository and file. The change of a version is the block of lines between the common first lines and the common last
// lines of the text before the version and the text of the version. The block, with the line before it and the line
// after it, must occur one time in the current text. The start and the end of the file give no line. It returns
// ErrNoMemoryVersion for an unknown version and for a version of another repository or file, and a refusal when the
// change cannot be undone.
func (e *Engine) revertedMemory(ctx context.Context, repository, file string, id int64) (string, error) {
	version, err := e.queries.GetMemoryVersion(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err != nil || version.Repository != repository || version.File != file {
		return "", ErrNoMemoryVersion
	}
	before, err := e.queries.GetMemoryVersionBefore(ctx, store.GetMemoryVersionBeforeParams{Repository: repository, File: file, ID: id})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	current, err := e.readMemory(repository, file)
	if err != nil {
		return "", err
	}

	old, changed, lines := splitLines(before), splitLines(version.Text), splitLines(current)
	first := 0
	for first < len(old) && first < len(changed) && old[first] == changed[first] {
		first++
	}
	last := 0
	for last < len(old)-first && last < len(changed)-first && old[len(old)-1-last] == changed[len(changed)-1-last] {
		last++
	}
	removed, added := old[first:len(old)-last], changed[first:len(changed)-last]
	if len(removed) == 0 && len(added) == 0 {
		return "", refuse("The revert changes nothing.")
	}

	var above, below []string
	if first > 0 {
		above = changed[first-1 : first]
	}
	if last > 0 {
		below = changed[len(changed)-last : len(changed)-last+1]
	}
	block := slices.Concat(above, added, below)
	place := -1
	for i := 0; i+len(block) <= len(lines); i++ {
		if slices.Equal(lines[i:i+len(block)], block) {
			if place >= 0 {
				return "", refuse(revertConflict)
			}
			place = i
		}
	}
	if place < 0 {
		return "", refuse(revertConflict)
	}
	from := place + len(above)
	reverted := strings.Join(slices.Concat(lines[:from], removed, lines[from+len(added):]), "")
	if n := countLines(reverted); n > maxMemoryLines {
		return "", refuse("The revert gives %d lines and the maximum is %d. Edit the file.", n, maxMemoryLines)
	}
	return reverted, nil
}

// MemoryRevertProblem gives the reason why the version id of the repository and file cannot be reverted, or "" when it
// can.
func (e *Engine) MemoryRevertProblem(ctx context.Context, repository, file string, id int64) (string, error) {
	_, err := e.revertedMemory(ctx, repository, file, id)
	if Refused(err) {
		return err.Error(), nil
	}
	return "", err
}

// RevertMemory undoes the change of the version id of the repository and file in the current text, and saves the result
// as a new version of the author owner. It returns ErrNoMemoryVersion for an unknown version and for a version of
// another repository or file, and a refusal when the change cannot be undone.
func (e *Engine) RevertMemory(ctx context.Context, repository, file string, id int64) error {
	e.memoryMu.Lock()
	defer e.memoryMu.Unlock()
	text, err := e.revertedMemory(ctx, repository, file, id)
	if err != nil {
		return err
	}
	return e.saveMemory(ctx, repository, file, "owner", fmt.Sprintf("Revert of version %d", id), text)
}
