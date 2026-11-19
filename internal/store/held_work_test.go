package store

import (
	"database/sql"
	"testing"
)

func TestHeldWorkIsSortedByTheTimeAndNotByTheText(t *testing.T) {
	_, queries := openQueries(t)
	ctx := t.Context()
	if _, err := queries.AddWorkstream(ctx, AddWorkstreamParams{Repository: "owner/shop", Number: 1, State: "open"}); err != nil {
		t.Fatal(err)
	}
	workstream, err := queries.GetWorkstreamID(ctx, GetWorkstreamIDParams{Repository: "owner/shop", Number: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range []int64{2, 3} {
		_, err := queries.AddCopiedIssue(ctx, AddCopiedIssueParams{Repository: "owner/shop", Number: issue, Title: "Issue", State: "open",
			Workstream: sql.NullInt64{Int64: workstream, Valid: true}, Kind: "task", Github: "{}"})
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
