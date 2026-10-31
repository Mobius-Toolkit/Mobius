package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"slices"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

const memoryPageSize = 20

// MemoryFile is a memory file of a repository with the number of its lines.
type MemoryFile struct {
	// File is the name of the memory file: shared, or the name of a role
	File string `gork:"file" validate:"oneof=shared lead implementer reviewer judge researcher diagnoser curator"`
	// Lines is the number of lines of the file, and 0 when the file is empty or missing
	Lines int `gork:"lines"`
}

// ListMemoryFilesRequest is the request of ListMemoryFiles.
type ListMemoryFilesRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
	}
}

// ListMemoryFilesResponse is the response of ListMemoryFiles.
type ListMemoryFilesResponse struct {
	Body Envelope[[]MemoryFile]
}

// MemoryVersion is a version of the memory file of a repository.
type MemoryVersion struct {
	// ID is the id of the version
	ID int64 `gork:"id"`
	// Time is the time when Mobius saved the version, in RFC 3339 format
	Time string `gork:"time"`
	// Author is curator or owner
	Author string `gork:"author" validate:"oneof=curator owner"`
	// Reason is the type of change and the evidence of the Curator, "Revert of version N" for a revert, and empty for an edit of the Owner
	Reason string `gork:"reason"`
	// Text is the text of the memory file in this version
	Text string `gork:"text"`
	// Before is the text of the previous version, and empty for the oldest version of the repository
	Before string `gork:"before"`
	// Revertible tells if the revert of the version is possible
	Revertible bool `gork:"revertible"`
	// RevertProblem is the reason why the revert is not possible, and empty when it is possible
	RevertProblem string `gork:"revert_problem"`
}

// ListMemoryVersionsRequest is the request of ListMemoryVersions.
type ListMemoryVersionsRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
		// File is the name of the memory file: shared, or the name of a role
		File string `gork:"file"`
	}
	Query struct {
		// Before is the id of a version. The page has at most 20 versions with an id below Before. With no Before,
		// the page has the newest 20 versions
		Before int64 `gork:"before"`
	}
}

// MemoryVersionsPage is a page of the versions of the memory file of a repository.
type MemoryVersionsPage struct {
	// Versions are the versions of the page, the newest first
	Versions []MemoryVersion `gork:"versions"`
	// Older is true when the repository has versions before the last version of the page
	Older bool `gork:"older"`
}

// ListMemoryVersionsResponse is the response of ListMemoryVersions.
type ListMemoryVersionsResponse struct {
	Body Envelope[MemoryVersionsPage]
}

// ListMemoryFiles returns each memory file of the repository in the order of the engine list, with the number of its
// lines. It returns 404 when the repository is not a repository of Mobius.
func (h *handlers) ListMemoryFiles(_ context.Context, req ListMemoryFilesRequest) (*ListMemoryFilesResponse, error) {
	repository := req.Path.Owner + "/" + req.Path.Name
	if err := h.knownRepository(repository); err != nil {
		return nil, err
	}
	infos, err := h.engine.MemoryFiles(repository)
	if err != nil {
		return nil, err
	}
	files := make([]MemoryFile, 0, len(infos))
	for _, info := range infos {
		files = append(files, MemoryFile{File: info.File, Lines: info.Lines})
	}
	return &ListMemoryFilesResponse{Body: Envelope[[]MemoryFile]{Data: files}}, nil
}

// ListMemoryVersions returns a page of the versions of the memory file of the repository, the newest first, each with the
// result of the check of its revert. The first version of the first page is the current text of the file. It returns 404
// when the repository is not a repository of Mobius, or when it has no file with that name.
func (h *handlers) ListMemoryVersions(ctx context.Context, req ListMemoryVersionsRequest) (*ListMemoryVersionsResponse, error) {
	repository := req.Path.Owner + "/" + req.Path.Name
	if err := h.knownRepository(repository); err != nil {
		return nil, err
	}
	file := req.Path.File
	if err := knownMemoryFile(file); err != nil {
		return nil, err
	}
	before := req.Query.Before
	if before == 0 {
		before = math.MaxInt64
	}
	// One more row than the page tells if older versions exist, and gives the text before the last version.
	rows, err := h.queries.ListMemoryVersionsPage(ctx, store.ListMemoryVersionsPageParams{Repository: repository, File: file, ID: before, Limit: memoryPageSize + 1})
	if err != nil {
		return nil, err
	}
	page := MemoryVersionsPage{Older: len(rows) > memoryPageSize, Versions: []MemoryVersion{}}
	for i, row := range rows[:min(len(rows), memoryPageSize)] {
		problem, err := h.engine.MemoryRevertProblem(ctx, repository, file, row.ID)
		if err != nil {
			return nil, err
		}
		version := MemoryVersion{ID: row.ID, Time: row.Time, Author: row.Author, Reason: row.Reason, Text: row.Text, Revertible: problem == "", RevertProblem: problem}
		if i+1 < len(rows) {
			version.Before = rows[i+1].Text
		}
		page.Versions = append(page.Versions, version)
	}
	return &ListMemoryVersionsResponse{Body: Envelope[MemoryVersionsPage]{Data: page}}, nil
}

// SaveMemoryRequest is the request of SaveMemory.
type SaveMemoryRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
		// File is the name of the memory file: shared, or the name of a role
		File string `gork:"file"`
	}
	Body struct {
		// Text is the new text of the memory file
		Text string `gork:"text"`
		// BaseVersion is the id of the newest version when the edit started, and 0 when the file had no version
		BaseVersion int64 `gork:"base_version"`
	}
}

// SaveMemory saves the text as the memory file of the repository and adds a version with the author owner. It does
// nothing when the text does not change. It returns 404 when the repository is not a repository of Mobius, or when it
// has no file with that name, 409 when a newer version exists than the base version, and 422 when the text has more than
// 200 lines.
func (h *handlers) SaveMemory(ctx context.Context, req SaveMemoryRequest) error {
	repository := req.Path.Owner + "/" + req.Path.Name
	if err := h.knownRepository(repository); err != nil {
		return err
	}
	file := req.Path.File
	if err := knownMemoryFile(file); err != nil {
		return err
	}
	err := h.engine.EditMemory(ctx, repository, file, req.Body.BaseVersion, req.Body.Text)
	if errors.Is(err, engine.ErrMemoryTooLong) {
		return api.NewHTTPError(http.StatusUnprocessableEntity, err.Error())
	}
	if engine.Refused(err) {
		return api.NewHTTPError(http.StatusConflict, err.Error())
	}
	return err
}

// RevertMemoryRequest is the request of RevertMemory.
type RevertMemoryRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
		// File is the name of the memory file: shared, or the name of a role
		File string `gork:"file"`
		// ID is the id of the version to revert
		ID int64 `gork:"id"`
	}
}

// RevertMemory undoes the change of the version in the current text of the memory file and saves the result as a new
// version with the author owner. It returns 404 when the repository is not a repository of Mobius, when it has no file
// with that name, or when the version does not exist or belongs to another repository or file. It returns 409 when a
// later version changed the part, when the revert changes nothing, or when the result has more than 200 lines.
func (h *handlers) RevertMemory(ctx context.Context, req RevertMemoryRequest) error {
	repository := req.Path.Owner + "/" + req.Path.Name
	if err := h.knownRepository(repository); err != nil {
		return err
	}
	file := req.Path.File
	if err := knownMemoryFile(file); err != nil {
		return err
	}
	err := h.engine.RevertMemory(ctx, repository, file, req.Path.ID)
	if errors.Is(err, engine.ErrNoMemoryVersion) {
		return api.NewHTTPError(http.StatusNotFound, "The memory file has no such version.")
	}
	if engine.Refused(err) {
		return api.NewHTTPError(http.StatusConflict, err.Error())
	}
	return err
}

// knownRepository gives a 404 error when Mobius has no repository with the full name.
func (h *handlers) knownRepository(fullName string) error {
	if !slices.ContainsFunc(h.github.Repositories(), func(r github.Repository) bool { return r.FullName == fullName }) {
		return api.NewHTTPError(http.StatusNotFound, "Mobius has no repository "+fullName+".")
	}
	return nil
}

// knownMemoryFile gives a 404 error when the repository has no memory file with the name.
func knownMemoryFile(file string) error {
	if !engine.ValidMemoryFile(file) {
		return api.NewHTTPError(http.StatusNotFound, "The memory has no file "+file+".")
	}
	return nil
}
