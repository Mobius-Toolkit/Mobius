package testkit

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
)

// appJWT gives a JSON Web Token of the App appID, signed with the App key.
func appJWT(t *testing.T, appID int64, expires time.Time) string {
	t.Helper()
	block, _ := pem.Decode([]byte(AppPrivateKey))
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"iss":%d,"exp":%d}`, appID, expires.Unix()))
	digest := sha256.Sum256([]byte(header + "." + claims))
	signature, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return header + "." + claims + "." + base64.RawURLEncoding.EncodeToString(signature)
}

type result struct {
	StatusCode int
	Header     http.Header
}

// send sends the request with token and body, and decodes the JSON response into response when it is not nil.
func send(t *testing.T, method, url, token, body string, response any) result {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	reply, err := http.DefaultClient.Do(request)
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
			t.Fatalf("%s %s: %v: %s", method, url, err, text)
		}
	}
	return result{reply.StatusCode, reply.Header}
}

func installationToken(t *testing.T, github *FakeGitHub) string {
	t.Helper()
	var response struct {
		Token string `json:"token"`
	}
	send(t, http.MethodPost, github.URL+"/app/installations/1/access_tokens", appJWT(t, AppID, time.Now().Add(time.Minute)), "", &response)
	return response.Token
}

func TestGhinstallationGetsAnInstallationTokenAndTheRepositoriesOfItsApp(t *testing.T) {
	github := NewFakeGitHub(t)
	github.InstallSecondApp("other")
	github.AddRepository("owner/shop")
	github.AddRepository("other/garden")
	github.AddRepository("owner/cafe")
	var names [][]string
	for installation, appID := range []int64{AppID, SecondAppID} {
		transport, err := ghinstallation.New(http.DefaultTransport, appID, int64(installation+1), []byte(AppPrivateKey))
		if err != nil {
			t.Fatal(err)
		}
		transport.BaseURL = github.URL
		response, err := (&http.Client{Transport: transport}).Get(github.URL + "/installation/repositories")
		if err != nil {
			t.Fatal(err)
		}
		var list struct {
			Repositories []struct {
				FullName string `json:"full_name"`
			} `json:"repositories"`
		}
		err = json.NewDecoder(response.Body).Decode(&list)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		var appNames []string
		for _, repository := range list.Repositories {
			appNames = append(appNames, repository.FullName)
		}
		names = append(names, appNames)
		expires, _, err := transport.Expiry()
		if err != nil || expires.Before(time.Now().Add(59*time.Minute)) {
			t.Errorf("expiry = %v, %v", expires, err)
		}
	}

	want := [][]string{{"owner/shop", "owner/cafe"}, {"other/garden"}}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("repositories = %v", names)
	}
}

func TestAnExpiredInstallationTokenIsRefused(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddRepository("owner/shop")
	github.SetInstallationTokenLife(0)

	response := send(t, http.MethodGet, github.URL+"/installation/repositories", installationToken(t, github), "", nil)

	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d", response.StatusCode)
	}
}

func TestAnAppTokenNeedsTheAppKeyAndAnExpiryInTheFuture(t *testing.T) {
	github := NewFakeGitHub(t)
	github.SetAppPermissions(SecondAppID, map[string]string{"issues": "read"})
	type app struct {
		ID          int64             `json:"id"`
		Slug        string            `json:"slug"`
		Permissions map[string]string `json:"permissions"`
	}
	var first, second app

	send(t, http.MethodGet, github.URL+"/app", appJWT(t, AppID, time.Now().Add(time.Minute)), "", &first)
	send(t, http.MethodGet, github.URL+"/app", appJWT(t, SecondAppID, time.Now().Add(time.Minute)), "", &second)

	if first.ID != AppID || first.Slug != AppSlug || !reflect.DeepEqual(first.Permissions, defaultPermissions) {
		t.Errorf("first app = %+v", first)
	}
	if second.ID != SecondAppID || !reflect.DeepEqual(second.Permissions, map[string]string{"issues": "read"}) {
		t.Errorf("second app = %+v", second)
	}
	for _, token := range []string{"", "ghs_1", appJWT(t, AppID, time.Now().Add(-time.Second))} {
		if response := send(t, http.MethodGet, github.URL+"/app", token, "", nil); response.StatusCode != http.StatusUnauthorized {
			t.Errorf("status with %q = %d", token, response.StatusCode)
		}
	}
}

func TestTheManifestConversionsCreateTheAppsInOrderAndUseEachCodeOneTime(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddManifestCode("first-code")
	github.AddManifestCode("second-code")
	var app struct {
		ID           int64  `json:"id"`
		Slug         string `json:"slug"`
		PEM          string `json:"pem"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}

	send(t, http.MethodPost, github.URL+"/app-manifests/first-code/conversions", "", "", &app)
	if app.ID != AppID || app.Slug != AppSlug || app.PEM != AppPrivateKey || app.ClientID != AppClientID || app.ClientSecret != AppClientSecret {
		t.Errorf("first app = %+v", app)
	}
	send(t, http.MethodPost, github.URL+"/app-manifests/second-code/conversions", "", "", &app)
	if app.ID != SecondAppID || app.Slug != SecondAppSlug {
		t.Errorf("second app = %+v", app)
	}
	if response := send(t, http.MethodPost, github.URL+"/app-manifests/first-code/conversions", "", "", nil); response.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d", response.StatusCode)
	}
}

type exchangeResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
}

func exchange(t *testing.T, github *FakeGitHub, body string) exchangeResponse {
	t.Helper()
	var response exchangeResponse
	send(t, http.MethodPost, github.URL+"/login/oauth/access_token", "", body, &response)
	return response
}

func TestAUserCodeGivesAUserTokenAndARefreshTokenGivesANewOne(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddUserCode(AppID, "user-code", "owner")
	github.AddUserCode(SecondAppID, "second-code", "owner")
	credentials := fmt.Sprintf(`"client_id": %q, "client_secret": %q`, AppClientID, AppClientSecret)

	first := exchange(t, github, `{`+credentials+`, "code": "user-code"}`)
	if first != (exchangeResponse{AccessToken: "ghu_1", ExpiresIn: 28800, RefreshToken: "ghr_1"}) {
		t.Errorf("first = %+v", first)
	}
	var user struct {
		Login string `json:"login"`
	}
	send(t, http.MethodGet, github.URL+"/user", "ghu_1", "", &user)
	if user.Login != "owner" {
		t.Errorf("user = %+v", user)
	}

	if again := exchange(t, github, `{`+credentials+`, "code": "user-code"}`); again.Error != "bad_verification_code" {
		t.Errorf("same code = %+v", again)
	}
	if other := exchange(t, github, `{`+credentials+`, "code": "second-code"}`); other.Error != "bad_verification_code" {
		t.Errorf("code of the second App = %+v", other)
	}
	refreshed := exchange(t, github, `{`+credentials+`, "grant_type": "refresh_token", "refresh_token": "ghr_1"}`)
	if refreshed.AccessToken != "ghu_2" || refreshed.RefreshToken != "ghr_2" {
		t.Errorf("refreshed = %+v", refreshed)
	}
	if again := exchange(t, github, `{`+credentials+`, "grant_type": "refresh_token", "refresh_token": "ghr_1"}`); again.Error != "bad_refresh_token" {
		t.Errorf("same refresh token = %+v", again)
	}
	if wrong := exchange(t, github, `{"client_id": "Iv23test", "client_secret": "wrong", "code": "second-code"}`); wrong.Error != "incorrect_client_credentials" {
		t.Errorf("wrong secret = %+v", wrong)
	}
}

func TestTheInstallationHasTheAccountOfTheFirstRepositoryAndCanFail(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddAccount("acme", "Organization")
	github.AddRepository("acme/shop")
	github.SetInstallationPermissions(AppID, map[string]string{"workflows": "write"})
	token := appJWT(t, AppID, time.Now().Add(time.Minute))
	var installations []struct {
		ID          int64             `json:"id"`
		Account     map[string]string `json:"account"`
		Permissions map[string]string `json:"permissions"`
	}

	send(t, http.MethodGet, github.URL+"/app/installations", token, "", &installations)
	if len(installations) != 1 || installations[0].ID != 1 ||
		!reflect.DeepEqual(installations[0].Account, map[string]string{"login": "acme", "type": "Organization"}) ||
		!reflect.DeepEqual(installations[0].Permissions, map[string]string{"workflows": "write"}) {
		t.Errorf("installations = %+v", installations)
	}
	send(t, http.MethodGet, github.URL+"/app/installations", appJWT(t, SecondAppID, time.Now().Add(time.Minute)), "", &installations)
	if len(installations) != 0 {
		t.Errorf("installations of the second App = %+v", installations)
	}

	github.FailInstallations(AppID)

	if response := send(t, http.MethodGet, github.URL+"/app/installations", token, "", nil); response.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d", response.StatusCode)
	}
}

func TestTheAccountsAreTheAddedAccountsAndTheAppBots(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddAccount("acme", "Organization")
	var account accountJSON

	send(t, http.MethodGet, github.URL+"/users/acme", "", "", &account)
	if account != (accountJSON{Login: "acme", ID: 1, Type: "Organization"}) {
		t.Errorf("acme = %+v", account)
	}
	send(t, http.MethodGet, github.URL+"/users/mobius-second[bot]", "", "", &account)
	if account != (accountJSON{Login: "mobius-second[bot]", ID: botUserID, Type: "Bot"}) {
		t.Errorf("bot = %+v", account)
	}
	if response := send(t, http.MethodGet, github.URL+"/users/stranger", "", "", nil); response.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d", response.StatusCode)
	}
}

func TestARepositoryEndpointRefusesARequestWithNoValidToken(t *testing.T) {
	github := NewFakeGitHub(t)

	for _, token := range []string{"", "ghs_unknown"} {
		if response := send(t, http.MethodGet, github.URL+"/repos/owner/shop/issues", token, "", nil); response.StatusCode != http.StatusUnauthorized {
			t.Errorf("status with %q = %d", token, response.StatusCode)
		}
	}
}

func issueNumbers(issues []issueJSON) []int64 {
	numbers := []int64{}
	for _, issue := range issues {
		numbers = append(numbers, issue.Number)
	}
	return numbers
}

func TestTheIssueListFiltersByLabelAndTimeAndGivesNotModifiedForTheSameETag(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddIssue("owner/shop", 12, "Integrate loyalty plans")
	github.AddIssue("owner/shop", 13, "Plant roses")
	github.AddIssue("owner/garden", 1, "Water")
	github.AddLabel("owner/shop", 12, "mobius:workstream", "owner")
	token := installationToken(t, github)
	var issues []issueJSON

	send(t, http.MethodGet, github.URL+"/repos/owner/shop/issues", token, "", &issues)
	if numbers := issueNumbers(issues); !reflect.DeepEqual(numbers, []int64{13, 12}) {
		t.Errorf("all = %v", numbers)
	}
	if issues[1].ID != 100_012 || issues[1].User.Login != "owner" || !reflect.DeepEqual(issues[1].Labels, []nameJSON{{"mobius:workstream"}}) {
		t.Errorf("issue = %+v", issues[1])
	}
	send(t, http.MethodGet, github.URL+"/repos/owner/shop/issues?labels=mobius:workstream", token, "", &issues)
	if numbers := issueNumbers(issues); !reflect.DeepEqual(numbers, []int64{12}) {
		t.Errorf("labeled = %v", numbers)
	}
	send(t, http.MethodGet, github.URL+"/repos/owner/shop/issues?since="+issues[0].UpdatedAt, token, "", &issues)
	if numbers := issueNumbers(issues); !reflect.DeepEqual(numbers, []int64{12}) {
		t.Errorf("since = %v", numbers)
	}

	first := send(t, http.MethodGet, github.URL+"/repos/owner/shop/issues", token, "", nil)
	request, err := http.NewRequest(http.MethodGet, github.URL+"/repos/owner/shop/issues", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("If-None-Match", first.Header.Get("ETag"))
	second, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Body.Close()
	github.AddLabel("owner/shop", 13, "mobius:ready", "owner")
	third, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = third.Body.Close()

	if second.StatusCode != http.StatusNotModified || third.StatusCode != http.StatusOK || github.NotModifiedCount() != 1 {
		t.Errorf("statuses = %d, %d, not modified = %d", second.StatusCode, third.StatusCode, github.NotModifiedCount())
	}
}

func TestALabelWriteRecordsAnEventWithTheTokenOwner(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddIssue("owner/shop", 12, "Integrate loyalty plans")
	github.AddUserCode(AppID, "user-code", "owner")
	user := exchange(t, github, fmt.Sprintf(`{"client_id": %q, "client_secret": %q, "code": "user-code"}`, AppClientID, AppClientSecret)).AccessToken
	installation := installationToken(t, github)
	issue := github.URL + "/repos/owner/shop/issues/12"
	var labels []nameJSON

	send(t, http.MethodPost, issue+"/labels", installation, `{"labels": ["mobius:working", "mobius:working"]}`, &labels)
	if !reflect.DeepEqual(labels, []nameJSON{{"mobius:working"}}) {
		t.Errorf("labels = %+v", labels)
	}
	send(t, http.MethodDelete, issue+"/labels/mobius:working", user, "", &labels)
	if len(labels) != 0 || len(github.Labels("owner/shop", 12)) != 0 {
		t.Errorf("labels = %+v", labels)
	}
	if response := send(t, http.MethodDelete, issue+"/labels/mobius:working", user, "", nil); response.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d", response.StatusCode)
	}

	var events []eventJSON
	send(t, http.MethodGet, issue+"/events", installation, "", &events)
	var got []string
	for _, event := range events {
		got = append(got, event.Event+" "+event.Label.Name+" by "+event.Actor.Login)
	}
	want := []string{"labeled mobius:working by mobius-test[bot]", "unlabeled mobius:working by owner"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %q", got)
	}
}

func TestACreatedIssueGetsTheNextNumberAndHasSubIssuesAndComments(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddIssue("owner/shop", 12, "Integrate loyalty plans")
	github.AddIssue("owner/shop", 41, "Add plan model")
	token := installationToken(t, github)
	var created issueJSON

	send(t, http.MethodPost, github.URL+"/repos/owner/shop/issues", token, `{"title": "Seeds", "body": "Sell seeds."}`, &created)
	github.AddSubIssue("owner/shop", 12, 41)
	id := github.AddComment("owner/shop", 12, "owner", "Start with the model.")

	if created.Number != 42 || created.Title != "Seeds" || created.Body != "Sell seeds." || created.User.Login != "mobius-test[bot]" || created.State != "open" {
		t.Errorf("created = %+v", created)
	}
	var children []issueJSON
	send(t, http.MethodGet, github.URL+"/repos/owner/shop/issues/12/sub_issues", token, "", &children)
	if numbers := issueNumbers(children); !reflect.DeepEqual(numbers, []int64{41}) {
		t.Errorf("sub-issues = %v", numbers)
	}
	var comments []commentJSON
	send(t, http.MethodGet, github.URL+"/repos/owner/shop/issues/12/comments", token, "", &comments)
	if len(comments) != 1 || comments[0].ID != id || comments[0].User.Login != "owner" || comments[0].Body != "Start with the model." {
		t.Errorf("comments = %+v", comments)
	}
	if response := send(t, http.MethodGet, github.URL+"/repos/owner/shop/issues/7", token, "", nil); response.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d", response.StatusCode)
	}
}

func TestTheCommentListOfARepositoryFiltersSortsAndPages(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddIssue("owner/shop", 12, "Integrate loyalty plans")
	github.AddIssue("owner/shop", 41, "Add plan model")
	github.AddIssue("owner/other", 7, "Other")
	first := github.AddComment("owner/shop", 12, "owner", "One.")
	github.AddComment("owner/shop", 41, "owner", "Two.")
	github.AddComment("owner/other", 7, "owner", "Three.")
	last := github.AddCommentInLastSecond("owner/shop", 41, "owner", "Four.")
	github.EditComment("owner/shop", first, "One, edited.")
	token := installationToken(t, github)
	list := github.URL + "/repos/owner/shop/issues/comments"
	ids := func(query string) []int64 {
		var comments []commentJSON
		send(t, http.MethodGet, list+query, token, "", &comments)
		var ids []int64
		for _, comment := range comments {
			ids = append(ids, comment.ID)
		}
		return ids
	}

	created := ids("")
	updated := ids("?sort=updated&direction=desc")
	since := ids("?sort=updated&since=" + timestamp(github.Now().Unix()))
	paged := ids("?per_page=1&page=2")

	if !reflect.DeepEqual(created, []int64{first, first + 1, last}) || !reflect.DeepEqual(updated, []int64{first, last, first + 1}) ||
		!reflect.DeepEqual(since, []int64{first}) || !reflect.DeepEqual(paged, []int64{first + 1}) {
		t.Errorf("created = %v, updated = %v, since = %v, paged = %v", created, updated, since, paged)
	}
	var comments []commentJSON
	send(t, http.MethodGet, list, token, "", &comments)
	if comments[0].IssueURL != "https://api.github.com/repos/owner/shop/issues/12" {
		t.Errorf("comment = %+v", comments[0])
	}
}

func TestRepositoryLabelsCompareTheNameWithNoRegardToCase(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddRepositoryLabel("owner/shop", "mobius:working", "ededed", "Custom description")
	github.AddRepositoryLabel("owner/shop", "bug", "d73a4a", "Something is wrong")
	token := installationToken(t, github)
	labels := github.URL + "/repos/owner/shop/labels"

	created := send(t, http.MethodPost, labels, token, `{"name": "mobius:ready", "color": "0E8A16", "description": "Ready"}`, nil)
	duplicate := send(t, http.MethodPost, labels, token, `{"name": "MOBIUS:READY", "color": "0E8A16", "description": "Ready"}`, nil)
	patched := send(t, http.MethodPatch, labels+"/Mobius:Working", token, `{"color": "FBCA04"}`, nil)
	missing := send(t, http.MethodPatch, labels+"/mobius:autopilot", token, `{"color": "1D76DB"}`, nil)

	if created.StatusCode != http.StatusCreated || duplicate.StatusCode != http.StatusUnprocessableEntity ||
		patched.StatusCode != http.StatusOK || missing.StatusCode != http.StatusNotFound {
		t.Errorf("statuses = %d, %d, %d, %d", created.StatusCode, duplicate.StatusCode, patched.StatusCode, missing.StatusCode)
	}
	want := []Label{
		{"bug", "d73a4a", "Something is wrong"},
		{"mobius:ready", "0E8A16", "Ready"},
		{"mobius:working", "FBCA04", "Custom description"},
	}
	if got := github.RepositoryLabels("owner/shop"); !reflect.DeepEqual(got, want) {
		t.Errorf("labels = %+v", got)
	}
	if got := github.LabelPatches("owner/shop"); !reflect.DeepEqual(got, []string{"Mobius:Working"}) {
		t.Errorf("patches = %q", got)
	}
}

func TestAListPageHasTheLinkOfTheNextPage(t *testing.T) {
	github := NewFakeGitHub(t)
	for _, name := range []string{"a", "b", "c"} {
		github.AddRepositoryLabel("owner/shop", name, "ededed", "")
	}
	token := installationToken(t, github)
	var labels []Label

	first := send(t, http.MethodGet, github.URL+"/repos/owner/shop/labels?per_page=2", token, "", &labels)
	if len(labels) != 2 || labels[1].Name != "b" {
		t.Errorf("first page = %+v", labels)
	}
	next := strings.TrimSuffix(strings.TrimPrefix(first.Header.Get("Link"), "<"), `>; rel="next"`)
	last := send(t, http.MethodGet, next, token, "", &labels)

	if len(labels) != 1 || labels[0].Name != "c" || last.Header.Get("Link") != "" {
		t.Errorf("last page = %+v, link %q", labels, last.Header.Get("Link"))
	}
}

// GitHub takes the labels as an array of names or as the field labels of an object.
func TestALabelAdditionTakesAnArrayOfNames(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddIssue("owner/shop", 12, "Integrate loyalty plans")
	var labels []nameJSON

	send(t, http.MethodPost, github.URL+"/repos/owner/shop/issues/12/labels", installationToken(t, github), `["bug", "mobius:ready"]`, &labels)

	if !reflect.DeepEqual(labels, []nameJSON{{"bug"}, {"mobius:ready"}}) {
		t.Errorf("labels = %+v", labels)
	}
}

func TestASubIssueMovesToItsNewParentAndTheSummaryCountsTheOpenBlockers(t *testing.T) {
	github := NewFakeGitHub(t)
	for number := range int64(4) {
		github.AddIssue("owner/shop", number+1, "Issue")
	}
	github.AddSubIssue("owner/shop", 1, 3)
	github.AddBlockedBy("owner/shop", 3, 4)
	github.CloseIssue("owner/shop", 4)
	token := installationToken(t, github)
	issue := github.URL + "/repos/owner/shop/issues/"

	added := send(t, http.MethodPost, issue+"2/sub_issues", token, `{"sub_issue_id": 100003, "replace_parent": true}`, nil)
	send(t, http.MethodPost, issue+"3/dependencies/blocked_by", token, `{"issue_id": 100001}`, nil)

	var parent, child issueJSON
	send(t, http.MethodGet, issue+"3/parent", token, "", &parent)
	send(t, http.MethodGet, issue+"3", token, "", &child)
	if added.StatusCode != http.StatusCreated || parent.Number != 2 || len(github.SubIssueNumbers("owner/shop", 1)) != 0 {
		t.Errorf("status = %d, parent = #%d", added.StatusCode, parent.Number)
	}
	if summary := child.IssueDependenciesSummary; summary == nil || summary.BlockedBy != 1 || summary.TotalBlockedBy != 2 {
		t.Errorf("summary = %+v", summary)
	}
	if response := send(t, http.MethodGet, issue+"1/parent", token, "", nil); response.StatusCode != http.StatusNotFound {
		t.Errorf("parent of #1: status %d", response.StatusCode)
	}
}

func TestABodyWithAnUnknownFieldIsRefused(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddIssue("owner/shop", 12, "Integrate loyalty plans")

	response := send(t, http.MethodPost, github.URL+"/repos/owner/shop/issues/12/labels", installationToken(t, github), `{"names": ["bug"]}`, nil)

	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d", response.StatusCode)
	}
}

func TestTheNewAppPageSendsTheBrowserToTheRedirectURLWithANewCodeAndTheState(t *testing.T) {
	github := NewFakeGitHub(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	manifest := url.Values{"manifest": {`{"redirect_url": "https://mobius.example.ts.net/api/github/manifest-callback"}`}}

	response, err := client.PostForm(github.URL+"/organizations/acme/settings/apps/new?state=abc", manifest)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	want := "https://mobius.example.ts.net/api/github/manifest-callback?code=manifest-code-1&state=abc"
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != want {
		t.Errorf("status %d, location %q", response.StatusCode, response.Header.Get("Location"))
	}
	var app struct {
		ID int64 `json:"id"`
	}
	send(t, http.MethodPost, github.URL+"/app-manifests/manifest-code-1/conversions", "", "", &app)
	if app.ID != AppID {
		t.Errorf("app = %+v", app)
	}
}

func TestTheLatestReleaseOfMobiusNeedsNoToken(t *testing.T) {
	github := NewFakeGitHub(t)
	url := github.URL + "/repos/Mobius-Toolkit/Mobius/releases/latest"
	if got := send(t, http.MethodGet, url, "", "", nil); got.StatusCode != http.StatusNotFound {
		t.Errorf("status with no release = %d", got.StatusCode)
	}
	github.SetLatestRelease("v0.3.0", "mobius-x86_64-unknown-linux-gnu.tar.gz")

	var release releaseJSON
	got := send(t, http.MethodGet, url, "", "", &release)

	want := releaseJSON{TagName: "v0.3.0", Assets: []assetJSON{{"mobius-x86_64-unknown-linux-gnu.tar.gz"}}}
	if got.StatusCode != http.StatusOK || !reflect.DeepEqual(release, want) {
		t.Errorf("release = %d %+v", got.StatusCode, release)
	}
}

func TestAReactionGoesToAConversationCommentOrAReviewCommentOnceForEachContent(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddIssue("owner/shop", 12, "Integrate loyalty plans")
	github.AddIssue("owner/shop", 42, "Add plan model")
	conversation := github.AddComment("owner/shop", 12, "owner", "Start with the model.")
	review := github.AddReviewComment("owner/shop", 42, 0, "owner", "Rename plan to tier.")
	token := installationToken(t, github)
	base := github.URL + "/repos/owner/shop"

	first := send(t, http.MethodPost, fmt.Sprintf("%s/issues/comments/%d/reactions", base, conversation), token, `{"content": "eyes"}`, nil)
	second := send(t, http.MethodPost, fmt.Sprintf("%s/issues/comments/%d/reactions", base, conversation), token, `{"content": "eyes"}`, nil)
	send(t, http.MethodPost, fmt.Sprintf("%s/pulls/comments/%d/reactions", base, review), token, `{"content": "confused"}`, nil)
	wrongKind := send(t, http.MethodPost, fmt.Sprintf("%s/pulls/comments/%d/reactions", base, conversation), token, `{"content": "eyes"}`, nil)

	if first.StatusCode != http.StatusCreated || second.StatusCode != http.StatusOK || wrongKind.StatusCode != http.StatusNotFound {
		t.Errorf("statuses = %d, %d, %d", first.StatusCode, second.StatusCode, wrongKind.StatusCode)
	}
	if got := github.Reactions("owner/shop", conversation); !reflect.DeepEqual(got, []Reaction{{"mobius-test[bot]", "eyes"}}) {
		t.Errorf("reactions = %+v", got)
	}
	if got := github.Reactions("owner/shop", review); !reflect.DeepEqual(got, []Reaction{{"mobius-test[bot]", "confused"}}) {
		t.Errorf("reactions = %+v", got)
	}
}

func TestAFailedReactionContentGivesAServerErrorAndAddsNothing(t *testing.T) {
	github := NewFakeGitHub(t)
	github.AddIssue("owner/shop", 12, "Integrate loyalty plans")
	comment := github.AddComment("owner/shop", 12, "owner", "Start with the model.")
	token := installationToken(t, github)
	url := fmt.Sprintf("%s/repos/owner/shop/issues/comments/%d/reactions", github.URL, comment)
	github.FailReactions("rocket", true)

	failed := send(t, http.MethodPost, url, token, `{"content": "rocket"}`, nil)
	other := send(t, http.MethodPost, url, token, `{"content": "eyes"}`, nil)

	if failed.StatusCode != http.StatusInternalServerError || other.StatusCode != http.StatusCreated {
		t.Errorf("statuses = %d, %d", failed.StatusCode, other.StatusCode)
	}
	if got := github.Reactions("owner/shop", comment); !reflect.DeepEqual(got, []Reaction{{"mobius-test[bot]", "eyes"}}) {
		t.Errorf("reactions = %+v", got)
	}
}
