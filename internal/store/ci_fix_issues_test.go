package store

import (
	"slices"
	"testing"
)

func TestTheCIFixIssuesAreTheIssuesOfEachCIBreakAndOfEachCIFailureRecordWithAnIssue(t *testing.T) {
	db, queries := openQueries(t)
	ctx := t.Context()
	statements := []string{
		// An open hard break, an ended hard break, a flaky break and a break with an issue that is not below a Workstream yet.
		`INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, result, issue, started_at)
		 VALUES ('owner/shop', 1, 'ci', 1, '', 1, 'hard', 11, '2027-01-01T00:00:00Z')`,
		`INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, result, ended_run_id, ended_at, issue, started_at)
		 VALUES ('owner/shop', 2, 'ci', 2, '', 1, 'hard', 3, '2027-01-02T00:00:00Z', 12, '2027-01-01T00:00:00Z')`,
		`INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, result, ended_at, issue, started_at)
		 VALUES ('owner/shop', 3, 'ci', 4, '', 1, 'flaky', '2027-01-01T00:00:00Z', 13, '2027-01-01T00:00:00Z')`,
		`INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, result, issue, started_at)
		 VALUES ('owner/shop', 4, 'ci', 5, '', 1, 'hard', -15, '2027-01-01T00:00:00Z')`,
		// An open CI failure record, a record with an issue that is not below a Workstream yet and a record with no issue.
		`INSERT INTO ci_failures (repository, reason, issue, created_at) VALUES ('owner/shop', 'The plan test fails.', 14, '2027-01-01T00:00:00Z')`,
		`INSERT INTO ci_failures (repository, reason, issue, created_at) VALUES ('owner/shop', 'The price test fails.', -16, '2027-01-01T00:00:00Z')`,
		`INSERT INTO ci_failures (repository, reason, issue, created_at) VALUES ('owner/shop', 'The cart test fails.', 0, '2027-01-01T00:00:00Z')`,
		// A check timeout is not a CI fix.
		`INSERT INTO check_timeouts (repository, task_issue, session, timeout, output, issue, created_at)
		 VALUES ('owner/shop', 20, 1, '15m', '', 17, '2027-01-01T00:00:00Z')`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := db.QueryContext(ctx, "SELECT repository, issue FROM ci_fix_issues ORDER BY issue")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []int64
	for rows.Next() {
		var repository string
		var issue int64
		if err := rows.Scan(&repository, &issue); err != nil {
			t.Fatal(err)
		}
		if repository != "owner/shop" {
			t.Errorf("repository = %q", repository)
		}
		got = append(got, issue)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []int64{11, 12, 13, 14, 15, 16}; !slices.Equal(got, want) {
		t.Errorf("issues = %v, want %v", got, want)
	}
	for issue, want := range map[int64]bool{11: true, 15: true, 16: true, 17: false, 20: false, 0: false} {
		got, err := queries.IsCIFixIssue(ctx, IsCIFixIssueParams{Repository: "owner/shop", Issue: issue})
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("IsCIFixIssue(%d) = %v, want %v", issue, got, want)
		}
	}
	if got, err := queries.IsCIFixIssue(ctx, IsCIFixIssueParams{Repository: "owner/other", Issue: 11}); err != nil || got {
		t.Errorf("IsCIFixIssue of another repository = %v, %v", got, err)
	}
}
