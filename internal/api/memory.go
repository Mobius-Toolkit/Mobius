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

// ListMemoryVersions returns the versions of the memory file of the repository, the newest first. The first version is
// the current text of the file. It returns 404 when the repository is not a repository of Mobius.
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
		versions = append(versions, MemoryVersion{ID: row.ID, Time: row.Time, Author: row.Author, Text: row.Text})
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
	}
}

// SaveMemory saves the text as the memory file of the repository and adds a version with the author owner. It does
// nothing when the text does not change. It returns 404 when the repository is not a repository of Mobius, and 422
// when the text has more than 200 lines.
func (h *handlers) SaveMemory(ctx context.Context, req SaveMemoryRequest) error {
	repository := req.Path.Owner + "/" + req.Path.Name
	if err := h.knownRepository(repository); err != nil {
		return err
	}
	err := h.engine.SaveMemory(ctx, repository, "owner", req.Body.Text)
	if errors.Is(err, engine.ErrMemoryTooLong) {
		return api.NewHTTPError(http.StatusUnprocessableEntity, err.Error())
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

// RevertMemory saves the text of the version before the version as a new version with the author owner. The text before
// the first version is empty. It returns 404 when the repository is not a repository of Mobius, or when the version
// does not exist or belongs to another repository.
func (h *handlers) RevertMemory(ctx context.Context, req RevertMemoryRequest) error {
	repository := req.Path.Owner + "/" + req.Path.Name
	if err := h.knownRepository(repository); err != nil {
		return err
	}
	err := h.engine.RevertMemory(ctx, repository, req.Path.ID)
	if engine.Refused(err) {
		return api.NewHTTPError(http.StatusNotFound, err.Error())
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
