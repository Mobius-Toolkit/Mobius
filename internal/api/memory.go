package api

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/github"
)

// MemoryVersion is a version of the memory file of a repository.
type MemoryVersion struct {
	// ID is the id of the version
	ID int64 `gork:"id"`
	// Time is the time when Mobius saved the version, in RFC 3339 format
	Time string `gork:"time"`
	// Author is curator or owner
	Author string `gork:"author" validate:"oneof=curator owner"`
	// Text is the text of the memory file in this version
	Text string `gork:"text"`
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
	}
}

// ListMemoryVersionsResponse is the response of ListMemoryVersions.
type ListMemoryVersionsResponse struct {
	Body Envelope[[]MemoryVersion]
}

// ListMemoryVersions returns the versions of the memory file of the repository, the newest first, each with the result
// of the check of its revert. The first version is the current text of the file. It returns 404 when the repository is not a repository of Mobius.
func (h *handlers) ListMemoryVersions(ctx context.Context, req ListMemoryVersionsRequest) (*ListMemoryVersionsResponse, error) {
	repository := req.Path.Owner + "/" + req.Path.Name
	if err := h.knownRepository(repository); err != nil {
		return nil, err
	}
	rows, err := h.queries.ListMemoryVersions(ctx, repository)
	if err != nil {
		return nil, err
	}
	versions := make([]MemoryVersion, 0, len(rows))
	for _, row := range rows {
		problem, err := h.engine.MemoryRevertProblem(ctx, repository, row.ID)
		if err != nil {
			return nil, err
		}
		versions = append(versions, MemoryVersion{ID: row.ID, Time: row.Time, Author: row.Author, Text: row.Text, Revertible: problem == "", RevertProblem: problem})
	}
	return &ListMemoryVersionsResponse{Body: Envelope[[]MemoryVersion]{Data: versions}}, nil
}

// SaveMemoryRequest is the request of SaveMemory.
type SaveMemoryRequest struct {
	Path struct {
		// Owner is the owner of the repository
		Owner string `gork:"owner"`
		// Name is the name of the repository
		Name string `gork:"name"`
	}
	Body struct {
		// Text is the new text of the memory file
		Text string `gork:"text"`
		// BaseVersion is the id of the newest version when the edit started, and 0 when the file had no version
		BaseVersion int64 `gork:"base_version"`
	}
}

// SaveMemory saves the text as the memory file of the repository and adds a version with the author owner. It does
// nothing when the text does not change. It returns 404 when the repository is not a repository of Mobius, 409 when a
// newer version exists than the base version, and 422 when the text has more than 200 lines.
func (h *handlers) SaveMemory(ctx context.Context, req SaveMemoryRequest) error {
	repository := req.Path.Owner + "/" + req.Path.Name
	if err := h.knownRepository(repository); err != nil {
		return err
	}
	err := h.engine.EditMemory(ctx, repository, req.Body.BaseVersion, req.Body.Text)
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
		// ID is the id of the version to revert
		ID int64 `gork:"id"`
	}
}

// RevertMemory undoes the change of the version in the current text of the memory file and saves the result as a new
// version with the author owner. It returns 404 when the repository is not a repository of Mobius, or when the version
// does not exist or belongs to another repository. It returns 409 when a later version changed the part, when the
// revert changes nothing, or when the result has more than 200 lines.
func (h *handlers) RevertMemory(ctx context.Context, req RevertMemoryRequest) error {
	repository := req.Path.Owner + "/" + req.Path.Name
	if err := h.knownRepository(repository); err != nil {
		return err
	}
	err := h.engine.RevertMemory(ctx, repository, req.Path.ID)
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
