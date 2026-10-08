package api_test

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

type memoryVersion struct {
	ID     int64  `json:"id"`
	Time   string `json:"time"`
	Author string `json:"author"`
	Text   string `json:"text"`

	Revertible    bool   `json:"revertible"`
	RevertProblem string `json:"revert_problem"`
}

func memoryURL(server *testserver.Server, repository string) string {
	return server.URL + "/api/repositories/" + repository + "/memory"
}

func listMemory(t *testing.T, server *testserver.Server, repository string) []memoryVersion {
	t.Helper()
	var body struct {
		Data []memoryVersion `json:"data"`
	}
	if reply := call(t, server.Client, http.MethodGet, memoryURL(server, repository), "", &body); reply.StatusCode != http.StatusOK {
		t.Fatalf("list memory: status %d", reply.StatusCode)
	}
	return body.Data
}

func saveMemory(t *testing.T, server *testserver.Server, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if err := server.Engine.SaveMemory(t.Context(), shop, "curator", "", text); err != nil {
			t.Fatal(err)
		}
	}
}

func revertMemory(t *testing.T, server *testserver.Server, version memoryVersion) result {
	t.Helper()
	return call(t, server.Client, http.MethodPost, memoryURL(server, shop)+"/"+strconv.FormatInt(version.ID, 10)+"/revert", "", nil)
}

func refusedRevert(t *testing.T, server *testserver.Server, version memoryVersion) (result, string) {
	t.Helper()
	var failure struct {
		Error string `json:"error"`
	}
	reply := call(t, server.Client, http.MethodPost, memoryURL(server, shop)+"/"+strconv.FormatInt(version.ID, 10)+"/revert", "", &failure)
	return reply, failure.Error
}

func currentMemory(t *testing.T, server *testserver.Server) string {
	t.Helper()
	return listMemory(t, server, shop)[0].Text
}

func texts(versions []memoryVersion) []string {
	found := []string{}
	for _, version := range versions {
		found = append(found, version.Author+":"+version.Text)
	}
	return found
}

func TestTheMemoryRouteGivesTheVersionsNewestFirst(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	if err := server.Engine.SaveMemory(t.Context(), shop, "curator", "", "Run make fmt.\n"); err != nil {
		t.Fatal(err)
	}
	base := listMemory(t, server, shop)[0].ID
	if reply := call(t, server.Client, http.MethodPut, memoryURL(server, shop), `{"text": "Run make fmt.\nRun make check.\n", "base_version": `+strconv.FormatInt(base, 10)+`}`, nil); reply.StatusCode != http.StatusNoContent {
		t.Fatalf("save: status %d", reply.StatusCode)
	}

	versions := listMemory(t, server, shop)

	want := []string{"owner:Run make fmt.\nRun make check.\n", "curator:Run make fmt.\n"}
	if got := texts(versions); !slices.Equal(got, want) {
		t.Errorf("versions = %q, want %q", got, want)
	}
	if versions[0].ID <= versions[1].ID || versions[0].Time == "" {
		t.Errorf("versions = %+v", versions)
	}
}

func TestTheMemoryRouteGivesNoVersionForARepositoryWithNoMemory(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")

	if got := listMemory(t, server, shop); got == nil || len(got) != 0 {
		t.Errorf("versions = %v", got)
	}
}

func TestTheSaveRouteRefusesATextOfMoreThan200LinesAndTellsTheLimit(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	lines := func(n int) string { return `{"text": "` + strings.Repeat(`Run make fmt.\n`, n) + `"}` }
	var failure struct {
		Error string `json:"error"`
	}

	long := call(t, server.Client, http.MethodPut, memoryURL(server, shop), lines(201), &failure)
	limit := call(t, server.Client, http.MethodPut, memoryURL(server, shop), lines(200), nil)

	if long.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(failure.Error, "201 lines") || !strings.Contains(failure.Error, "maximum is 200") {
		t.Errorf("long text: status %d, error %q", long.StatusCode, failure.Error)
	}
	if limit.StatusCode != http.StatusNoContent {
		t.Errorf("200 lines: status %d", limit.StatusCode)
	}
	if versions := listMemory(t, server, shop); len(versions) != 1 || strings.Count(versions[0].Text, "\n") != 200 {
		t.Errorf("versions = %d", len(versions))
	}
}

func TestTheRevertRouteUndoesOnlyTheChangeOfTheVersionAndKeepsTheLaterChanges(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	saveMemory(t, server, "One.\nTwo.\n", "One.\nTwo.\nThree.\n", "One.\nTwo.\nThree.\nFour.\n")
	versions := listMemory(t, server, shop)

	reply := revertMemory(t, server, versions[1])

	if reply.StatusCode != http.StatusNoContent {
		t.Fatalf("revert: status %d", reply.StatusCode)
	}
	got := listMemory(t, server, shop)
	if len(got) != 4 || got[0].Author != "owner" || got[0].Text != "One.\nTwo.\nFour.\n" {
		t.Errorf("versions = %q", texts(got))
	}
	var reason string
	if err := server.DB.QueryRow("SELECT reason FROM memory_versions ORDER BY id DESC LIMIT 1").Scan(&reason); err != nil || reason != fmt.Sprintf("Revert of version %d", versions[1].ID) {
		t.Errorf("reason = %q, %v", reason, err)
	}
}

func TestTheRevertRouteOfTheFirstVersionSavesAnEmptyTextWhenNothingElseChanged(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	saveMemory(t, server, "One.\n")
	first := listMemory(t, server, shop)[0]

	reply := revertMemory(t, server, first)

	got := listMemory(t, server, shop)
	if reply.StatusCode != http.StatusNoContent || len(got) != 2 || got[0].Author != "owner" || got[0].Text != "" {
		t.Errorf("status %d, versions = %q", reply.StatusCode, texts(got))
	}
}

func TestTheRevertRouteOfAVersionThatRemovedLinesPutsTheLinesBackAtTheirPlace(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	saveMemory(t, server, "One.\nTwo.\nThree.\n", "One.\nThree.\n", "One.\nThree.\nFour.\n")
	versions := listMemory(t, server, shop)

	reply := revertMemory(t, server, versions[1])

	if reply.StatusCode != http.StatusNoContent {
		t.Fatalf("revert: status %d", reply.StatusCode)
	}
	if got := currentMemory(t, server); got != "One.\nTwo.\nThree.\nFour.\n" {
		t.Errorf("text = %q", got)
	}
}

func TestTheRevertRouteGivesConflictWhenALaterVersionChangedTheSameBlock(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	saveMemory(t, server, "One.\nTwo.\nThree.\n", "One.\nZwei.\nThree.\n", "One.\nDeux.\nThree.\n")
	versions := listMemory(t, server, shop)

	reply, problem := refusedRevert(t, server, versions[1])

	if reply.StatusCode != http.StatusConflict || problem != "A later version changed this part. Edit the file." {
		t.Errorf("status %d, error %q", reply.StatusCode, problem)
	}
	if got := listMemory(t, server, shop); len(got) != 3 {
		t.Errorf("versions = %q", texts(got))
	}
	if versions[1].Revertible || versions[1].RevertProblem != problem {
		t.Errorf("version = %+v", versions[1])
	}
	if !versions[0].Revertible || versions[0].RevertProblem != "" {
		t.Errorf("newest version = %+v", versions[0])
	}
}

func TestTheRevertRouteGivesConflictWhenTheBlockOccursTwice(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	saveMemory(t, server, "One.\nTwo.\n", "One.\nTwo.\nOne.\nTwo.\n")
	versions := listMemory(t, server, shop)

	reply, problem := refusedRevert(t, server, versions[1])

	if reply.StatusCode != http.StatusConflict || problem != "A later version changed this part. Edit the file." {
		t.Errorf("status %d, error %q", reply.StatusCode, problem)
	}
}

func TestTheRevertRouteGivesConflictWhenNothingChanges(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	saveMemory(t, server, "One.\n")
	if _, err := server.DB.Exec("INSERT INTO memory_versions (repository, time, author, text) SELECT repository, time, author, text FROM memory_versions"); err != nil {
		t.Fatal(err)
	}
	versions := listMemory(t, server, shop)

	reply, problem := refusedRevert(t, server, versions[0])

	if reply.StatusCode != http.StatusConflict || problem != "The revert changes nothing." {
		t.Errorf("status %d, error %q", reply.StatusCode, problem)
	}
	if versions[0].Revertible || versions[0].RevertProblem != problem {
		t.Errorf("version = %+v", versions[0])
	}
}

func TestTheRevertRouteGivesConflictWhenTheResultHasMoreThan200Lines(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	filler := strings.Repeat("Filler.\n", 198)
	saveMemory(t, server, "One.\nTwo.\n"+filler, "One.\n"+filler, "One.\n"+filler+"Three.\n")
	versions := listMemory(t, server, shop)

	reply, problem := refusedRevert(t, server, versions[1])

	if reply.StatusCode != http.StatusConflict || !strings.Contains(problem, "201 lines") || !strings.Contains(problem, "maximum is 200") {
		t.Errorf("status %d, error %q", reply.StatusCode, problem)
	}
}

func TestTheSaveRouteGivesConflictWhenANewerVersionExistsThanTheVersionOfTheEdit(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	saveMemory(t, server, "One.\n")
	first := listMemory(t, server, shop)[0]
	saveMemory(t, server, "One.\nTwo.\n")
	var failure struct {
		Error string `json:"error"`
	}

	stale := call(t, server.Client, http.MethodPut, memoryURL(server, shop), `{"text": "Mine.\n", "base_version": `+strconv.FormatInt(first.ID, 10)+`}`, &failure)
	none := call(t, server.Client, http.MethodPut, memoryURL(server, shop), `{"text": "Mine.\n"}`, nil)

	if stale.StatusCode != http.StatusConflict || failure.Error != "The memory file changed after the start of your edit. Copy your text, then load the file again." {
		t.Errorf("stale edit: status %d, error %q", stale.StatusCode, failure.Error)
	}
	if none.StatusCode != http.StatusConflict {
		t.Errorf("edit with no base version: status %d", none.StatusCode)
	}
	if got := currentMemory(t, server); got != "One.\nTwo.\n" {
		t.Errorf("text = %q", got)
	}
}

func TestTheRevertRouteGivesNotFoundForAnUnknownVersionAndForAVersionOfAnotherRepository(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	github.AddRepository("owner/cafe")
	server := startWithApp(t, github, "User")
	testkit.WaitFor(t, func() bool { return len(github.RepositoryLabels("owner/cafe")) > 0 })
	if err := server.Engine.SaveMemory(t.Context(), "owner/cafe", "curator", "", "One.\n"); err != nil {
		t.Fatal(err)
	}
	other := listMemory(t, server, "owner/cafe")[0]

	for _, id := range []int64{other.ID, other.ID + 100} {
		reply := call(t, server.Client, http.MethodPost, memoryURL(server, shop)+"/"+strconv.FormatInt(id, 10)+"/revert", "", nil)
		if reply.StatusCode != http.StatusNotFound {
			t.Errorf("version %d: status %d", id, reply.StatusCode)
		}
	}
	if got := listMemory(t, server, "owner/cafe"); len(got) != 1 {
		t.Errorf("versions = %q", texts(got))
	}
}

func TestEachMemoryRouteGivesNotFoundForAnUnknownRepository(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")

	for method, address := range map[string]string{
		http.MethodGet:  memoryURL(server, "owner/none"),
		http.MethodPut:  memoryURL(server, "owner/none"),
		http.MethodPost: memoryURL(server, "owner/none") + "/1/revert",
	} {
		if reply := call(t, server.Client, method, address, `{"text": "One.\n"}`, nil); reply.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: status %d", method, address, reply.StatusCode)
		}
	}
}

func TestEachMemoryRouteNeedsADeviceLogin(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	anonymous := *server.Client
	anonymous.Jar = nil

	for method, address := range map[string]string{
		http.MethodGet:  memoryURL(server, shop),
		http.MethodPut:  memoryURL(server, shop),
		http.MethodPost: memoryURL(server, shop) + "/1/revert",
	} {
		if reply := call(t, &anonymous, method, address, `{"text": "One.\n"}`, nil); reply.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: status %d", method, address, reply.StatusCode)
		}
	}
}
