package engine

import (
	"reflect"
	"testing"
)

func batchItem(id int64, bot bool) judgeItem {
	return judgeItem{id: id, bot: bot}
}

func verdictsOf(id int64, actions ...action) itemVerdicts {
	return itemVerdicts{Item: id, Actions: actions}
}

func TestFixAndQuestionGoToTheRoundAndTheOtherActionsToTheirRoutes(t *testing.T) {
	items := []judgeItem{batchItem(1, false), batchItem(2, true)}
	given := []itemVerdicts{
		verdictsOf(1, action{fixVerdict, "Rename the field."}, action{followUpVerdict, "Move the parser."}, action{questionVerdict, "Why cents?"}),
		verdictsOf(2, action{rejectVerdict, "The API needs the name."}),
	}

	want := judgeRoutes{
		round:     []itemVerdicts{verdictsOf(1, action{fixVerdict, "Rename the field."}, action{questionVerdict, "Why cents?"})},
		followUps: []itemText{{1, "Move the parser."}},
		rejects:   []itemText{{2, "The API needs the name."}},
	}
	if got := routesOf(items, given); !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %+v", got)
	}
}

func TestWithNoValidCallEachItemGoesToTheRoundAsFix(t *testing.T) {
	items := []judgeItem{batchItem(1, false), batchItem(2, true)}

	want := judgeRoutes{round: []itemVerdicts{verdictsOf(1, action{Verdict: fixVerdict}), verdictsOf(2, action{Verdict: fixVerdict})}}
	if got := routesOf(items, nil); !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %+v", got)
	}
}

func TestAValidCallGivesEachItemOneTimeWithAllowedActions(t *testing.T) {
	items := []judgeItem{batchItem(1, false), batchItem(2, true)}

	err := validateVerdicts(items, []itemVerdicts{
		verdictsOf(1, action{followUpVerdict, "Move the parser."}),
		verdictsOf(2, action{fixVerdict, "Add the test."}),
	})

	if err != nil {
		t.Error(err)
	}
}

func TestACallWithAMissingARepeatedOrAnUnknownItemIsNotValid(t *testing.T) {
	items := []judgeItem{batchItem(1, false), batchItem(2, false)}
	fix := action{fixVerdict, "Rename the field."}

	for _, given := range [][]itemVerdicts{
		{verdictsOf(1, fix)},
		{verdictsOf(1, fix), verdictsOf(1, fix), verdictsOf(2, fix)},
		{verdictsOf(1, fix), verdictsOf(2, fix), verdictsOf(3, fix)},
	} {
		if validateVerdicts(items, given) == nil {
			t.Errorf("%+v is valid", given)
		}
	}
}

func TestABotItemTakesNoQuestionOrFollowUpAndAUserItemTakesNoReject(t *testing.T) {
	for _, c := range []struct {
		item   judgeItem
		action action
	}{
		{batchItem(1, true), action{questionVerdict, "Why?"}},
		{batchItem(1, true), action{followUpVerdict, "Later."}},
		{batchItem(1, false), action{rejectVerdict, "No."}},
	} {
		if validateVerdicts([]judgeItem{c.item}, []itemVerdicts{verdictsOf(1, c.action)}) == nil {
			t.Errorf("%+v is valid for %+v", c.action, c.item)
		}
	}
}

func TestAnActionNeedsATextAndAnItemNeedsAnAction(t *testing.T) {
	items := []judgeItem{batchItem(1, false)}

	for _, given := range []itemVerdicts{verdictsOf(1, action{fixVerdict, " "}), verdictsOf(1)} {
		if validateVerdicts(items, []itemVerdicts{given}) == nil {
			t.Errorf("%+v is valid", given)
		}
	}
}

func TestAnActionNeedsAKnownVerdict(t *testing.T) {
	if validateVerdicts([]judgeItem{batchItem(1, false)}, []itemVerdicts{verdictsOf(1, action{"ignore", "Later."})}) == nil {
		t.Error("the verdict ignore is valid")
	}
}
