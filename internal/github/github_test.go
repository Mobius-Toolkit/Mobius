package github_test

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func start(t *testing.T, fake *testkit.FakeGitHub) (*github.GitHub, *store.Queries) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "mobius.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	queries := store.New(db)
	gh, err := github.New(queries, fake.URL, fake.URL, []string{"owner"})
	if err != nil {
		t.Fatal(err)
	}
	return gh, queries
}

func manifestForm(t *testing.T, gh *github.GitHub, account string) github.ManifestForm {
	t.Helper()
	form, found, err := gh.ManifestForm(t.Context(), account, "Mobius "+account, "https://mobius.example.ts.net")
	if err != nil || !found {
		t.Fatalf("manifest form of %s: %v, %v", account, found, err)
	}
	return form
}

func state(t *testing.T, form github.ManifestForm) string {
	t.Helper()
	page, err := url.Parse(form.URL)
	if err != nil {
		t.Fatal(err)
	}
	return page.Query().Get("state")
}

// convert creates the next App of the fake GitHub through a form of the App setup.
func convert(t *testing.T, gh *github.GitHub, fake *testkit.FakeGitHub, code string) {
	t.Helper()
	fake.AddAccount("owner", "User")
	fake.AddManifestCode(code)
	if ok, err := gh.ConvertManifest(t.Context(), code, state(t, manifestForm(t, gh, "owner"))); err != nil || !ok {
		t.Fatalf("convert %s: %v, %v", code, ok, err)
	}
}

func authorize(t *testing.T, gh *github.GitHub, code string) bool {
	t.Helper()
	ok, err := gh.AuthorizeUser(t.Context(), code)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func app(t *testing.T, queries *store.Queries, appID int64) store.GithubApp {
	t.Helper()
	row, err := queries.GetGitHubApp(t.Context(), appID)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

var statePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestManifestFormPostsTheManifestToTheSettingsOfAnOrganization(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddAccount("acme", "Organization")
	gh, _ := start(t, fake)

	form, found, err := gh.ManifestForm(t.Context(), "acme", "Mobius acme", "https://mobius.example.ts.net")
	if err != nil || !found {
		t.Fatal(found, err)
	}

	page, _, _ := strings.Cut(form.URL, "?")
	if page != fake.URL+"/organizations/acme/settings/apps/new" || !statePattern.MatchString(state(t, form)) {
		t.Errorf("url = %s", form.URL)
	}
	var manifest any
	if err := json.Unmarshal([]byte(form.Manifest), &manifest); err != nil {
		t.Fatal(err)
	}
	var want any
	_ = json.Unmarshal([]byte(`{
		"name": "Mobius acme",
		"url": "https://github.com/Mobius-Toolkit/Mobius",
		"redirect_url": "https://mobius.example.ts.net/api/github/manifest-callback",
		"callback_urls": ["https://mobius.example.ts.net/api/github/user-callback"],
		"request_oauth_on_install": true,
		"public": false,
		"default_permissions": {
			"issues": "write",
			"pull_requests": "write",
			"contents": "write",
			"checks": "write",
			"workflows": "write",
			"actions": "write",
			"metadata": "read"
		}
	}`), &want)
	if !reflect.DeepEqual(manifest, want) {
		t.Errorf("manifest = %s", form.Manifest)
	}
}

func TestManifestFormPostsTheManifestToThePersonalSettingsOfAUser(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddAccount("owner", "User")
	gh, _ := start(t, fake)

	form := manifestForm(t, gh, "owner")

	if page, _, _ := strings.Cut(form.URL, "?"); page != fake.URL+"/settings/apps/new" {
		t.Errorf("url = %s", form.URL)
	}
}

func TestManifestFormFindsNoAccountThatGitHubDoesNotHave(t *testing.T) {
	gh, _ := start(t, testkit.NewFakeGitHub(t))

	_, found, err := gh.ManifestForm(t.Context(), "stranger", "Mobius stranger", "https://mobius.example.ts.net")

	if err != nil || found {
		t.Errorf("found = %v, %v", found, err)
	}
}

func TestManifestCallbackStoresTheApp(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	gh, queries := start(t, fake)

	convert(t, gh, fake, "manifest-code")

	want := store.GithubApp{
		AppID:        testkit.AppID,
		Slug:         testkit.AppSlug,
		PrivateKey:   testkit.AppPrivateKey,
		ClientID:     testkit.AppClientID,
		ClientSecret: testkit.AppClientSecret,
	}
	if got := app(t, queries, testkit.AppID); got != want {
		t.Errorf("app = %+v", got)
	}
}

func TestManifestCallbackNeedsTheStateOfAFormAndUsesItOneTime(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddAccount("owner", "User")
	fake.AddManifestCode("first-code")
	fake.AddManifestCode("second-code")
	gh, queries := start(t, fake)
	form := manifestForm(t, gh, "owner")

	for _, wrong := range []string{"", strings.Repeat("0", 64)} {
		if ok, err := gh.ConvertManifest(t.Context(), "first-code", wrong); err != nil || ok {
			t.Errorf("state %q: %v, %v", wrong, ok, err)
		}
	}
	if ok, err := gh.ConvertManifest(t.Context(), "first-code", state(t, form)); err != nil || !ok {
		t.Fatalf("state of the form: %v, %v", ok, err)
	}
	if ok, err := gh.ConvertManifest(t.Context(), "second-code", state(t, form)); err != nil || ok {
		t.Errorf("same state again: %v, %v", ok, err)
	}

	apps, err := queries.ListGitHubApps(t.Context())
	if err != nil || len(apps) != 1 {
		t.Errorf("apps = %+v, %v", apps, err)
	}
}

func TestUserCallbackStoresTheTokensAndANewAuthorizationReplacesThem(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "first-code", "owner")
	fake.AddUserCode(testkit.AppID, "second-code", "owner")
	gh, queries := start(t, fake)
	convert(t, gh, fake, "manifest-code")

	if !authorize(t, gh, "first-code") {
		t.Fatal("the first authorization is refused")
	}
	first := app(t, queries, testkit.AppID)
	expires, err := time.Parse(time.RFC3339Nano, first.UserTokenExpiresAt.String)
	if first.UserToken.String != "ghu_1" || first.RefreshToken.String != "ghr_1" || err != nil || time.Until(expires) < 7*time.Hour {
		t.Errorf("app = %+v, %v", first, err)
	}

	if !authorize(t, gh, "second-code") {
		t.Fatal("the second authorization is refused")
	}
	second := app(t, queries, testkit.AppID)
	if second.UserToken.String != "ghu_2" || second.RefreshToken.String != "ghr_2" {
		t.Errorf("app = %+v", second)
	}
}

func TestUserCallbackRefusesALoginThatIsNotTrusted(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "mallory-code", "mallory")
	gh, queries := start(t, fake)
	convert(t, gh, fake, "manifest-code")

	if authorize(t, gh, "mallory-code") {
		t.Error("the authorization of mallory is accepted")
	}

	if got := app(t, queries, testkit.AppID); got.UserToken.Valid || got.RefreshToken.Valid {
		t.Errorf("app = %+v", got)
	}
}

func TestManifestCallbackAddsASecondApp(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	gh, queries := start(t, fake)
	convert(t, gh, fake, "first-code")

	convert(t, gh, fake, "second-code")

	apps, err := queries.ListGitHubApps(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, app := range apps {
		slugs = append(slugs, app.Slug)
	}
	if !slices.Equal(slugs, []string{testkit.AppSlug, testkit.SecondAppSlug}) {
		t.Errorf("slugs = %v", slugs)
	}
}

func TestUserCallbackStoresTheTokensOnTheAppOfTheCode(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.SecondAppID, "user-code", "owner")
	gh, queries := start(t, fake)
	convert(t, gh, fake, "first-code")
	convert(t, gh, fake, "second-code")

	if !authorize(t, gh, "user-code") {
		t.Fatal("the authorization is refused")
	}

	if first := app(t, queries, testkit.AppID); first.UserToken.Valid {
		t.Errorf("first app = %+v", first)
	}
	if second := app(t, queries, testkit.SecondAppID); second.UserToken.String != "ghu_1" {
		t.Errorf("second app = %+v", second)
	}
}

func TestUserCallbackComparesTheLoginWithoutLetterCase(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "Owner")
	gh, _ := start(t, fake)
	convert(t, gh, fake, "manifest-code")

	if !authorize(t, gh, "user-code") {
		t.Error("the authorization of Owner is refused")
	}
}

func TestUserTokenGivesTheStoredTokenWhileItIsValidForMoreThanFiveMinutes(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	gh, _ := start(t, fake)
	convert(t, gh, fake, "manifest-code")
	authorize(t, gh, "user-code")

	token, err := gh.UserToken(t.Context(), testkit.AppID)

	if token != "ghu_1" || err != nil {
		t.Errorf("token = %q, %v", token, err)
	}
}

func TestUserTokenRefreshesATokenThatExpiresWithinFiveMinutesAndStoresTheNewTokens(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	gh, queries := start(t, fake)
	convert(t, gh, fake, "manifest-code")
	authorize(t, gh, "user-code")
	err := queries.SetUserTokens(t.Context(), store.SetUserTokensParams{
		UserToken:          sql.NullString{String: "ghu_1", Valid: true},
		RefreshToken:       sql.NullString{String: "ghr_1", Valid: true},
		UserTokenExpiresAt: sql.NullString{String: time.Now().Add(4 * time.Minute).UTC().Format(time.RFC3339Nano), Valid: true},
		AppID:              testkit.AppID,
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := gh.UserToken(t.Context(), testkit.AppID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gh.UserToken(t.Context(), testkit.AppID)
	if err != nil {
		t.Fatal(err)
	}

	stored := app(t, queries, testkit.AppID)
	if first != "ghu_2" || second != "ghu_2" || stored.UserToken.String != "ghu_2" || stored.RefreshToken.String != "ghr_2" {
		t.Errorf("tokens = %q, %q, app = %+v", first, second, stored)
	}
}

// The Rust version writes the expiry in RFC 3339 of the time crate, with the fraction of a second only when it is not
// zero, and with no trailing zero.
func TestUserTokenReadsTheExpiryThatTheRustVersionWrote(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	fake.AddUserCode(testkit.AppID, "user-code", "owner")
	gh, queries := start(t, fake)
	convert(t, gh, fake, "manifest-code")
	authorize(t, gh, "user-code")
	err := queries.SetUserTokens(t.Context(), store.SetUserTokensParams{
		UserToken:          sql.NullString{String: "ghu_1", Valid: true},
		RefreshToken:       sql.NullString{String: "ghr_1", Valid: true},
		UserTokenExpiresAt: sql.NullString{String: "2099-01-01T00:00:00.5Z", Valid: true},
		AppID:              testkit.AppID,
	})
	if err != nil {
		t.Fatal(err)
	}

	token, err := gh.UserToken(t.Context(), testkit.AppID)

	if token != "ghu_1" || err != nil {
		t.Errorf("token = %q, %v", token, err)
	}
}

func TestUserTokenTellsTheOwnerToAuthorizeTheAppWhenThereIsNoToken(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	gh, _ := start(t, fake)
	convert(t, gh, fake, "manifest-code")

	_, err := gh.UserToken(t.Context(), testkit.AppID)

	if err == nil || !strings.Contains(err.Error(), fake.URL+"/login/oauth/authorize?client_id="+testkit.AppClientID) {
		t.Errorf("error = %v", err)
	}
}

// connect gives the first App the organization owner, and the second App the organization other.
func connect(t *testing.T, fake *testkit.FakeGitHub) *github.GitHub {
	t.Helper()
	fake.InstallSecondApp("other")
	fake.AddRepository("owner/shop")
	fake.AddRepository("other/garden")
	gh, _ := start(t, fake)
	convert(t, gh, fake, "first-code")
	convert(t, gh, fake, "second-code")
	if _, err := gh.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	return gh
}

func repositoryNames(gh *github.GitHub) []string {
	var names []string
	for _, repository := range gh.Repositories() {
		names = append(names, repository.FullName+" "+repository.AppSlug)
	}
	slices.Sort(names)
	return names
}

func TestEachAppGivesTheRepositoriesOfItsOrganization(t *testing.T) {
	gh := connect(t, testkit.NewFakeGitHub(t))

	if got := gh.Organizations(); !slices.Equal(got, []string{"other", "owner"}) {
		t.Errorf("organizations = %v", got)
	}
	if got, want := repositoryNames(gh), []string{"other/garden mobius-second", "owner/shop mobius-test"}; !slices.Equal(got, want) {
		t.Errorf("repositories = %v", got)
	}
}

func TestAFailedAppKeepsItsRepositoriesAndTheOtherAppContinues(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	gh := connect(t, fake)

	fake.FailInstallations(testkit.SecondAppID)
	fake.AddRepository("owner/cafe")
	if complete, err := gh.Refresh(t.Context()); err != nil || complete {
		t.Fatalf("complete = %v, err = %v", complete, err)
	}

	want := []string{"other/garden mobius-second", "owner/cafe mobius-test", "owner/shop mobius-test"}
	if got := repositoryNames(gh); !slices.Equal(got, want) {
		t.Errorf("repositories = %v", got)
	}
}

func TestTheClientOfARepositoryGetsANewInstallationTokenBeforeTheTokenExpires(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	// ghinstallation gets a new token when the token expires within one minute.
	fake.SetInstallationTokenLife(30 * time.Second)
	gh := connect(t, fake)
	client := gh.Repositories()[0].Client
	given := fake.InstallationTokensGiven()

	for range 2 {
		if _, _, err := client.Apps.ListRepos(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
	}

	if got := fake.InstallationTokensGiven(); got != given+2 {
		t.Errorf("installation tokens = %d, want %d", got, given+2)
	}
}

func TestAnInstallationKeepsItsTokenUntilTheTokenExpires(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	gh := connect(t, fake)

	for range 3 {
		if _, err := gh.Refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	if got := fake.InstallationTokensGiven(); got != 2 {
		t.Errorf("installation tokens = %d, want one for each App", got)
	}
}

func TestPullRequestReviewsReadsTheNextPagesOfEachConnection(t *testing.T) {
	const shop = "owner/shop"
	fake := testkit.NewFakeGitHub(t)
	gh := connect(t, fake)
	repository, _ := gh.Repository(shop)
	fake.AddIssue(shop, 41, "Add plan model")
	fake.PushCommit(shop, "mobius/41", "Add plan model")
	number := fake.OpenPullRequest(shop, "Add plan model", "mobius/41")
	first := fake.AddReviewComment(shop, number, 0, "owner", "Use price_cents.")
	fake.AddReviewComment(shop, number, first, "mobius-test[bot]", "Done.")
	fake.AddReviewComment(shop, number, first, "owner", "Thanks.")
	fake.AddReviewComment(shop, number, 0, "coderabbitai[bot]", "Rename plan to tier.")
	fake.AddReviewComment(shop, number, 0, "owner", "Split the parser.")
	for range 3 {
		fake.AddReview(shop, number, "owner", "COMMENTED", "")
	}
	fake.AddReview(shop, number, "owner", "APPROVED", "")
	fake.SetGraphQLPageSize(2)

	found, err := repository.PullRequestReviews(t.Context(), []int64{number})

	if err != nil {
		t.Fatal(err)
	}
	reviews, threads := found[number].Reviews, found[number].Threads
	if len(reviews) != 4 || reviews[3].State != "APPROVED" || reviews[3].Author != "owner" || reviews[3].Commit == "" || reviews[3].SubmittedAt.IsZero() {
		t.Errorf("reviews = %+v", reviews)
	}
	if len(threads) != 3 || len(threads[0].Comments) != 3 || threads[0].Comment != first || threads[1].Authors[0] != "coderabbitai[bot]" {
		t.Errorf("threads = %+v", threads)
	}
	if threads[0].Path != "src/plan.rs" || threads[0].Line != 12 || threads[0].Comments[2].Body != "Thanks." {
		t.Errorf("thread = %+v", threads[0])
	}
	if got := fake.PullRequestReads(); got != 1 {
		t.Errorf("reads of pull requests = %d", got)
	}
}

func TestMergePullRequestTellsAMergeApartFromAChangedHeadAndARefusal(t *testing.T) {
	const shop = "owner/shop"
	fake := testkit.NewFakeGitHub(t)
	gh := connect(t, fake)
	repository, _ := gh.Repository(shop)
	fake.AddIssue(shop, 41, "Add plan model")
	fake.PushCommit(shop, "mobius/41", "Add plan model")
	number := fake.OpenPullRequest(shop, "Add plan model", "mobius/41")
	sha := testkit.Git(t, fake.Remote(shop), "rev-parse", "mobius/41")

	merged, reason, err := repository.MergePullRequest(t.Context(), number, "0000000000000000000000000000000000000000")
	if err != nil || merged || reason != "" {
		t.Errorf("changed head: %v, %q, %v", merged, reason, err)
	}

	fake.RefuseMerge(shop, number, "Required status check is expected.")
	merged, reason, err = repository.MergePullRequest(t.Context(), number, sha)
	if err != nil || merged || reason != "Required status check is expected." {
		t.Errorf("refusal: %v, %q, %v", merged, reason, err)
	}

	fake.RefuseMerge(shop, number, "")
	merged, reason, err = repository.MergePullRequest(t.Context(), number, sha)
	if err != nil || !merged || reason != "" {
		t.Errorf("merge: %v, %q, %v", merged, reason, err)
	}
}
