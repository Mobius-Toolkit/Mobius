package store

import (
	"database/sql"
	"testing"
)

func TestANewSessionOfADiagnosisClearsTheReasonsOfTheEarlierSession(t *testing.T) {
	_, queries := openQueries(t)
	ctx := t.Context()
	if _, err := queries.AddCIDiagnosis(ctx, AddCIDiagnosisParams{Repository: "owner/shop", PullRequest: 42, Head: "abc", CreatedAt: "2026-10-20T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	row, err := queries.NextCIDiagnosis(ctx, "owner/shop")
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.SetCIDiagnosisSession(ctx, SetCIDiagnosisSessionParams{Session: sql.NullInt64{Int64: 1, Valid: true}, ID: row.ID}); err != nil {
		t.Fatal(err)
	}
	if err := queries.AddCIDiagnosisReason(ctx, AddCIDiagnosisReasonParams{Reasons: "- The plan test fails.\n", ID: row.ID}); err != nil {
		t.Fatal(err)
	}

	if err := queries.SetCIDiagnosisSession(ctx, SetCIDiagnosisSessionParams{Session: sql.NullInt64{Int64: 2, Valid: true}, ID: row.ID}); err != nil {
		t.Fatal(err)
	}
	row, err = queries.GetCIDiagnosisBySession(ctx, sql.NullInt64{Int64: 2, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if row.Reasons != "" {
		t.Errorf("reasons = %q", row.Reasons)
	}
}
