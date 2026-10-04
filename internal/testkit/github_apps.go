package testkit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// AddAccount makes GET /users/{login} give the account with accountType, for example "User" or "Organization".
func (g *FakeGitHub) AddAccount(login, accountType string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.accountTypes[login] = accountType
}

// AddManifestCode makes code valid for one App manifest conversion.
func (g *FakeGitHub) AddManifestCode(code string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.manifestCodes[code] = true
}

// AddUserCode makes code valid for one user token of login from the App appID.
func (g *FakeGitHub) AddUserCode(appID int64, code, login string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.userCodes[code] = grant{login: login, app: appIndex(appID)}
}

// InstallSecondApp gives the repositories of account to the second App. The first App has the repositories of all other accounts.
func (g *FakeGitHub) InstallSecondApp(account string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.secondAppAccounts[account] = true
}

// SetAppPermissions sets the permissions of the App appID.
func (g *FakeGitHub) SetAppPermissions(appID int64, permissions map[string]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.appPermissions[appID] = permissions
}

// SetInstallationPermissions sets the permissions of the installation of the App appID.
func (g *FakeGitHub) SetInstallationPermissions(appID int64, permissions map[string]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.installationPermissions[appID] = permissions
}

// FailInstallations makes the installation list of the App appID fail.
func (g *FakeGitHub) FailInstallations(appID int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failedApps[appID] = true
}

// SetInstallationTokenLife sets the time from the creation of an installation token to its expiry. The default is one hour, as on GitHub.
func (g *FakeGitHub) SetInstallationTokenLife(life time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.installationTokenLife = life
}

// AddRepository adds the repository fullName, for example "owner/shop", with a git repository that has one commit
// on main.
func (g *FakeGitHub) AddRepository(fullName string) {
	g.addRemote(fullName)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.repositories = append(g.repositories, fullName)
}

func appIndex(appID int64) int {
	return slices.IndexFunc(apps, func(app githubApp) bool { return app.id == appID })
}

func owner(repository string) string {
	owner, _, _ := strings.Cut(repository, "/")
	return owner
}

func (g *FakeGitHub) appOf(repository string) int {
	if g.secondAppAccounts[owner(repository)] {
		return 1
	}
	return 0
}

func botLogin(app int) string {
	return apps[app].slug + "[bot]"
}

func permissions(set map[int64]map[string]string, appID int64) map[string]string {
	if found, ok := set[appID]; ok {
		return found
	}
	return defaultPermissions
}

type accountJSON struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
	Type  string `json:"type"`
}

func (g *FakeGitHub) getAccount(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	for index := range apps {
		if name == botLogin(index) {
			writeJSON(w, http.StatusOK, accountJSON{Login: name, ID: botUserID, Type: "Bot"})
			return
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	accountType, ok := g.accountTypes[name]
	if !ok {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, accountJSON{Login: name, ID: 1, Type: accountType})
}

// InstallationTokensGiven gives the number of installation tokens that the fake created.
func (g *FakeGitHub) InstallationTokensGiven() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.installationTokensGiven
}

// newApp is the web page that creates an App from the `manifest` form field. The fake creates
// no form for the user: it adds a manifest code and sends the browser to the `redirect_url`
// of the manifest with the code and the `state` of the page URL, as GitHub does after the form.
func (g *FakeGitHub) newApp(w http.ResponseWriter, r *http.Request) {
	var manifest struct {
		RedirectURL string `json:"redirect_url"`
	}
	if err := json.Unmarshal([]byte(r.PostFormValue("manifest")), &manifest); err != nil {
		message(w, http.StatusBadRequest, err.Error())
		return
	}
	g.mu.Lock()
	g.manifestCodesGiven++
	code := fmt.Sprintf("manifest-code-%d", g.manifestCodesGiven)
	g.manifestCodes[code] = true
	g.mu.Unlock()
	query := url.Values{"code": {code}, "state": {r.URL.Query().Get("state")}}
	http.Redirect(w, r, manifest.RedirectURL+"?"+query.Encode(), http.StatusFound)
}

func (g *FakeGitHub) convertManifest(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	code := r.PathValue("code")
	if !g.manifestCodes[code] || g.appsCreated == len(apps) {
		notFound(w)
		return
	}
	delete(g.manifestCodes, code)
	app := apps[g.appsCreated]
	g.appsCreated++
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":            app.id,
		"slug":          app.slug,
		"pem":           AppPrivateKey,
		"client_id":     app.clientID,
		"client_secret": app.clientSecret,
	})
}

// exchangeCode gives a user token for a user code or a refresh token. GitHub answers a refused exchange with status 200 and an `error` field.
func (g *FakeGitHub) exchangeCode(w http.ResponseWriter, r *http.Request) {
	var exchange struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Code         string `json:"code"`
		GrantType    string `json:"grant_type"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&exchange); err != nil {
		message(w, http.StatusBadRequest, err.Error())
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	app := slices.IndexFunc(apps, func(app githubApp) bool {
		return app.clientID == exchange.ClientID && app.clientSecret == exchange.ClientSecret
	})
	if app < 0 {
		refuse(w, "incorrect_client_credentials", "The client_id and/or client_secret passed are incorrect.")
		return
	}
	grants, key := g.userCodes, exchange.Code
	if exchange.GrantType == "refresh_token" {
		grants, key = g.refreshTokens, exchange.RefreshToken
	}
	found, ok := grants[key]
	if !ok || found.app != app {
		if exchange.GrantType == "refresh_token" {
			refuse(w, "bad_refresh_token", "The refresh token passed is incorrect or expired.")
		} else {
			refuse(w, "bad_verification_code", "The code passed is incorrect or expired.")
		}
		return
	}
	delete(grants, key)
	g.userTokensGiven++
	number := strconv.Itoa(g.userTokensGiven)
	const life = 8 * time.Hour
	g.tokens["ghu_"+number] = token{login: found.login, app: app, expires: time.Now().Add(life)}
	g.refreshTokens["ghr_"+number] = found
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":             "ghu_" + number,
		"expires_in":               int(life.Seconds()),
		"refresh_token":            "ghr_" + number,
		"refresh_token_expires_in": 15638400,
		"scope":                    "",
		"token_type":               "bearer",
	})
}

func refuse(w http.ResponseWriter, code, description string) {
	writeJSON(w, http.StatusOK, map[string]string{"error": code, "error_description": description})
}

func (g *FakeGitHub) getUser(w http.ResponseWriter, r *http.Request) {
	found, ok := g.validToken(r)
	if !ok || found.installation {
		badCredentials(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"login": found.login})
}

func (g *FakeGitHub) getApp(w http.ResponseWriter, r *http.Request) {
	index, ok := g.signer(r)
	if !ok {
		badCredentials(w)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	app := apps[index]
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          app.id,
		"slug":        app.slug,
		"permissions": permissions(g.appPermissions, app.id),
	})
}

// listInstallations gives the one installation of the App, on the account of its first repository.
func (g *FakeGitHub) listInstallations(w http.ResponseWriter, r *http.Request) {
	index, ok := g.signer(r)
	if !ok {
		badCredentials(w)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	app := apps[index]
	if g.failedApps[app.id] {
		message(w, http.StatusInternalServerError, "Server Error")
		return
	}
	first := slices.IndexFunc(g.repositories, func(repository string) bool { return g.appOf(repository) == index })
	if first < 0 {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	login := owner(g.repositories[first])
	accountType, ok := g.accountTypes[login]
	if !ok {
		accountType = "User"
	}
	writeJSON(w, http.StatusOK, []any{map[string]any{
		"id":          index + 1,
		"app_id":      app.id,
		"account":     map[string]string{"login": login, "type": accountType},
		"permissions": permissions(g.installationPermissions, app.id),
	}})
}

func (g *FakeGitHub) createInstallationToken(w http.ResponseWriter, r *http.Request) {
	index, ok := g.signer(r)
	if !ok {
		badCredentials(w)
		return
	}
	if r.PathValue("id") != strconv.Itoa(index+1) {
		notFound(w)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.installationTokensGiven++
	value := fmt.Sprintf("ghs_%d", g.installationTokensGiven)
	expires := time.Now().Add(g.installationTokenLife)
	g.tokens[value] = token{login: botLogin(index), app: index, installation: true, expires: expires}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":       value,
		"expires_at":  expires.UTC().Format(time.RFC3339),
		"permissions": permissions(g.installationPermissions, apps[index].id),
	})
}

type repositoryJSON struct {
	FullName      string            `json:"full_name"`
	Name          string            `json:"name"`
	Owner         map[string]string `json:"owner"`
	DefaultBranch string            `json:"default_branch"`
	CloneURL      string            `json:"clone_url"`
}

func (g *FakeGitHub) listInstallationRepositories(w http.ResponseWriter, r *http.Request) {
	found, ok := g.validToken(r)
	if !ok || !found.installation {
		badCredentials(w)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	repositories := []repositoryJSON{}
	for _, repository := range g.repositories {
		if g.appOf(repository) == found.app {
			owner, name, _ := strings.Cut(repository, "/")
			repositories = append(repositories, repositoryJSON{
				FullName:      repository,
				Name:          name,
				Owner:         map[string]string{"login": owner},
				DefaultBranch: "main",
				CloneURL:      "file://" + g.Remote(repository),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_count":  len(repositories),
		"repositories": page(w, r, repositories),
	})
}
