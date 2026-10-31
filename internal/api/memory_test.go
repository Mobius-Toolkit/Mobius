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
	Reason string `json:"reason"`
	Text   string `json:"text"`
	Before string `json:"before"`

	Revertible    bool   `json:"revertible"`
	RevertProblem string `json:"revert_problem"`
}

func memoryURL(server *testserver.Server, repository string) string {
	return server.URL + "/api/repositories/" + repository + "/memory"
}

func memoryFileURL(server *testserver.Server, repository, file string) string {
	return memoryURL(server, repository) + "/" + file
}

type memoryFile struct {
	File  string `json:"file"`
	Lines int    `json:"lines"`
}

func listMemoryFiles(t *testing.T, server *testserver.Server, repository string) []memoryFile {
	t.Helper()
	var body struct {
		Data []memoryFile `json:"data"`
	}
	if reply := call(t, server.Client, http.MethodGet, memoryURL(server, repository), "", &body); reply.StatusCode != http.StatusOK {
		t.Fatalf("list memory files: status %d", reply.StatusCode)
	}
	return body.Data
}

type memoryPage struct {
	Versions []memoryVersion `json:"versions"`
	Older    bool            `json:"older"`
}

func listMemoryPage(t *testing.T, server *testserver.Server, repository, file string, before int64) memoryPage {
	t.Helper()
	var body struct {
		Data memoryPage `json:"data"`
	}
	address := memoryFileURL(server, repository, file)
	if before != 0 {
		address += "?before=" + strconv.FormatInt(before, 10)
	}
	if reply := call(t, server.Client, http.MethodGet, address, "", &body); reply.StatusCode != http.StatusOK {
		t.Fatalf("list memory: status %d", reply.StatusCode)
	}
	return body.Data
}

func listMemory(t *testing.T, server *testserver.Server, repository string) []memoryVersion {
	t.Helper()
	return listMemoryPage(t, server, repository, "shared", 0).Versions
}

func saveMemoryFile(t *testing.T, server *testserver.Server, file string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if err := server.Engine.SaveMemory(t.Context(), shop, file, "curator", "", text); err != nil {
			t.Fatal(err)
		}
	}
}

func saveMemory(t *testing.T, server *testserver.Server, texts ...string) {
	t.Helper()
	saveMemoryFile(t, server, "shared", texts...)
}

func revertMemory(t *testing.T, server *testserver.Server, version memoryVersion) result {
	t.Helper()
	return call(t, server.Client, http.MethodPost, memoryFileURL(server, shop, "shared")+"/"+strconv.FormatInt(version.ID, 10)+"/revert", "", nil)
}

func refusedRevert(t *testing.T, server *testserver.Server, version memoryVersion) (result, string) {
	t.Helper()
	var failure struct {
		Error string `json:"error"`
	}
	reply := call(t, server.Client, http.MethodPost, memoryFileURL(server, shop, "shared")+"/"+strconv.FormatInt(version.ID, 10)+"/revert", "", &failure)
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
	if err := server.Engine.SaveMemory(t.Context(), shop, "shared", "curator", "", "Run make fmt.\n"); err != nil {
		t.Fatal(err)
	}
	base := listMemory(t, server, shop)[0].ID
	if reply := call(t, server.Client, http.MethodPut, memoryFileURL(server, shop, "shared"), `{"text": "Run make fmt.\nRun make check.\n", "base_version": `+strconv.FormatInt(base, 10)+`}`, nil); reply.StatusCode != http.StatusNoContent {
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

func TestTheMemoryRouteGivesThe20NewestVersionsAndThePageBeforeAVersion(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	for i := 1; i <= 25; i++ {
		saveMemory(t, server, fmt.Sprintf("Version %d.\n", i))
	}

	first := listMemoryPage(t, server, shop, "shared", 0)
	second := listMemoryPage(t, server, shop, "shared", first.Versions[19].ID)

	if len(first.Versions) != 20 || !first.Older || len(second.Versions) != 5 || second.Older {
		t.Fatalf("pages = %d %t, %d %t", len(first.Versions), first.Older, len(second.Versions), second.Older)
	}
	all := slices.Concat(first.Versions, second.Versions)
	for i, version := range all {
		want := fmt.Sprintf("Version %d.\n", 25-i)
		wantBefore := ""
		if i < 24 {
			wantBefore = fmt.Sprintf("Version %d.\n", 24-i)
		}
		if version.Text != want || version.Before != wantBefore {
			t.Errorf("version %d: text %q, before %q, want %q, %q", i, version.Text, version.Before, want, wantBefore)
		}
	}
}

func TestTheMemoryRouteGivesTheReasonOfEachVersion(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	if err := server.Engine.SaveMemory(t.Context(), shop, "shared", "curator", "Add: three fix rounds repeated the same format error", "Run make fmt.\n"); err != nil {
		t.Fatal(err)
	}
	if err := server.Engine.SaveMemory(t.Context(), shop, "shared", "owner", "", "Run make fmt.\nRun make check.\n"); err != nil {
		t.Fatal(err)
	}
	first := listMemory(t, server, shop)[1]
	if err := server.Engine.RevertMemory(t.Context(), shop, "shared", first.ID); err != nil {
		t.Fatal(err)
	}

	versions := listMemory(t, server, shop)

	var got []string
	for _, version := range versions {
		got = append(got, version.Reason)
	}
	want := []string{"Revert of version " + strconv.FormatInt(first.ID, 10), "", "Add: three fix rounds repeated the same format error"}
	if !slices.Equal(got, want) {
		t.Errorf("reasons = %q, want %q", got, want)
	}
}

func TestTheMemoryRouteGivesNoVersionForARepositoryWithNoMemory(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")

	if got := listMemory(t, server, shop); got == nil || len(got) != 0 {
		t.Errorf("versions = %v", got)
	}
}

func TestTheMemoryRouteGivesEachFileWithItsLineCount(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	saveMemoryFile(t, server, "shared", "One.\nTwo.\n")
	saveMemoryFile(t, server, "implementer", "Only line.\n")

	files := listMemoryFiles(t, server, shop)

	want := []memoryFile{
		{File: "shared", Lines: 2},
		{File: "lead", Lines: 0},
		{File: "implementer", Lines: 1},
		{File: "reviewer", Lines: 0},
		{File: "judge", Lines: 0},
		{File: "researcher", Lines: 0},
		{File: "diagnoser", Lines: 0},
		{File: "curator", Lines: 0},
	}
	if !slices.Equal(files, want) {
		t.Errorf("files = %+v, want %+v", files, want)
	}
}

func TestTheVersionsOfAFileHaveNoVersionOfAnotherFile(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	saveMemoryFile(t, server, "shared", "Shared.\n")
	saveMemoryFile(t, server, "implementer", "First.\n", "Second.\n")

	versions := listMemoryPage(t, server, shop, "implementer", 0).Versions

	want := []string{"curator:Second.\n", "curator:First.\n"}
	if got := texts(versions); !slices.Equal(got, want) {
		t.Errorf("versions = %q, want %q", got, want)
	}
}

func TestTheSaveRouteOfARoleFileChangesOnlyThatFile(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	saveMemoryFile(t, server, "shared", "Shared.\n")
	saveMemoryFile(t, server, "implementer", "Old.\n")
	implementer := listMemoryPage(t, server, shop, "implementer", 0).Versions[0]

	if reply := call(t, server.Client, http.MethodPut, memoryFileURL(server, shop, "implementer"), `{"text": "New.\n", "base_version": `+strconv.FormatInt(implementer.ID, 10)+`}`, nil); reply.StatusCode != http.StatusNoContent {
		t.Fatalf("save: status %d", reply.StatusCode)
	}

	if got := listMemoryPage(t, server, shop, "implementer", 0).Versions[0].Text; got != "New.\n" {
		t.Errorf("implementer text = %q", got)
	}
	if got := listMemory(t, server, shop); len(got) != 1 || got[0].Text != "Shared.\n" {
		t.Errorf("shared versions = %q", texts(got))
	}
}

func TestTheRevertRouteOfAFileGivesNotFoundForAVersionOfAnotherFile(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	saveMemoryFile(t, server, "shared", "Shared.\n")
	saveMemoryFile(t, server, "implementer", "Old.\n")
	shared := listMemory(t, server, shop)[0]

	reply := call(t, server.Client, http.MethodPost, memoryFileURL(server, shop, "implementer")+"/"+strconv.FormatInt(shared.ID, 10)+"/revert", "", nil)

	if reply.StatusCode != http.StatusNotFound {
		t.Errorf("status %d", reply.StatusCode)
	}
	if got := listMemoryPage(t, server, shop, "implementer", 0).Versions; len(got) != 1 || got[0].Text != "Old.\n" {
		t.Errorf("implementer versions = %q", texts(got))
	}
	if got := listMemory(t, server, shop); len(got) != 1 || got[0].Text != "Shared.\n" {
		t.Errorf("shared versions = %q", texts(got))
	}
}

func TestTheSaveRouteRefusesATextOfMoreThan200LinesAndTellsTheLimit(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	lines := func(n int) string { return `{"text": "` + strings.Repeat(`Run make fmt.\n`, n) + `"}` }
	var failure struct {
		Error string `json:"error"`
	}

	long := call(t, server.Client, http.MethodPut, memoryFileURL(server, shop, "shared"), lines(201), &failure)
	limit := call(t, server.Client, http.MethodPut, memoryFileURL(server, shop, "shared"), lines(200), nil)

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

func TestTheSaveRouteRefusesATextAboveTheByteLimit(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	var failure struct {
		Error string `json:"error"`
	}
	body := `{"text": "` + strings.Repeat("A", 16385) + `"}`
	res := call(t, server.Client, http.MethodPut, memoryFileURL(server, shop, "shared"), body, &failure)
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(failure.Error, "16385 bytes") || !strings.Contains(failure.Error, "limit is 16384") {
		t.Errorf("status %d, error %q", res.StatusCode, failure.Error)
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

	stale := call(t, server.Client, http.MethodPut, memoryFileURL(server, shop, "shared"), `{"text": "Mine.\n", "base_version": `+strconv.FormatInt(first.ID, 10)+`}`, &failure)
	none := call(t, server.Client, http.MethodPut, memoryFileURL(server, shop, "shared"), `{"text": "Mine.\n"}`, nil)

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
	if err := server.Engine.SaveMemory(t.Context(), "owner/cafe", "shared", "curator", "", "One.\n"); err != nil {
		t.Fatal(err)
	}
	other := listMemory(t, server, "owner/cafe")[0]

	for _, id := range []int64{other.ID, other.ID + 100} {
		reply := call(t, server.Client, http.MethodPost, memoryFileURL(server, shop, "shared")+"/"+strconv.FormatInt(id, 10)+"/revert", "", nil)
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

	for _, route := range []struct{ method, address string }{
		{http.MethodGet, memoryURL(server, "owner/none")},
		{http.MethodGet, memoryFileURL(server, "owner/none", "shared")},
		{http.MethodPut, memoryFileURL(server, "owner/none", "shared")},
		{http.MethodPost, memoryFileURL(server, "owner/none", "shared") + "/1/revert"},
	} {
		if reply := call(t, server.Client, route.method, route.address, `{"text": "One.\n"}`, nil); reply.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: status %d", route.method, route.address, reply.StatusCode)
		}
	}
}

func TestEachMemoryRouteGivesNotFoundForAnUnknownFile(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")

	for _, route := range []struct{ method, address string }{
		{http.MethodGet, memoryFileURL(server, shop, "nobody")},
		{http.MethodPut, memoryFileURL(server, shop, "nobody")},
		{http.MethodPost, memoryFileURL(server, shop, "nobody") + "/1/revert"},
	} {
		var failure struct {
			Error string `json:"error"`
		}
		reply := call(t, server.Client, route.method, route.address, `{"text": "One.\n"}`, &failure)
		if reply.StatusCode != http.StatusNotFound || failure.Error != "The memory has no file nobody." {
			t.Errorf("%s %s: status %d, error %q", route.method, route.address, reply.StatusCode, failure.Error)
		}
	}
}

func TestEachMemoryRouteNeedsADeviceLogin(t *testing.T) {
	server := startWithApp(t, testkit.NewFakeGitHub(t), "User")
	anonymous := *server.Client
	anonymous.Jar = nil

	for _, route := range []struct{ method, address string }{
		{http.MethodGet, memoryURL(server, shop)},
		{http.MethodGet, memoryFileURL(server, shop, "shared")},
		{http.MethodPut, memoryFileURL(server, shop, "shared")},
		{http.MethodPost, memoryFileURL(server, shop, "shared") + "/1/revert"},
	} {
		if reply := call(t, &anonymous, route.method, route.address, `{"text": "One.\n"}`, nil); reply.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: status %d", route.method, route.address, reply.StatusCode)
		}
	}
}
