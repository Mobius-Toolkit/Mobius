package engine_test

import (
	"slices"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
)

func TestEditIssueBodyChangesIssuesBodyAndWritesGitHubWithChatLink(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake,
		call("edit_issue_body", `{ repository = "owner/shop", n = 41, body = "Updated goal.\n\n- Check item" }`),
		withUIURL)
	fake.AddIssue(shop, 41, "Plan API")
	fake.SetBody(shop, 41, "Old body")
	fake.AddSubIssue(shop, 12, 41)
	waitForTasks(t, server, []taskLine{line(41, "Plan API", "open", 0)})

	session := run(t, server, leadSpec(t), "1. ")

	if got := reply(t, server, session); got != "Changed the body of owner/shop#41." {
		t.Errorf("reply = %q", got)
	}
	var dbBody string
	if err := server.DB.QueryRow("SELECT body FROM issues WHERE repository = ? AND number = 41", shop).Scan(&dbBody); err != nil {
		t.Fatal(err)
	}
	if dbBody != "Updated goal.\n\n- Check item" {
		t.Errorf("issues.body = %q", dbBody)
	}
	var changes []string
	rows, err := server.DB.Query(`SELECT c.field || ':' || c.status FROM outgoing_changes c
		JOIN issues i ON i.id = c.issue
		WHERE i.repository = ? AND i.number = 41`, shop)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			t.Fatal(err)
		}
		changes = append(changes, entry)
	}
	if !slices.Equal(changes, []string{"body:sent"}) {
		t.Errorf("outgoing changes = %v", changes)
	}
	wantGH := "Updated goal.\n\n- Check item\n\n[Open the Lead chat in Mobius](https://mobius.example.com/workstreams/owner/shop/12)"
	if _, gotGH := fake.Issue(shop, 41); gotGH != wantGH {
		t.Errorf("github body = %q, want %q", gotGH, wantGH)
	}
}

func TestEditIssueBodyKeepsTheNewBodyAcrossAPollBeforeSend(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake,
		call("edit_issue_body", `{ repository = "owner/shop", n = 41, body = "New body before send" }`),
		withUIURL)
	fake.AddIssue(shop, 41, "Plan API")
	fake.SetBody(shop, 41, "Old body on GitHub")
	fake.AddSubIssue(shop, 12, 41)
	waitForTasks(t, server, []taskLine{line(41, "Plan API", "open", 0)})
	fake.FailBodyWrites(true)

	session := run(t, server, leadSpec(t), "1. ")

	if got := reply(t, server, session); got != "Mobius sends the body of owner/shop#41 to GitHub later." {
		t.Errorf("reply = %q", got)
	}
	if _, gotGH := fake.Issue(shop, 41); gotGH != "Old body on GitHub" {
		t.Fatalf("github body while failing = %q", gotGH)
	}

	fake.SetTitle(shop, 41, "Plan API updated")
	testkit.WaitFor(t, func() bool {
		var title string
		_ = server.DB.QueryRow("SELECT title FROM issues WHERE repository = ? AND number = 41", shop).Scan(&title)
		return title == "Plan API updated"
	})
	var dbBody string
	if err := server.DB.QueryRow("SELECT body FROM issues WHERE repository = ? AND number = 41", shop).Scan(&dbBody); err != nil {
		t.Fatal(err)
	}
	if dbBody != "New body before send" {
		t.Errorf("issues.body after poll = %q, want %q", dbBody, "New body before send")
	}

	fake.FailBodyWrites(false)
	wantGH := "New body before send\n\n[Open the Lead chat in Mobius](https://mobius.example.com/workstreams/owner/shop/12)"
	testkit.WaitFor(t, func() bool {
		_, gotGH := fake.Issue(shop, 41)
		return gotGH == wantGH
	})
}

func TestEditIssueBodyRefusesAnIssueOfAnotherWorkstream(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	server, _ := connectWith(t, fake,
		call("edit_issue_body", `{ repository = "owner/shop", n = 60, body = "Other" }`)+
			call("edit_issue_body", `{ repository = "owner/shop", n = 12, body = "Workstream" }`)+
			call("edit_issue_body", `{ repository = "owner/shop", n = 45, body = "PR" }`),
		withUIURL)
	fake.AddIssue(shop, 13, "Billing")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	fake.AddIssue(shop, 60, "Invoice model")
	fake.SetBody(shop, 60, "Original 60")
	fake.AddSubIssue(shop, 13, 60)
	fake.AddPullRequest(shop, 45, "A pull request")
	testkit.WaitFor(t, func() bool {
		var count int
		_ = server.DB.QueryRow("SELECT count(*) FROM issues WHERE repository = ? AND number = 60", shop).Scan(&count)
		return count == 1
	})

	session := run(t, server, leadSpec(t), "1. ", "2. ", "3. ")

	want := "error: owner/shop#60 is not in this Workstream." +
		"error: owner/shop#12 is a Workstream issue." +
		"error: owner/shop#45 is not an issue."
	if got := reply(t, server, session); got != want {
		t.Errorf("reply = %q", got)
	}
	var dbBody string
	if err := server.DB.QueryRow("SELECT body FROM issues WHERE repository = ? AND number = 60", shop).Scan(&dbBody); err != nil {
		t.Fatal(err)
	}
	if dbBody != "Original 60" {
		t.Errorf("issues.body of 60 = %q", dbBody)
	}
	if _, gotGH := fake.Issue(shop, 60); gotGH != "Original 60" {
		t.Errorf("github body of 60 = %q", gotGH)
	}
	var changes int
	if err := server.DB.QueryRow("SELECT count(*) FROM outgoing_changes WHERE issue IS NOT NULL").Scan(&changes); err != nil || changes != 0 {
		t.Errorf("outgoing changes = %d, %v", changes, err)
	}
}
