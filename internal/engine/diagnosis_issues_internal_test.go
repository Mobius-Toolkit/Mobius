package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

func TestTheIssueOfARecordGoesUnderTheReasonOfThatRecordWhateverTheOrderOfThePlaces(t *testing.T) {
	t.Parallel()
	e := researchEngine(t)
	other, err := e.queries.AddCIFailure(t.Context(), store.AddCIFailureParams{Repository: "owner/shop", Reason: "A", Test: "TestOther", Location: "other_test.go:1", CreatedAt: now()})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := e.queries.AddCIFailure(t.Context(), store.AddCIFailureParams{Repository: "owner/shop", Reason: "B", Test: "TestPlan", Location: "plan_test.go:12", CreatedAt: now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.queries.SetCIFailureIssue(t.Context(), store.SetCIFailureIssueParams{Issue: 7, ID: plan}); err != nil {
		t.Fatal(err)
	}
	reasons := fmt.Sprintf("- The other test fails.%[1]s  Record %[2]d.\n- The plan test fails.%[1]s  Record %[3]d.\n", notCausedSentence, other, plan)

	section, err := e.diagnosisWithIssues(t.Context(), github.Repository{FullName: "owner/shop", DefaultBranch: "main"}, store.CiDiagnosis{Reasons: reasons})

	want := "- The other test fails." + notCausedSentence + "- The plan test fails." + notCausedSentence + fmt.Sprintf(openIssueSentence, 7)
	if err != nil || !strings.Contains(section, "Diagnosis of the failed jobs:\n"+want+"\n") {
		t.Errorf("section = %q, error = %v", section, err)
	}
}
