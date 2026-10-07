package api_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

const shop = "owner/shop"

type labelCheck struct {
	Name   string `json:"name"`
	Color  string `json:"color"`
	Status string `json:"status"`
	Found  string `json:"found"`
}

type permissionCheck struct {
	Name   string `json:"name"`
	Level  string `json:"level"`
	Status string `json:"status"`
	URL    string `json:"url"`
}

type checkup struct {
	Repositories []struct {
		Repository string       `json:"repository"`
		Labels     []labelCheck `json:"labels"`
	} `json:"repositories"`
	Permissions      []permissionCheck `json:"permissions"`
	PermissionsError string            `json:"permissionsError"`
	LabelFix         string            `json:"labelFix"`
}

func getCheckup(t *testing.T, server *testserver.Server, organization string) checkup {
	t.Helper()
	var body struct {
		Data checkup `json:"data"`
	}
	if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/checkup?organization="+organization, "", &body); reply.StatusCode != http.StatusOK {
		t.Fatalf("checkup: status %d", reply.StatusCode)
	}
	return body.Data
}

func fixLabels(t *testing.T, server *testserver.Server, organization string) {
	t.Helper()
	if reply := call(t, server.Client, http.MethodPost, server.URL+"/api/checkup/fix", `{"organization": "`+organization+`"}`, nil); reply.StatusCode != http.StatusNoContent {
		t.Fatalf("fix: status %d", reply.StatusCode)
	}
}

// startWithApp starts a server with an App that is installed on owner/shop, and waits for the first poll of owner/shop.
func startWithApp(t *testing.T, github *testkit.FakeGitHub, accountType string) *testserver.Server {
	t.Helper()
	github.AddAccount("owner", accountType)
	github.AddRepository(shop)
	server := testserver.Start(t, t.TempDir(), github.URL)
	createApp(t, server, "owner")
	testkit.WaitFor(t, func() bool { return len(github.RepositoryLabels(shop)) == len(engine.Labels) })
	return server
}

func status(found checkup, name string) labelCheck {
	i := slices.IndexFunc(found.Repositories[0].Labels, func(label labelCheck) bool { return label.Name == name })
	return found.Repositories[0].Labels[i]
}

func TestTheCheckupShowsTheLabelStatusAndTheFixFixesTheFixableLabels(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	server := startWithApp(t, github, "User")

	if found := getCheckup(t, server, "stranger"); len(found.Repositories) != 0 || len(found.Permissions) != 0 || found.LabelFix != "none" {
		t.Errorf("checkup of another organization = %+v", found)
	}

	// The first poll created the labels, and it fixes them one time in a run of the server.
	for _, label := range engine.Labels {
		github.DeleteRepositoryLabel(shop, label.Name)
	}
	found := getCheckup(t, server, "owner")
	if len(found.Repositories) != 1 || found.Repositories[0].Repository != shop || len(found.Repositories[0].Labels) != len(engine.Labels) {
		t.Fatalf("repositories = %+v", found.Repositories)
	}
	for _, label := range found.Repositories[0].Labels {
		if label.Status != "missing" {
			t.Errorf("label %+v", label)
		}
	}
	if found.LabelFix != "create" {
		t.Errorf("label fix = %q, want create", found.LabelFix)
	}

	fixLabels(t, server, "owner")

	for _, want := range engine.Labels {
		if !slices.Contains(github.RepositoryLabels(shop), testkit.Label(want)) {
			t.Errorf("label %+v is not on GitHub", want)
		}
	}
	if patches := github.LabelPatches(shop); len(patches) != 0 {
		t.Errorf("patches = %v", patches)
	}

	github.DeleteRepositoryLabel(shop, "mobius:ready")
	github.AddRepositoryLabel(shop, "mobius:working", "ededed", "Custom description")
	found = getCheckup(t, server, "owner")
	if got := status(found, "mobius:ready"); got.Status != "missing" {
		t.Errorf("mobius:ready = %+v", got)
	}
	if got := status(found, "mobius:working"); got.Status != "wrong-color" || got.Found != "ededed" {
		t.Errorf("mobius:working = %+v", got)
	}
	if found.LabelFix != "fix" {
		t.Errorf("label fix = %q, want fix", found.LabelFix)
	}

	fixLabels(t, server, "owner")

	labels := github.RepositoryLabels(shop)
	if !slices.Contains(labels, testkit.Label{Name: "mobius:ready", Color: "0E8A16", Description: "Mobius can dispatch this task"}) ||
		!slices.Contains(labels, testkit.Label{Name: "mobius:working", Color: "FBCA04", Description: "Custom description"}) {
		t.Errorf("labels = %+v", labels)
	}
	if patches := github.LabelPatches(shop); !slices.Equal(patches, []string{"mobius:working"}) {
		t.Errorf("patches = %v", patches)
	}
	found = getCheckup(t, server, "owner")
	for _, label := range found.Repositories[0].Labels {
		if label.Status != "present" {
			t.Errorf("label %+v", label)
		}
	}
	if found.LabelFix != "none" {
		t.Errorf("label fix = %q, want none", found.LabelFix)
	}

	// A label in a different case shows its own status, and alone it needs no fix.
	github.AddRepositoryLabel(shop, "Mobius:Autopilot", "1D76DB", "Autopilot")
	found = getCheckup(t, server, "owner")
	if got := status(found, "mobius:autopilot"); got.Status != "wrong-case" || got.Found != "Mobius:Autopilot" {
		t.Errorf("mobius:autopilot = %+v", got)
	}
	if found.LabelFix != "none" {
		t.Errorf("label fix = %q, want none", found.LabelFix)
	}

	fixLabels(t, server, "owner")

	if !slices.ContainsFunc(github.RepositoryLabels(shop), func(label testkit.Label) bool { return label.Name == "Mobius:Autopilot" }) {
		t.Error("the fix changed the label in a different case")
	}
	if patches := github.LabelPatches(shop); !slices.Equal(patches, []string{"mobius:working"}) {
		t.Errorf("patches = %v", patches)
	}
}

func permissionStatus(t *testing.T, server *testserver.Server, name string) permissionCheck {
	t.Helper()
	permissions := getCheckup(t, server, "owner").Permissions
	return permissions[slices.IndexFunc(permissions, func(p permissionCheck) bool { return p.Name == name })]
}

func TestTheCheckupShowsTheStatusOfEachAppPermissionWithThePageThatFixesIt(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	server := startWithApp(t, github, "Organization")

	var names []string
	for _, permission := range getCheckup(t, server, "owner").Permissions {
		names = append(names, permission.Name)
	}
	if want := []string{"issues", "pull_requests", "contents", "checks", "workflows", "actions", "metadata"}; !slices.Equal(names, want) {
		t.Errorf("names = %v", names)
	}
	if got := permissionStatus(t, server, "issues"); got.Status != "present" || got.URL != "" {
		t.Errorf("issues = %+v", got)
	}

	// The App has no workflows permission.
	want := permissionCheck{"workflows", "write", "missing", github.URL + "/organizations/owner/settings/apps/" + testkit.AppSlug + "/permissions"}
	if got := permissionStatus(t, server, "workflows"); got != want {
		t.Errorf("workflows = %+v, want %+v", got, want)
	}

	// The App has the permission, and the installation does not.
	permissions := map[string]string{
		"issues":        "write",
		"pull_requests": "write",
		"contents":      "write",
		"checks":        "write",
		"metadata":      "read",
		"workflows":     "write",
		"actions":       "write",
	}
	github.SetAppPermissions(testkit.AppID, permissions)
	want = permissionCheck{"workflows", "write", "not-accepted", github.URL + "/organizations/owner/settings/installations/1"}
	if got := permissionStatus(t, server, "workflows"); got != want {
		t.Errorf("workflows = %+v, want %+v", got, want)
	}

	// A lower level in the installation is not sufficient.
	github.SetInstallationPermissions(testkit.AppID, map[string]string{"workflows": "read"})
	for _, name := range []string{"workflows", "issues"} {
		if got := permissionStatus(t, server, name); got.Status != "not-accepted" {
			t.Errorf("%s = %+v", name, got)
		}
	}

	// The installation has each permission, and a higher level counts.
	permissions["workflows"] = "admin"
	github.SetInstallationPermissions(testkit.AppID, permissions)
	for _, permission := range getCheckup(t, server, "owner").Permissions {
		if permission.Status != "present" {
			t.Errorf("permission %+v", permission)
		}
	}
}

func TestTheCheckupShowsTheMissingActionsWritePermission(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	server := startWithApp(t, github, "Organization")

	want := permissionCheck{"actions", "write", "missing", github.URL + "/organizations/owner/settings/apps/" + testkit.AppSlug + "/permissions"}
	if got := permissionStatus(t, server, "actions"); got != want {
		t.Errorf("actions = %+v, want %+v", got, want)
	}
}

func TestTheCheckupShowsTheLabelsWhenTheCheckOfTheAppPermissionsFails(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	server := startWithApp(t, github, "Organization")
	github.FailInstallations(testkit.AppID)

	found := getCheckup(t, server, "owner")

	if found.PermissionsError == "" || len(found.Permissions) != 0 {
		t.Errorf("permissions = %+v, error %q", found.Permissions, found.PermissionsError)
	}
	if len(found.Repositories) != 1 || len(found.Repositories[0].Labels) == 0 {
		t.Errorf("repositories = %+v", found.Repositories)
	}
}

func TestTheCheckupNeedsAnOrganization(t *testing.T) {
	server := testserver.Start(t, t.TempDir(), testkit.NewFakeGitHub(t).URL)

	for _, reply := range []result{
		call(t, server.Client, http.MethodGet, server.URL+"/api/checkup", "", nil),
		call(t, server.Client, http.MethodPost, server.URL+"/api/checkup/fix", `{}`, nil),
	} {
		if reply.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d", reply.StatusCode)
		}
	}
}

func TestTheToolsCheckGivesEachProgramWithItsStatus(t *testing.T) {
	t.Setenv("CLAUDE_CODE_EXECUTABLE", "")
	server := testserver.Start(t, t.TempDir(), testkit.NewFakeGitHub(t).URL)

	var body struct {
		Data []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if reply := call(t, server.Client, http.MethodGet, server.URL+"/api/checkup/tools", "", &body); reply.StatusCode != http.StatusOK {
		t.Fatalf("tools: status %d", reply.StatusCode)
	}

	var names []string
	for _, tool := range body.Data {
		names = append(names, tool.Name)
	}
	if want := []string{"claude-agent-acp", "Claude Code CLI", "agy_acp_server", "devin", "git", "curl", "tar"}; !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	// The test PATH has no Harness command.
	if got := body.Data[0].Status; got != "not-found" {
		t.Errorf("claude-agent-acp status = %q", got)
	}
	if got := body.Data[1].Status; got != "no-version" {
		t.Errorf("Claude Code CLI status = %q", got)
	}
}
