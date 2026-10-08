package testkit

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The Apps of the fake GitHub. Both Apps sign with AppPrivateKey.
const (
	AppID           int64 = 7
	AppSlug               = "mobius-test"
	AppClientID           = "Iv23test"
	AppClientSecret       = "client-secret"
	SecondAppID     int64 = 8
	SecondAppSlug         = "mobius-second"
)

// AppPrivateKey is a test key with no use outside the tests.
//
//go:embed testdata/app_private_key.pem
var AppPrivateKey string

type githubApp struct {
	id           int64
	slug         string
	clientID     string
	clientSecret string
}

// The manifest conversions create the Apps in this order. The installation id of an App is its index plus 1.
var apps = []githubApp{
	{AppID, AppSlug, AppClientID, AppClientSecret},
	{SecondAppID, SecondAppSlug, "Iv23second", "second-client-secret"},
}

const botUserID = 41898282

// The id of an issue is its number plus this offset, so a number in place of an id finds no issue.
const issueIDOffset = 100_000

// The permissions of an App or an installation with no SetAppPermissions or SetInstallationPermissions.
var defaultPermissions = map[string]string{
	"issues":        "write",
	"pull_requests": "write",
	"contents":      "write",
	"checks":        "write",
	"metadata":      "read",
}

// FakeGitHub is an in-memory GitHub API on an httptest server.
//
// To add an endpoint, add its route in routes, its handler as a method next to the
// handlers of the same area, and its state as a field of FakeGitHub.
type FakeGitHub struct {
	URL string
	key *rsa.PublicKey
	t   testing.TB
	// remotes holds the bare git repository of each repository.
	remotes string

	mu sync.Mutex
	// Each write is one second after the last, so a `since` filter compares exactly. The unit is seconds after the Unix epoch.
	clock                   int64
	accountTypes            map[string]string
	manifestCodes           map[string]bool
	manifestCodesGiven      int
	appsCreated             int
	secondAppAccounts       map[string]bool
	failedApps              map[int64]bool
	appPermissions          map[int64]map[string]string
	installationPermissions map[int64]map[string]string
	userCodes               map[string]grant
	refreshTokens           map[string]grant
	tokens                  map[string]token
	userTokensGiven         int
	installationTokensGiven int
	installationTokenLife   time.Duration
	repositories            []string
	issues                  map[issueKey]*issue
	// commentsAfterList holds the comments that the next issue list adds after it builds its page.
	commentsAfterList []listedComment
	// The comment ids of all issues are different, as on GitHub.
	lastCommentID    int64
	repositoryLabels map[labelKey]Label
	labelPatches     []labelKey
	notModified      int
	// The numbers of reads of a comment list of one issue or pull request, and of a comment list of a repository.
	singleCommentReads, repositoryCommentReads int
	failedCloses                               map[issueKey]bool
	failedSubIssues                            map[issueKey]bool
	// The ids of the first comments of the resolved review threads.
	resolvedThreads map[int64]bool
	latestRelease   *releaseJSON
	comparedCommits []string
	holds           map[issueKey]*hold
	threadHolds     map[issueKey]*hold
	issueHolds      map[issueKey]*hold
	pullRequests    []pullRequest
	// createdAt holds the creation time of each pull request, in seconds after the Unix epoch.
	createdAt map[issueKey]int64
	behind    map[issueKey]bool
	// unknownMergeable holds the pull requests whose mergeable GitHub still calculates.
	unknownMergeable map[issueKey]bool
	// The id of a check run is its index plus 1.
	checkRuns    []checkRun
	annotations  map[int64][]annotationJSON
	checkRunApps map[int64]string
	jobLogs      map[int64]string
	// The id of a workflow run is its index plus 1.
	workflowRuns []workflowRun
}

// A grant is a user code or a refresh token: the login of the user and the App index.
type grant struct {
	login string
	app   int
}

type token struct {
	// The login of the user, or the bot login of the App for an installation token.
	login        string
	app          int
	installation bool
	expires      time.Time
}

// NewFakeGitHub starts a fake GitHub with no accounts and no repositories.
func NewFakeGitHub(t testing.TB) *FakeGitHub {
	t.Helper()
	block, _ := pem.Decode([]byte(AppPrivateKey))
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	g := &FakeGitHub{
		key:                     &key.PublicKey,
		t:                       t,
		remotes:                 t.TempDir(),
		clock:                   time.Now().Unix(),
		accountTypes:            map[string]string{},
		manifestCodes:           map[string]bool{},
		secondAppAccounts:       map[string]bool{},
		failedApps:              map[int64]bool{},
		appPermissions:          map[int64]map[string]string{},
		installationPermissions: map[int64]map[string]string{},
		userCodes:               map[string]grant{},
		refreshTokens:           map[string]grant{},
		tokens:                  map[string]token{},
		installationTokenLife:   time.Hour,
		issues:                  map[issueKey]*issue{},
		repositoryLabels:        map[labelKey]Label{},
		resolvedThreads:         map[int64]bool{},
		failedCloses:            map[issueKey]bool{},
		failedSubIssues:         map[issueKey]bool{},
		holds:                   map[issueKey]*hold{},
		threadHolds:             map[issueKey]*hold{},
		issueHolds:              map[issueKey]*hold{},
		createdAt:               map[issueKey]int64{},
		behind:                  map[issueKey]bool{},
		unknownMergeable:        map[issueKey]bool{},
		annotations:             map[int64][]annotationJSON{},
		checkRunApps:            map[int64]string{},
		jobLogs:                 map[int64]string{},
	}
	server := httptest.NewServer(g.routes())
	t.Cleanup(server.Close)
	g.URL = server.URL
	return g
}

func (g *FakeGitHub) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{name}", g.getAccount)
	mux.HandleFunc("POST /settings/apps/new", g.newApp)
	mux.HandleFunc("POST /organizations/{org}/settings/apps/new", g.newApp)
	mux.HandleFunc("POST /app-manifests/{code}/conversions", g.convertManifest)
	mux.HandleFunc("POST /login/oauth/access_token", g.exchangeCode)
	mux.HandleFunc("GET /user", g.getUser)
	mux.HandleFunc("GET /app", g.getApp)
	mux.HandleFunc("GET /app/installations", g.listInstallations)
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", g.createInstallationToken)
	mux.HandleFunc("GET /installation/repositories", g.listInstallationRepositories)
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues", g.withToken(g.listIssues))
	mux.HandleFunc("POST /repos/{owner}/{repo}/issues", g.withToken(g.createIssue))
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues/{number}", g.withToken(g.getIssue))
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/issues/{number}", g.withToken(g.closeIssue))
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues/{number}/sub_issues", g.withToken(g.subIssues))
	mux.HandleFunc("POST /repos/{owner}/{repo}/issues/{number}/sub_issues", g.withToken(g.addSubIssue))
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues/{number}/parent", g.withToken(g.parent))
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues/{number}/dependencies/blocked_by", g.withToken(g.blockedBy))
	mux.HandleFunc("POST /repos/{owner}/{repo}/issues/{number}/dependencies/blocked_by", g.withToken(g.addBlockedBy))
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues/{number}/events", g.withToken(g.issueEvents))
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues/comments", g.withToken(g.repositoryIssueComments))
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues/{number}/comments", g.withToken(g.issueComments))
	mux.HandleFunc("POST /repos/{owner}/{repo}/issues/{number}/comments", g.withToken(g.addComment))
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/issues/comments/{id}", g.withToken(g.updateComment))
	mux.HandleFunc("POST /repos/{owner}/{repo}/issues/{number}/labels", g.withToken(g.addLabels))
	mux.HandleFunc("DELETE /repos/{owner}/{repo}/issues/{number}/labels/{name}", g.withToken(g.removeLabel))
	mux.HandleFunc("GET /repos/{owner}/{repo}/labels", g.withToken(g.listRepositoryLabels))
	mux.HandleFunc("POST /repos/{owner}/{repo}/labels", g.withToken(g.createRepositoryLabel))
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/labels/{name}", g.withToken(g.updateRepositoryLabel))
	mux.HandleFunc("POST /repos/{owner}/{repo}/pulls", g.withToken(g.createPullRequest))
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls/{number}", g.withToken(g.getPullRequest))
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/pulls/{number}", g.withToken(g.closeIssue))
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls/{number}/reviews", g.withToken(g.reviews))
	mux.HandleFunc("POST /repos/{owner}/{repo}/pulls/{number}/reviews", g.withToken(g.submitReview))
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls/comments", g.withToken(g.repositoryReviewComments))
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls/{number}/comments", g.withToken(g.reviewComments))
	mux.HandleFunc("POST /repos/{owner}/{repo}/pulls/{number}/comments", g.withToken(g.replyToReviewComment))
	mux.HandleFunc("POST /graphql", g.withToken(g.graphql))
	mux.HandleFunc("POST /repos/{owner}/{repo}/check-runs", g.withToken(g.createCheckRun))
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/check-runs/{id}", g.withToken(g.updateCheckRun))
	mux.HandleFunc("GET /repos/{owner}/{repo}/check-runs/{id}/annotations", g.withToken(g.checkRunAnnotations))
	mux.HandleFunc("GET /repos/{owner}/{repo}/commits/{sha}/check-runs", g.withToken(g.commitCheckRuns))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/runs", g.withToken(g.listWorkflowRuns))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/jobs/{id}/logs", g.withToken(g.jobLogLink))
	mux.HandleFunc("GET /job-logs/{id}", g.jobLog)
	mux.HandleFunc("GET /repos/{owner}/{repo}/releases/latest", g.getLatestRelease)
	mux.HandleFunc("GET /repos/{owner}/{repo}/compare/{basehead}", g.compare)
	mux.HandleFunc("GET /{owner}/{repo}/releases/download/{tag}/{name}", g.downloadReleaseFile)
	return mux
}

// Now gives the time of the last write.
func (g *FakeGitHub) Now() time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	return time.Unix(g.clock, 0)
}

// tick gives the time of a new write.
func (g *FakeGitHub) tick() int64 {
	g.clock++
	return g.clock
}

func timestamp(seconds int64) string {
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func message(w http.ResponseWriter, status int, text string) {
	writeJSON(w, status, map[string]string{"message": text})
}

func notFound(w http.ResponseWriter) {
	message(w, http.StatusNotFound, "Not Found")
}

func badCredentials(w http.ResponseWriter) {
	message(w, http.StatusUnauthorized, "Bad credentials")
}

// decode refuses a body with an unknown field, so a request with a field that the fake does not know fails.
func decode(w http.ResponseWriter, r *http.Request, body any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		message(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

// credential gives the token of the Authorization header: ghinstallation sends "token", the other clients send "Bearer".
func credential(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if value, ok := strings.CutPrefix(header, "token "); ok {
		return value
	}
	return strings.TrimPrefix(header, "Bearer ")
}

// validToken gives the installation or user token of r, when GitHub accepts it.
func (g *FakeGitHub) validToken(r *http.Request) (token, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	found, ok := g.tokens[credential(r)]
	return found, ok && time.Now().Before(found.expires)
}

func (g *FakeGitHub) withToken(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := g.validToken(r); !ok {
			badCredentials(w)
			return
		}
		handler(w, r)
	}
}

// signer gives the App index of the JSON Web Token of r. GitHub accepts the token when
// the App key signed it with RS256, its `exp` is in the future, and its `iss` is the App id.
func (g *FakeGitHub) signer(r *http.Request) (int, bool) {
	parts := strings.Split(credential(r), ".")
	if len(parts) != 3 {
		return 0, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return 0, false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(g.key, crypto.SHA256, digest[:], signature) != nil {
		return 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, false
	}
	var claims struct {
		Issuer  json.Number `json:"iss"`
		Expires int64       `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || time.Now().Unix() >= claims.Expires {
		return 0, false
	}
	for index, app := range apps {
		if string(claims.Issuer) == strconv.FormatInt(app.id, 10) {
			return index, true
		}
	}
	return 0, false
}

// page gives the page of items that the `page` and `per_page` parameters select, and
// sets the Link header of the next page, as GitHub does.
func page[T any](w http.ResponseWriter, r *http.Request, items []T) []T {
	query := r.URL.Query()
	size, err := strconv.Atoi(query.Get("per_page"))
	if err != nil {
		size = 30
	}
	number, err := strconv.Atoi(query.Get("page"))
	if err != nil {
		number = 1
	}
	start := min((number-1)*size, len(items))
	end := min(start+size, len(items))
	if end < len(items) {
		query.Set("page", strconv.Itoa(number+1))
		query.Set("per_page", strconv.Itoa(size))
		w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?%s>; rel="next"`, r.Host, r.URL.Path, query.Encode()))
	}
	return append([]T{}, items[start:end]...)
}
