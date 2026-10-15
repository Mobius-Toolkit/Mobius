package api_test

import (
	"bufio"
	"net/http"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

type ciStop struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Workflow   string `json:"workflow"`
	RunURL     string `json:"runUrl"`
	Issue      int64  `json:"issue"`
	IssueURL   string `json:"issueUrl"`
}

func getCIStops(t *testing.T, client *http.Client, address string) []ciStop {
	t.Helper()
	var body struct {
		Data []ciStop `json:"data"`
	}
	if reply := call(t, client, http.MethodGet, address+"/api/ci-stops", "", &body); reply.StatusCode != http.StatusOK {
		t.Fatalf("ci stops: status %d", reply.StatusCode)
	}
	return body.Data
}

func TestAHardBreakWithAnIssueGivesOneCIStopAndItsEndSendsTheEventWithAnEmptyList(t *testing.T) {
	github := testkit.NewFakeGitHub(t)
	server := startWithApp(t, github, "User")
	github.AddIssue(shop, 30, "Fix the failed workflow")
	if _, err := server.DB.Exec(`INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, rerun_at, result, issue, started_at) VALUES
		(?, 7, 'ci', 1, 'https://github.com/owner/shop/actions/runs/1', 1, '2026-10-04T10:00:00Z', 'hard', 30, '2026-10-04T10:00:00Z'),
		(?, 8, 'lint', 2, '', 1, '2026-10-04T10:00:00Z', 'hard', 0, '2026-10-04T10:00:00Z'),
		(?, 9, 'docs', 3, '', 1, '2026-10-04T10:00:00Z', 'flaky', 31, '2026-10-04T10:00:00Z')`, shop, shop, shop); err != nil {
		t.Fatal(err)
	}

	want := ciStop{
		Repository: shop,
		Branch:     "main",
		Workflow:   "ci",
		RunURL:     "https://github.com/owner/shop/actions/runs/1",
		Issue:      30,
		IssueURL:   "https://github.com/owner/shop/issues/30",
	}
	if got := getCIStops(t, server.Client, server.URL); len(got) != 1 || got[0] != want {
		t.Fatalf("stops = %+v, want [%+v]", got, want)
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := server.Client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reply.Body.Close() }()
	github.CloseIssue(shop, 30)

	scanner := bufio.NewScanner(reply.Body)
	event := ""
	for scanner.Scan() {
		line := scanner.Text()
		if name, found := strings.CutPrefix(line, "event: "); found {
			event = name
		}
		if data, found := strings.CutPrefix(line, "data: "); found && event == "ciStops" {
			if data != "[]" {
				t.Errorf("ciStops data = %s, want []", data)
			}
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if got := getCIStops(t, server.Client, server.URL); len(got) != 0 {
		t.Errorf("stops after the end = %+v", got)
	}
}
