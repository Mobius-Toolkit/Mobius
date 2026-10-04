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

// The permission names with the levels that the Mobius App needs.
var requiredPermissions = map[string]string{
	"issues":        "write",
	"pull_requests": "write",
	"contents":      "write",
	"checks":        "write",
	"workflows":     "write",
	"actions":       "read",
	"metadata":      "read",
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
	manifest, err := json.Marshal(map[string]any{
		"name":                     name,
		"url":                      "https://github.com/Mobius-Toolkit/Mobius",
		"redirect_url":             origin + "/api/github/manifest-callback",
		"callback_urls":            []string{origin + "/api/github/user-callback"},
		"request_oauth_on_install": true,
		"public":                   false,
		"default_permissions":      requiredPermissions,
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

// Run reads the repositories of the installations of each App now and then after each interval, until ctx ends.
func (g *GitHub) Run(ctx context.Context, interval time.Duration) {
	for {
		if err := g.Refresh(ctx); err != nil {
			log.Printf("read the repositories of the GitHub Apps: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// Refresh reads the repositories of the installations of each App. An App whose read
// fails keeps the repositories of its last read.
func (g *GitHub) Refresh(ctx context.Context) error {
	apps, err := g.queries.ListGitHubApps(ctx)
	if err != nil {
		return err
	}
	var repositories []Repository
	for _, app := range apps {
		found, err := g.appRepositories(ctx, app)
		if err != nil {
			log.Printf("read the repositories of the GitHub App %s: %v", app.Slug, err)
			found = slices.DeleteFunc(g.Repositories(), func(r Repository) bool { return r.AppID != app.AppID })
		}
		repositories = append(repositories, found...)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.repositories = repositories
	return nil
}

func (g *GitHub) appRepositories(ctx context.Context, app store.GithubApp) ([]Repository, error) {
	appTransport, err := ghinstallation.NewAppsTransport(http.DefaultTransport, app.AppID, []byte(app.PrivateKey))
	if err != nil {
		return nil, err
	}
	appTransport.BaseURL = g.apiURL
	appClient, err := g.client(gh.WithTransport(appTransport))
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
