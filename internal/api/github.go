package api

import (
	"context"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gork-labs/gork/pkg/api"
)

// GitHub App names are unique on all of GitHub, and GitHub allows a maximum of 34 characters.
const maxAppName = 34

// ListGitHubAppsRequest is the request of ListGitHubApps.
type ListGitHubAppsRequest struct{}

// GitHubApp is a Mobius App on GitHub.
type GitHubApp struct {
	// Slug is the name of the App in its GitHub URLs
	Slug string `gork:"slug"`
	// InstallURL is the page of GitHub that installs the App on repositories
	InstallURL string `gork:"installUrl"`
	// Repositories are the repositories of the installations of the App, as "owner/name"
	Repositories []string `gork:"repositories"`
}

// ListGitHubAppsResponse is the response of ListGitHubApps.
type ListGitHubAppsResponse struct {
	Body Envelope[[]GitHubApp]
}

// ListGitHubApps returns the Mobius Apps with the repositories of their installations.
func (h *handlers) ListGitHubApps(ctx context.Context, _ ListGitHubAppsRequest) (*ListGitHubAppsResponse, error) {
	rows, err := h.queries.ListGitHubApps(ctx)
	if err != nil {
		return nil, err
	}
	repositories := h.github.Repositories()
	apps := make([]GitHubApp, 0, len(rows))
	for _, row := range rows {
		app := GitHubApp{Slug: row.Slug, InstallURL: h.github.InstallURL(row.Slug), Repositories: []string{}}
		for _, repository := range repositories {
			if repository.AppID == row.AppID {
				app.Repositories = append(app.Repositories, repository.FullName)
			}
		}
		apps = append(apps, app)
	}
	return &ListGitHubAppsResponse{Body: Envelope[[]GitHubApp]{Data: apps}}, nil
}

// CreateManifestFormRequest is the request of CreateManifestForm.
type CreateManifestFormRequest struct {
	Body struct {
		// Account is the GitHub user or organization that owns the new App
		Account string `gork:"account" validate:"required"`
		// Name is the name of the new App
		Name string `gork:"name"`
		// Origin is the origin of the Mobius UI, for example https://mobius.example.ts.net
		Origin string `gork:"origin" validate:"required"`
	}
}

// ManifestForm is the form that the browser posts to GitHub to create a Mobius App.
type ManifestForm struct {
	// URL is the action of the form
	URL string `gork:"url"`
	// Manifest is the value of the form field "manifest"
	Manifest string `gork:"manifest"`
}

// CreateManifestFormResponse is the response of CreateManifestForm.
type CreateManifestFormResponse struct {
	Body Envelope[ManifestForm]
}

// CreateManifestForm returns the form that creates a Mobius App in a GitHub account.
// After the form, GitHub sends the browser to GET /api/github/manifest-callback.
func (h *handlers) CreateManifestForm(ctx context.Context, req CreateManifestFormRequest) (*CreateManifestFormResponse, error) {
	name := strings.TrimSpace(req.Body.Name)
	if name == "" || utf8.RuneCountInString(name) > maxAppName {
		return nil, api.NewHTTPError(http.StatusBadRequest, "The App name must have 1 to 34 characters.")
	}
	form, found, err := h.github.ManifestForm(ctx, req.Body.Account, name, req.Body.Origin)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, api.NewHTTPError(http.StatusUnprocessableEntity, "GitHub has no account "+req.Body.Account+".")
	}
	return &CreateManifestFormResponse{Body: Envelope[ManifestForm]{Data: ManifestForm{URL: form.URL, Manifest: form.Manifest}}}, nil
}

// ListOrganizationsRequest is the request of ListOrganizations.
type ListOrganizationsRequest struct{}

// ListOrganizationsResponse is the response of ListOrganizations.
type ListOrganizationsResponse struct {
	Body Envelope[[]string]
}

// ListOrganizations returns the owners of the repositories of the Mobius Apps, sorted.
func (h *handlers) ListOrganizations(_ context.Context, _ ListOrganizationsRequest) (*ListOrganizationsResponse, error) {
	return &ListOrganizationsResponse{Body: Envelope[[]string]{Data: h.github.Organizations()}}, nil
}

// ManifestCallbackRequest is the request of ManifestCallback.
type ManifestCallbackRequest struct {
	Query struct {
		// Code is the code that converts the manifest into an App
		Code string `gork:"code" validate:"required"`
		// State is the state of the manifest form
		State string `gork:"state" validate:"required"`
	}
}

// ManifestCallbackResponse is the response of ManifestCallback.
type ManifestCallbackResponse struct {
	Headers struct {
		// Location is the page that the browser opens next
		Location string `gork:"Location"`
	}
}

// ManifestCallback is the page where GitHub sends the browser after it creates the App of
// a manifest form.
func (h *handlers) ManifestCallback(ctx context.Context, req ManifestCallbackRequest) (*ManifestCallbackResponse, error) {
	ok, err := h.github.ConvertManifest(ctx, req.Query.Code, req.Query.State)
	if err != nil {
		log.Printf("convert the App manifest: %v", err)
		return nil, err
	}
	if !ok {
		return nil, api.NewHTTPError(http.StatusForbidden, "The App setup is not valid or is older than one hour. Start the setup again on the GitHub page of Mobius.")
	}
	resp := &ManifestCallbackResponse{}
	resp.Headers.Location = "/github"
	return resp, nil
}

// UserCallbackRequest is the request of UserCallback.
type UserCallbackRequest struct {
	Query struct {
		// Code is the code that GitHub exchanges for a user token
		Code string `gork:"code" validate:"required"`
	}
}

// UserCallbackResponse is the response of UserCallback.
type UserCallbackResponse struct {
	Headers struct {
		// Location is the page that the browser opens next
		Location string `gork:"Location"`
	}
}

// UserCallback is the page where GitHub sends the browser after the Owner authorizes a Mobius App.
// GitHub sends no state after an installation, so the check of the user login protects this page.
func (h *handlers) UserCallback(ctx context.Context, req UserCallbackRequest) (*UserCallbackResponse, error) {
	ok, err := h.github.AuthorizeUser(ctx, req.Query.Code)
	if err != nil {
		log.Printf("authorize the GitHub user: %v", err)
		return nil, err
	}
	if !ok {
		return nil, api.NewHTTPError(http.StatusForbidden, "The GitHub login is not a trusted user.")
	}
	resp := &UserCallbackResponse{}
	resp.Headers.Location = "/github"
	return resp, nil
}
