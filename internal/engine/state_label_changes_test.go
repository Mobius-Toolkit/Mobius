package engine_test

import (
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestARemovalOfWorkingByAnActorWhoIsNotTrustedHasNoEffectAndTheSendAddsTheLabelBackAsAnOwnWrite(t *testing.T) {
	t.Parallel()
	for _, actor := range []string{"stranger", app} {
		t.Run(actor, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			server, _ := seedWaiting(t, fake, "approval")
			waitForPolls(t, fake)

			fake.RemoveLabel(shop, 41, "mobius:working", actor)

			testkit.WaitFor(t, func() bool {
				return hasLabel(fake, "mobius:working") && ownWrites(t, server) == 0 && incomingChanges(t, server, "pending") == 0
			})
			waitForPolls(t, fake)
			if state := taskState(t, server); state != "approval" {
				t.Errorf("state = %s", state)
			}
			if got := countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE field = 'state_labels' AND actor = ? AND change = 'unlabeled' AND status = 'done'", actor); got != 1 {
				t.Errorf("done removals of %s = %d, want 1", actor, got)
			}
			if got := incomingChanges(t, server, "pending"); got != 0 {
				t.Errorf("pending incoming changes = %d", got)
			}
			if got := fake.LabelActor(shop, 41, "mobius:working"); got != app {
				t.Errorf("actor of mobius:working = %s, want %s", got, app)
			}
		})
	}
}

func TestAnAddOfNeedsHumanByATrustedUserToTheIssueOfAWorkingTaskHasNoEffectAndTheSendRemovesTheLabel(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := seedWaiting(t, fake, "approval")
	waitForPolls(t, fake)

	fake.AddLabel(shop, 41, "mobius:needs-human", "owner")

	testkit.WaitFor(t, func() bool { return !hasLabel(fake, "mobius:needs-human") })
	waitForStateLabelWrite(t, server, 41, "mobius:needs-human", "unlabeled")
	testkit.WaitFor(t, func() bool { return incomingChanges(t, server, "pending") == 0 })
	waitForPolls(t, fake)
	if state := taskState(t, server); state != "approval" {
		t.Errorf("state = %s", state)
	}
	if rounds := implementerRounds(t, server); rounds != 0 {
		t.Errorf("Implementers = %d", rounds)
	}
	if !hasLabel(fake, "mobius:working") {
		t.Errorf("labels = %v", fake.Labels(shop, 41))
	}
}

func TestAnAddOfWorkingByATrustedUserToATreeIssueWithNoLiveTaskGetsRemovedBySendStateLabels(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
	fake.AddIssue(shop, 41, "Add plan model")
	fake.AddSubIssue(shop, 12, 41)
	server := startCopied(t, fake)

	fake.AddLabel(shop, 41, "mobius:working", "owner")

	testkit.WaitFor(t, func() bool { return fake.LabelRemovedBy(shop, 41, "mobius:working", testkit.AppSlug+"[bot]") })
	if got := countRows(t, server, "SELECT count(*) FROM incoming_changes WHERE field = 'state_labels' AND actor = 'owner' AND change = 'labeled' AND number = 41"); got != 1 {
		t.Errorf("incoming changes of the add = %d, want 1", got)
	}
}
