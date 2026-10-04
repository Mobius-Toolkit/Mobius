package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit/testserver"
)

type result struct {
	StatusCode int
	Header     http.Header
}

// call sends the request with the body to the server with client, and decodes the JSON response into response when it is not nil.
func call(t *testing.T, client *http.Client, method, address, body string, response any) result {
	t.Helper()
	request, err := http.NewRequest(method, address, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	reply, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reply.Body.Close() }()
	text, err := io.ReadAll(reply.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response != nil {
		if err := json.Unmarshal(text, response); err != nil {
			t.Fatalf("%s %s: %v: %s", method, address, err, text)
		}
	}
	return result{reply.StatusCode, reply.Header}
}

// noRedirects gives a copy of client that does not follow redirects.
func noRedirects(client *http.Client) *http.Client {
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copied
}

type manifestForm struct {
	Data struct {
		URL      string `json:"url"`
		Manifest string `json:"manifest"`
	} `json:"data"`
}

func createManifestForm(t *testing.T, server *testserver.Server, account string) manifestForm {
	t.Helper()
	var form manifestForm
	body := `{"account": "` + account + `", "name": "Mobius ` + account + `", "origin": "` + server.URL + `"}`
	if reply := call(t, server.Client, http.MethodPost, server.URL+"/api/github/manifest", body, &form); reply.StatusCode != http.StatusOK {
		t.Fatalf("manifest form: status %d", reply.StatusCode)
	}
	return form
}

// createApp posts the manifest form to the fake GitHub as the browser does, and follows the
// redirect of GitHub to the manifest callback.
func createApp(t *testing.T, server *testserver.Server, account string) result {
	t.Helper()
	form := createManifestForm(t, server, account)
	reply, err := noRedirects(server.Client).PostForm(form.Data.URL, url.Values{"manifest": {form.Data.Manifest}})
	if err != nil {
		t.Fatal(err)
	}
	_ = reply.Body.Close()
	return call(t, noRedirects(server.Client), http.MethodGet, reply.Header.Get("Location"), "", nil)
}

type githubApps struct {
	Data []struct {
		Slug         string   `json:"slug"`
		InstallURL   string   `json:"installUrl"`
		Repositories []string `json:"repositories"`
	} `json:"data"`
}

func TestTheSetupPageCreatesAnAppAndTheServerListsTheRepositoriesOfItsInstallation(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	github.AddAccount("acme", "Organization")
	github.AddRepository("acme/shop")
	github.AddRepository("acme/cafe")
	server := testserver.Start(t, t.TempDir(), github.URL)

	reply := createApp(t, server, "acme")

	if reply.StatusCode != http.StatusSeeOther || reply.Header.Get("Location") != "/github" {
		t.Errorf("callback: status %d, location %q", reply.StatusCode, reply.Header.Get("Location"))
	}
	apps := testkit.WaitForValue(t, func() (githubApps, bool) {
		var apps githubApps
		call(t, server.Client, http.MethodGet, server.URL+"/api/github/apps", "", &apps)
		return apps, len(apps.Data) == 1 && len(apps.Data[0].Repositories) == 2
	})
	app := apps.Data[0]
	if app.Slug != testkit.AppSlug || app.InstallURL != github.URL+"/apps/mobius-test/installations/new" ||
		!slices.Equal(app.Repositories, []string{"acme/shop", "acme/cafe"}) {
		t.Errorf("app = %+v", app)
	}
	var organizations struct {
		Data []string `json:"data"`
	}
	call(t, server.Client, http.MethodGet, server.URL+"/api/organizations", "", &organizations)
	if !reflect.DeepEqual(organizations.Data, []string{"acme"}) {
		t.Errorf("organizations = %v", organizations.Data)
	}
}

func TestTheServerWithNoAppHasNoAppAndNoOrganization(t *testing.T) {
	server := testserver.Start(t, t.TempDir(), testkit.NewFakeGitHub(t).URL)
	var apps githubApps
	var organizations struct {
		Data []string `json:"data"`
	}

	call(t, server.Client, http.MethodGet, server.URL+"/api/github/apps", "", &apps)
	call(t, server.Client, http.MethodGet, server.URL+"/api/organizations", "", &organizations)

	if apps.Data == nil || len(apps.Data) != 0 || organizations.Data == nil || len(organizations.Data) != 0 {
		t.Errorf("apps = %+v, organizations = %v", apps.Data, organizations.Data)
	}
}

func TestTheManifestCallbackRefusesAStateThatNoFormGave(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	github.AddManifestCode("manifest-code")
	server := testserver.Start(t, t.TempDir(), github.URL)

	reply := call(t, noRedirects(server.Client), http.MethodGet, server.URL+"/api/github/manifest-callback?code=manifest-code&state=forged", "", nil)

	var apps githubApps
	call(t, server.Client, http.MethodGet, server.URL+"/api/github/apps", "", &apps)
	if reply.StatusCode != http.StatusForbidden || len(apps.Data) != 0 {
		t.Errorf("status = %d, apps = %+v", reply.StatusCode, apps.Data)
	}
}

func TestTheCallbacksNeedTheCookieOfADeviceLogin(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	github.AddAccount("acme", "Organization")
	server := testserver.Start(t, t.TempDir(), github.URL)
	form := createManifestForm(t, server, "acme")
	reply, err := noRedirects(server.Client).PostForm(form.Data.URL, url.Values{"manifest": {form.Data.Manifest}})
	if err != nil {
		t.Fatal(err)
	}
	_ = reply.Body.Close()
	stranger := noRedirects(server.Client)
	stranger.Jar = nil

	for _, address := range []string{reply.Header.Get("Location"), server.URL + "/api/github/user-callback?code=user-code"} {
		if reply := call(t, stranger, http.MethodGet, address, "", nil); reply.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d", address, reply.StatusCode)
		}
	}
	if reply := call(t, noRedirects(server.Client), http.MethodGet, reply.Header.Get("Location"), "", nil); reply.StatusCode != http.StatusSeeOther {
		t.Errorf("the state is not valid after a request with no login: status %d", reply.StatusCode)
	}
}

func TestTheUserCallbackStoresTheTokensOfATrustedUserAndRefusesOtherUsers(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	github.AddAccount("owner", "User")
	github.AddUserCode(testkit.AppID, "mallory-code", "mallory")
	github.AddUserCode(testkit.AppID, "owner-code", "owner")
	server := testserver.Start(t, t.TempDir(), github.URL)
	createApp(t, server, "owner")
	client := noRedirects(server.Client)

	refused := call(t, client, http.MethodGet, server.URL+"/api/github/user-callback?code=mallory-code", "", nil)
	accepted := call(t, client, http.MethodGet, server.URL+"/api/github/user-callback?code=owner-code", "", nil)

	if refused.StatusCode != http.StatusForbidden || accepted.StatusCode != http.StatusSeeOther || accepted.Header.Get("Location") != "/github" {
		t.Errorf("statuses = %d, %d", refused.StatusCode, accepted.StatusCode)
	}
	var token string
	if err := server.DB.QueryRow("SELECT user_token FROM github_apps").Scan(&token); err != nil || token != "ghu_2" {
		t.Errorf("user token = %q, %v", token, err)
	}
}

func TestTheManifestFormRefusesABadAppNameAndAnUnknownAccount(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	github.AddAccount("owner", "User")
	server := testserver.Start(t, t.TempDir(), github.URL)
	cases := []struct {
		account, name string
		status        int
		error         string
	}{
		{"owner", "   ", http.StatusBadRequest, "The App name must have 1 to 34 characters."},
		{"owner", strings.Repeat("m", 35), http.StatusBadRequest, "The App name must have 1 to 34 characters."},
		{"stranger", "Mobius stranger", http.StatusUnprocessableEntity, "GitHub has no account stranger."},
	}

	for _, c := range cases {
		var body struct {
			Error string `json:"error"`
		}
		request := `{"account": "` + c.account + `", "name": "` + c.name + `", "origin": "` + server.URL + `"}`
		reply := call(t, server.Client, http.MethodPost, server.URL+"/api/github/manifest", request, &body)
		if reply.StatusCode != c.status || body.Error != c.error {
			t.Errorf("%s %q: status %d, error %q", c.account, c.name, reply.StatusCode, body.Error)
		}
	}
	var form manifestForm
	request := `{"account": "owner", "name": "  ` + strings.Repeat("m", 34) + ` ", "origin": "` + server.URL + `"}`
	call(t, server.Client, http.MethodPost, server.URL+"/api/github/manifest", request, &form)
	if !strings.Contains(form.Data.Manifest, `"name":"`+strings.Repeat("m", 34)+`"`) {
		t.Errorf("manifest = %s", form.Data.Manifest)
	}
}
