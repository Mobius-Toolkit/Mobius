package engine_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/Mobius-Toolkit/mobius-go/internal/engine"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit"
	"github.com/Mobius-Toolkit/mobius-go/internal/testkit/testserver"
)

// upgrade starts an upgrade through the API, and gives the status and the error text of the response.
func upgrade(t *testing.T, server *testserver.Server) (int, string) {
	t.Helper()
	response, err := server.Client.Post(server.URL+"/api/upgrade", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body.Error
}

// upgradeFailure gives the error of the last upgrade from the API.
func upgradeFailure(t *testing.T, server *testserver.Server) string {
	t.Helper()
	response, err := server.Client.Get(server.URL + "/api/upgrade")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body struct {
		Data struct {
			Failure string `json:"failure"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Data.Failure
}

func TestAnUpgradeOfALocalBuildFails(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	fake.SetLatestRelease("v0.3.0")

	status, text := upgrade(t, server)

	if status != http.StatusConflict || text != "This Mobius build is not a release." {
		t.Errorf("upgrade = %d %q", status, text)
	}
	if failure := upgradeFailure(t, server); failure != text {
		t.Errorf("failure = %q", failure)
	}
}

func TestAnUpgradeToTheNewestReleaseFailsWithNoDrain(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	fake.SetLatestRelease("v0.2.0", "mobius-aarch64-apple-darwin.tar.gz", "mobius-x86_64-unknown-linux-gnu.tar.gz")
	engine.Release = "v0.2.0"
	defer func() { engine.Release = "" }()

	status, text := upgrade(t, server)

	if status != http.StatusConflict || text != "v0.2.0 is the newest release." {
		t.Errorf("upgrade = %d %q", status, text)
	}
	if got := server.Engine.Draining(); got.On {
		t.Errorf("drain = %+v", got)
	}
}

// getData sends a GET request to path of the API, and decodes the data of the response into data. It gives the status and
// the error text of the response.
func getData(t *testing.T, server *testserver.Server, path string, data any) (int, string) {
	t.Helper()
	response, err := server.Client.Get(server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body := struct {
		Data  any    `json:"data"`
		Error string `json:"error"`
	}{Data: data}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, body.Error
}

func newRelease(t *testing.T, server *testserver.Server) string {
	t.Helper()
	var release struct {
		Version string `json:"version"`
	}
	if status, text := getData(t, server, "/api/release", &release); status != http.StatusOK {
		t.Fatalf("release = %d %q", status, text)
	}
	return release.Version
}

func TestTheReleaseIsTheNewestReleaseWhenItIsNewerThanThisProgram(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	fake.SetLatestRelease("v0.2.0")
	defer func() { engine.Release = "" }()

	for _, release := range []struct{ current, want string }{{"", ""}, {"v0.1.4", "v0.2.0"}, {"v0.2.0", ""}} {
		engine.Release = release.current
		if got := newRelease(t, server); got != release.want {
			t.Errorf("release of %q = %q, want %q", release.current, got, release.want)
		}
	}
}

func TestTheReleaseChangesGiveTheFirstLineOfEachCommitTheNewestFirst(t *testing.T) {
	fake := testkit.NewFakeGitHub(t)
	server, _ := connect(t, fake, "")
	fake.SetLatestRelease("v0.2.0")
	fake.SetComparedCommits(
		"Check for a new release each hour (#316)\n\nThe check runs once each hour.",
		"Show the release changes in a modal before the upgrade (#320)",
	)

	var changes []string
	if status, text := getData(t, server, "/api/release/changes", &changes); status != http.StatusConflict || text != "This Mobius build is not a release." {
		t.Errorf("changes of a local build = %d %q", status, text)
	}
	engine.Release = "v0.1.4"
	defer func() { engine.Release = "" }()
	if status, text := getData(t, server, "/api/release/changes", &changes); status != http.StatusOK {
		t.Fatalf("changes = %d %q", status, text)
	}

	want := []string{"Show the release changes in a modal before the upgrade (#320)", "Check for a new release each hour (#316)"}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("changes = %q", changes)
	}
}
