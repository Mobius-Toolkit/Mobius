// Package github holds the GitHub Apps of Mobius: the App setup, the user tokens of the Owner,
// the installation tokens, and the repositories of the installations.
package github

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/mobius-go/internal/store"
)

// The Owner must create the App within this time after the form opens the page of GitHub.
const stateLife = time.Hour

// A user token that expires within this time gets a refresh before its use.
const refreshMargin = 5 * time.Minute

// Permission is a permission name of a GitHub App with its level: read, write or admin.
type Permission struct {
	Name  string
	Level string
}

// RequiredPermissions are the permissions that the Mobius App needs.
var RequiredPermissions = []Permission{
	{"issues", "write"},
	{"pull_requests", "write"},
	{"contents", "write"},
	{"checks", "write"},
	{"workflows", "write"},
	{"actions", "read"},
	{"metadata", "read"},
}

// GitHub holds the Apps of the github_apps table and the repositories of their installations.
type GitHub struct {
	queries      *store.Queries
	apiURL       string
	webURL       string
	trustedUsers []string
	// api has no token. It makes only the calls that GitHub permits with no token.
	api *gh.Client
	// GitHub accepts a refresh token one time, so two refreshes must not run at the same time.
	refresh sync.Mutex

	mu           sync.Mutex
	states       map[string]time.Time
	repositories []Repository
	// The transports of the installations, by installation id. Each transport keeps its token until the token expires.
	transports map[int64]*ghinstallation.Transport
}

// Repository is a repository of an installation of a Mobius App.
type Repository struct {
	// FullName is "owner/name".
	FullName string
	AppID    int64
	AppSlug  string
	// Client uses the installation token, and gets a new token before the token expires.
	Client *gh.Client
}

// New gives the GitHub of apiURL (for example https://api.github.com) and webURL (for example https://github.com).
func New(queries *store.Queries, apiURL, webURL string, trustedUsers []string) (*GitHub, error) {
	g := &GitHub{
		queries:      queries,
		apiURL:       apiURL,
		webURL:       webURL,
		trustedUsers: trustedUsers,
		states:       map[string]time.Time{},
		transports:   map[int64]*ghinstallation.Transport{},
	}
	var err error
	g.api, err = g.client()
	return g, err
}

func (g *GitHub) client(options ...gh.ClientOptionsFunc) (*gh.Client, error) {
	base := g.apiURL + "/"
	return gh.NewClient(append(options, gh.WithURLs(&base, &base))...)
}

// ManifestForm is the form that creates a Mobius App on GitHub.
type ManifestForm struct {
	// URL is the page of GitHub that gets the form. It has the state of the setup.
	URL string
	// Manifest is the JSON text of the App manifest.
	Manifest string
}

// ManifestForm gives the form that creates the App name in the GitHub account, with
// the callbacks of the Mobius server at origin. It gives false when the account does not exist.
//
// The call has no token: before the first App, Mobius has no token.
func (g *GitHub) ManifestForm(ctx context.Context, account, name, origin string) (ManifestForm, bool, error) {
	found, _, err := g.api.Users.Get(ctx, url.PathEscape(account))
	var response *gh.ErrorResponse
	if errors.As(err, &response) && response.Response.StatusCode == http.StatusNotFound {
		return ManifestForm{}, false, nil
	}
	if err != nil {
		return ManifestForm{}, false, err
	}
	permissions := map[string]string{}
	for _, permission := range RequiredPermissions {
		permissions[permission.Name] = permission.Level
	}
	manifest, err := json.Marshal(map[string]any{
		"name":                     name,
		"url":                      "https://github.com/Mobius-Toolkit/Mobius",
		"redirect_url":             origin + "/api/github/manifest-callback",
		"callback_urls":            []string{origin + "/api/github/user-callback"},
		"request_oauth_on_install": true,
		"public":                   false,
		"default_permissions":      permissions,
	})
	if err != nil {
		return ManifestForm{}, false, err
	}
	page := g.webURL + "/settings/apps/new"
	if found.GetType() == "Organization" {
		page = g.webURL + "/organizations/" + url.PathEscape(account) + "/settings/apps/new"
	}
	return ManifestForm{URL: page + "?state=" + g.newState(), Manifest: string(manifest)}, true, nil
}

func (g *GitHub) newState() string {
	secret := make([]byte, 32)
	// rand.Read never returns an error.
	_, _ = rand.Read(secret)
	state := hex.EncodeToString(secret)
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	for old, expires := range g.states {
		if now.After(expires) {
			delete(g.states, old)
		}
	}
	g.states[state] = now.Add(stateLife)
	return state
}

func (g *GitHub) takeState(state string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	expires, ok := g.states[state]
	delete(g.states, state)
	return ok && time.Now().Before(expires)
}

// ConvertManifest gets the new App of the manifest code from GitHub and stores it.
// It gives false when state is not the state of a form from ManifestForm, or when the
// form is older than one hour. Each state is valid for one conversion.
//
// The call has no token: the code is the credential.
func (g *GitHub) ConvertManifest(ctx context.Context, code, state string) (bool, error) {
	if !g.takeState(state) {
		return false, nil
	}
	app, _, err := g.api.Apps.CompleteAppManifest(ctx, url.PathEscape(code))
	if err != nil {
		return false, err
	}
	return true, g.queries.AddGitHubApp(ctx, store.AddGitHubAppParams{
		AppID:        app.GetID(),
		Slug:         app.GetSlug(),
		PrivateKey:   app.GetPEM(),
		ClientID:     app.GetClientID(),
		ClientSecret: app.GetClientSecret(),
	})
}

// InstallURL gives the page of GitHub that installs the App slug.
func (g *GitHub) InstallURL(slug string) string {
	return g.webURL + "/apps/" + url.PathEscape(slug) + "/installations/new"
}

// LatestRelease gives the latest release of Mobius.
//
// The call has no token: the releases of Mobius are public, so a server with no App can also upgrade.
func (g *GitHub) LatestRelease(ctx context.Context) (*gh.RepositoryRelease, error) {
	release, _, err := g.api.Repositories.GetLatestRelease(ctx, "Mobius-Toolkit", "Mobius")
	return release, err
}

// ReleaseURL gives the download URL of the file asset of the release of Mobius with tag.
func (g *GitHub) ReleaseURL(tag, asset string) string {
	return g.webURL + "/Mobius-Toolkit/Mobius/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(asset)
}

type userTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	// ExpiresIn is in seconds.
	ExpiresIn        int64  `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// exchange gets user tokens for a user code or a refresh token. The client id and secret
// of the App are the credentials. GitHub refuses with status 200 and an `error` field.
func (g *GitHub) exchange(ctx context.Context, body map[string]string) (userTokens, error) {
	text, err := json.Marshal(body)
	if err != nil {
		return userTokens{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.webURL+"/login/oauth/access_token", bytes.NewReader(text))
	if err != nil {
		return userTokens{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return userTokens{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return userTokens{}, fmt.Errorf("token exchange: status %d", response.StatusCode)
	}
	var tokens userTokens
	if err := json.NewDecoder(response.Body).Decode(&tokens); err != nil {
		return userTokens{}, err
	}
	if tokens.Error != "" {
		return userTokens{}, errors.New(tokens.ErrorDescription)
	}
	return tokens, nil
}

func (g *GitHub) storeUserTokens(ctx context.Context, appID int64, tokens userTokens) error {
	expires := time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	return g.queries.SetUserTokens(ctx, store.SetUserTokensParams{
		UserToken:          sql.NullString{String: tokens.AccessToken, Valid: true},
		RefreshToken:       sql.NullString{String: tokens.RefreshToken, Valid: true},
		UserTokenExpiresAt: sql.NullString{String: expires.UTC().Format(time.RFC3339Nano), Valid: true},
		AppID:              appID,
	})
}

// AuthorizeUser gets the user tokens of the OAuth code and stores them on the App of the code.
// It gives false, and stores nothing, when the user of the code is not a trusted user.
//
// GitHub sends the Owner to the same callback URL for each App, so AuthorizeUser tries the code with each App.
func (g *GitHub) AuthorizeUser(ctx context.Context, code string) (bool, error) {
	apps, err := g.queries.ListGitHubApps(ctx)
	if err != nil {
		return false, err
	}
	refused := errors.New("no Mobius App exists")
	for _, app := range apps {
		tokens, err := g.exchange(ctx, map[string]string{
			"client_id":     app.ClientID,
			"client_secret": app.ClientSecret,
			"code":          code,
		})
		if err != nil {
			refused = err
			continue
		}
		user, err := g.client(gh.WithAuthToken(tokens.AccessToken))
		if err != nil {
			return false, err
		}
		found, _, err := user.Users.Get(ctx, "")
		if err != nil {
			return false, err
		}
		if !slices.ContainsFunc(g.trustedUsers, func(trusted string) bool { return strings.EqualFold(trusted, found.GetLogin()) }) {
			return false, nil
		}
		return true, g.storeUserTokens(ctx, app.AppID, tokens)
	}
	return false, refused
}

// UserToken gives the user token of the Owner for the App appID. When the token expires
// within five minutes, UserToken first gets new tokens with the refresh token and stores them.
func (g *GitHub) UserToken(ctx context.Context, appID int64) (string, error) {
	g.refresh.Lock()
	defer g.refresh.Unlock()
	app, err := g.queries.GetGitHubApp(ctx, appID)
	if err != nil {
		return "", err
	}
	if app.UserToken.Valid && app.UserTokenExpiresAt.Valid {
		expires, err := time.Parse(time.RFC3339Nano, app.UserTokenExpiresAt.String)
		if err != nil {
			return "", err
		}
		if time.Until(expires) > refreshMargin {
			return app.UserToken.String, nil
		}
	}
	authorize := g.webURL + "/login/oauth/authorize?client_id=" + url.QueryEscape(app.ClientID)
	if !app.RefreshToken.Valid {
		return "", fmt.Errorf("the Owner did not authorize the Mobius App %s: tell the Owner to open %s and authorize the App", app.Slug, authorize)
	}
	tokens, err := g.exchange(ctx, map[string]string{
		"client_id":     app.ClientID,
		"client_secret": app.ClientSecret,
		"grant_type":    "refresh_token",
		"refresh_token": app.RefreshToken.String,
	})
	if err != nil {
		return "", fmt.Errorf("refresh the user token of the Mobius App %s: %w: tell the Owner to open %s and authorize the App", app.Slug, err, authorize)
	}
	if err := g.storeUserTokens(ctx, appID, tokens); err != nil {
		return "", err
	}
	return tokens.AccessToken, nil
}

// Refresh reads the repositories of the installations of each App. An App whose read
// fails keeps the repositories of its last read, and then Refresh gives false.
func (g *GitHub) Refresh(ctx context.Context) (bool, error) {
	apps, err := g.queries.ListGitHubApps(ctx)
	if err != nil {
		return false, err
	}
	var repositories []Repository
	complete := true
	for _, app := range apps {
		found, err := g.appRepositories(ctx, app)
		if err != nil {
			log.Printf("read the repositories of the GitHub App %s: %v", app.Slug, err)
			found = slices.DeleteFunc(g.Repositories(), func(r Repository) bool { return r.AppID != app.AppID })
			complete = false
		}
		repositories = append(repositories, found...)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.repositories = repositories
	return complete, nil
}

// appClient gives the client that signs each request with the key of the App.
func (g *GitHub) appClient(app store.GithubApp) (*gh.Client, *ghinstallation.AppsTransport, error) {
	appTransport, err := ghinstallation.NewAppsTransport(http.DefaultTransport, app.AppID, []byte(app.PrivateKey))
	if err != nil {
		return nil, nil, err
	}
	appTransport.BaseURL = g.apiURL
	client, err := g.client(gh.WithTransport(appTransport))
	return client, appTransport, err
}

func (g *GitHub) appRepositories(ctx context.Context, app store.GithubApp) ([]Repository, error) {
	appClient, appTransport, err := g.appClient(app)
	if err != nil {
		return nil, err
	}
	var repositories []Repository
	for installation, err := range appClient.Apps.ListInstallationsIter(ctx, &gh.ListOptions{PerPage: 100}) {
		if err != nil {
			return nil, err
		}
		client, err := g.client(gh.WithTransport(g.installationTransport(appTransport, installation.GetID())))
		if err != nil {
			return nil, err
		}
		for repository, err := range client.Apps.ListReposIter(ctx, &gh.ListOptions{PerPage: 100}) {
			if err != nil {
				return nil, err
			}
			repositories = append(repositories, Repository{
				FullName: repository.GetFullName(),
				AppID:    app.AppID,
				AppSlug:  app.Slug,
				Client:   client,
			})
		}
	}
	return repositories, nil
}

func (g *GitHub) installationTransport(appTransport *ghinstallation.AppsTransport, id int64) *ghinstallation.Transport {
	g.mu.Lock()
	defer g.mu.Unlock()
	transport, ok := g.transports[id]
	if !ok {
		transport = ghinstallation.NewFromAppsTransport(appTransport, id)
		g.transports[id] = transport
	}
	return transport
}

// AsOwner gives repository with a client that uses the user token of the Owner in place of the installation token.
func (g *GitHub) AsOwner(ctx context.Context, repository Repository) (Repository, error) {
	token, err := g.UserToken(ctx, repository.AppID)
	if err != nil {
		return Repository{}, err
	}
	repository.Client, err = g.client(gh.WithAuthToken(token))
	return repository, err
}

// Repositories gives the repositories of the last read.
func (g *GitHub) Repositories() []Repository {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.repositories)
}

// Organizations gives the owners of the repositories, sorted, each one time.
func (g *GitHub) Organizations() []string {
	organizations := []string{}
	for _, repository := range g.Repositories() {
		owner, _, _ := strings.Cut(repository.FullName, "/")
		organizations = append(organizations, owner)
	}
	slices.Sort(organizations)
	return slices.Compact(organizations)
}

// AppAccess is the access of a Mobius App in an account.
type AppAccess struct {
	// AppPermissions and InstallationPermissions give the level of each permission name.
	AppPermissions          map[string]string
	InstallationPermissions map[string]string
	// AppPermissionsURL is the page of the App where the Owner adds a permission.
	AppPermissionsURL string
	// InstallationURL is the page of the installation where the Owner accepts the new permissions.
	InstallationURL string
}

// AppAccess gives the permissions of the App appID and of its installation in account.
func (g *GitHub) AppAccess(ctx context.Context, appID int64, account string) (AppAccess, error) {
	app, err := g.queries.GetGitHubApp(ctx, appID)
	if err != nil {
		return AppAccess{}, err
	}
	client, _, err := g.appClient(app)
	if err != nil {
		return AppAccess{}, err
	}
	var installation *gh.Installation
	for found, err := range client.Apps.ListInstallationsIter(ctx, &gh.ListOptions{PerPage: 100}) {
		if err != nil {
			return AppAccess{}, err
		}
		if strings.EqualFold(found.GetAccount().GetLogin(), account) {
			installation = found
			break
		}
	}
	if installation == nil {
		return AppAccess{}, fmt.Errorf("the Mobius App has no installation in %s", account)
	}
	found, _, err := client.Apps.Get(ctx, "")
	if err != nil {
		return AppAccess{}, err
	}
	access := AppAccess{
		AppPermissionsURL: g.webURL + "/settings/apps/" + url.PathEscape(app.Slug) + "/permissions",
		InstallationURL:   g.webURL + "/settings/installations/" + strconv.FormatInt(installation.GetID(), 10),
	}
	if installation.GetAccount().GetType() == "Organization" {
		organization := g.webURL + "/organizations/" + url.PathEscape(account)
		access.AppPermissionsURL = organization + "/settings/apps/" + url.PathEscape(app.Slug) + "/permissions"
		access.InstallationURL = organization + "/settings/installations/" + strconv.FormatInt(installation.GetID(), 10)
	}
	if access.AppPermissions, err = levels(found.GetPermissions()); err != nil {
		return AppAccess{}, err
	}
	access.InstallationPermissions, err = levels(installation.GetPermissions())
	return access, err
}

// levels gives the level of each permission name. The JSON names of the fields are the permission names.
func levels(permissions *gh.InstallationPermissions) (map[string]string, error) {
	text, err := json.Marshal(permissions)
	if err != nil {
		return nil, err
	}
	var found map[string]string
	return found, json.Unmarshal(text, &found)
}

// Grants tells if permissions give the permission name at the level required or at a higher level.
// The levels are read, write and admin, in this order.
func Grants(permissions map[string]string, name, required string) bool {
	rank := func(level string) int { return slices.Index([]string{"read", "write", "admin"}, level) }
	level, ok := permissions[name]
	return ok && rank(level) >= rank(required)
}

// IssuePage is a list of issues and pull requests.
type IssuePage struct {
	Issues []*gh.Issue
	// ETag is empty when the list has more than one page: a 304 for page 1 says nothing about the other pages.
	ETag string
}

// IssuesSince gives the issues and pull requests that changed at or after since, or all of them when
// since is zero, in the order of their last change. It gives false when GitHub answers 304 Not Modified to etag.
func (r Repository) IssuesSince(ctx context.Context, since time.Time, etag string) (IssuePage, bool, error) {
	query := url.Values{"state": {"all"}, "sort": {"updated"}, "direction": {"asc"}, "per_page": {"100"}}
	if !since.IsZero() {
		query.Set("since", since.UTC().Format(time.RFC3339))
	}
	var result IssuePage
	for page := 1; ; page++ {
		query.Set("page", strconv.Itoa(page))
		request, err := r.Client.NewRequest(ctx, http.MethodGet, "repos/"+r.FullName+"/issues?"+query.Encode(), nil)
		if err != nil {
			return IssuePage{}, false, err
		}
		if page == 1 && etag != "" {
			request.Header.Set("If-None-Match", etag)
		}
		var issues []*gh.Issue
		response, err := r.Client.Do(request, &issues)
		if response != nil && response.StatusCode == http.StatusNotModified {
			return IssuePage{}, false, nil
		}
		if err != nil {
			return IssuePage{}, false, err
		}
		result.Issues = append(result.Issues, issues...)
		if page == 1 {
			result.ETag = response.Header.Get("ETag")
		}
		if response.NextPage == 0 {
			if page > 1 {
				result.ETag = ""
			}
			return result, true, nil
		}
	}
}
