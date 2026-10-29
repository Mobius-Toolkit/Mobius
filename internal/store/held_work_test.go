package store

import "testing"

func TestHeldWorkIsSortedByTheTimeAndNotByTheText(t *testing.T) {
	_, queries := openQueries(t)
	ctx := t.Context()
	if err := queries.AddCopiedWorkstream(ctx, AddCopiedWorkstreamParams{Repository: "owner/shop", Number: 1, Title: "Shop", CreatedAt: "2027-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for _, issue := range []int64{2, 3} {
		err := queries.AddCopiedIssue(ctx, AddCopiedIssueParams{Repository: "owner/shop", Workstream: 1, Position: issue, Number: issue,
			Title: "Issue", State: "open", RepositoryUrl: "https://api.github.com/repos/owner/shop"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := queries.AddGlobalPause(ctx, AddGlobalPauseParams{Kind: "start", PausedAt: "2027-01-01T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for issue, heldAt := range map[int64]string{2: "2027-01-01T10:00:05.15Z", 3: "2027-01-01T10:00:05.1Z"} {
		if _, err := queries.AddHeldWork(ctx, AddHeldWorkParams{Kind: "start", Repository: "owner/shop", Issue: issue, HeldAt: heldAt}); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := queries.ListHeldWork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Issue != 3 || rows[1].Issue != 2 {
		t.Fatalf("held work = %+v, want issue 3 (.1Z) before issue 2 (.15Z)", rows)
	}
}
