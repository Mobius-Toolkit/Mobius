package engine_test

import (
	"encoding/json"
	"net/http"
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
