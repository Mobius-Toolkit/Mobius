package engine_test

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func setEvergreen(t *testing.T, server *testserver.Server, number int64, on bool) (int, string) {
	t.Helper()
	return send(t, server, http.MethodPut, "/api/workstreams/owner/shop/"+itoa(number)+"/evergreen", `{"on":`+strconv.FormatBool(on)+`}`)
}

func evergreenOfBilling(t *testing.T, server *testserver.Server) bool {
	t.Helper()
	return workstreams(t, server)[0].Evergreen
}

func TestSetEvergreenOnAddsTheLabelAndShowsItAtOnce(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithBilling(t, fake)
	changes := listen(t, server)

	if status, body := setEvergreen(t, server, 20, true); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if !slices.Contains(fake.Labels(shop, 20), "mobius:evergreen") {
		t.Errorf("labels = %v", fake.Labels(shop, 20))
	}
	if !evergreenOfBilling(t, server) {
		t.Error("the list shows evergreen false")
	}
	if status, got := details(t, server, 20); status != http.StatusOK || !got.Evergreen || got.Maintenance || got.Skills {
		t.Errorf("details = %d %+v", status, got)
	}
	waitForWorkstreams(t, changes)
}

func TestSetEvergreenOffRemovesTheLabelAndShowsItAtOnce(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddIssue(shop, 20, "Billing")
	fake.AddLabel(shop, 20, "mobius:workstream", "owner")
	fake.AddLabel(shop, 20, "mobius:evergreen", "owner")
	server := startCopied(t, fake)
	if !evergreenOfBilling(t, server) {
		t.Fatal("billing is not evergreen")
	}

	if status, body := setEvergreen(t, server, 20, false); status != http.StatusNoContent {
		t.Fatalf("status = %d: %s", status, body)
	}

	if slices.Contains(fake.Labels(shop, 20), "mobius:evergreen") {
		t.Errorf("labels = %v", fake.Labels(shop, 20))
	}
	if evergreenOfBilling(t, server) {
		t.Error("the list shows evergreen true")
	}
	if status, got := details(t, server, 20); status != http.StatusOK || got.Evergreen {
		t.Errorf("details = %d %+v", status, got)
	}
}

func TestSetEvergreenRefusesAWorkstreamThatMobiusManages(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ label, name string }{
		{"mobius:maintenance", "Maintenance Workstream"},
		{"mobius:skills", "Skills Workstream"},
	} {
		t.Run(test.label, func(t *testing.T) {
			t.Parallel()
			fake := testkit.NewFakeGitHub(t)
			fake.AddIssue(shop, 20, "Managed")
			fake.AddLabel(shop, 20, "mobius:workstream", "owner")
			fake.AddLabel(shop, 20, test.label, "owner")
			fake.AddLabel(shop, 20, "mobius:evergreen", "owner")
			server := startCopied(t, fake)
			want := fake.Labels(shop, 20)

			for _, on := range []bool{false, true} {
				status, body := setEvergreen(t, server, 20, on)

				if status != http.StatusConflict || !strings.Contains(body, "#20 is the "+test.name+". Mobius manages it, so its evergreen value cannot change.") {
					t.Errorf("on %v: status = %d: %s", on, status, body)
				}
			}
			if got := fake.Labels(shop, 20); !slices.Equal(got, want) {
				t.Errorf("labels = %v, want %v", got, want)
			}
			status, got := details(t, server, 20)
			if status != http.StatusOK || !got.Evergreen || got.Maintenance != (test.label == "mobius:maintenance") || got.Skills != (test.label == "mobius:skills") {
				t.Errorf("details = %d %+v", status, got)
			}
		})
	}
}

func TestSetEvergreenRefusesAnIssueThatIsNoOpenWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server := startWithBilling(t, fake)

	for _, number := range []int64{88, 99} {
		status, body := setEvergreen(t, server, number, true)

		if status != http.StatusConflict || !strings.Contains(body, "The issue is not an open Workstream.") {
			t.Errorf("#%d: status = %d: %s", number, status, body)
		}
	}
	if slices.Contains(fake.Labels(shop, 88), "mobius:evergreen") {
		t.Errorf("labels = %v", fake.Labels(shop, 88))
	}
}
