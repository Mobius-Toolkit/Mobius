package engine_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Mobius-Toolkit/Mobius/internal/config"
	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

func skillReviews(t *testing.T, server *testserver.Server) []string {
	t.Helper()
	return query(t, server, func(rows *sql.Rows, payload *string) error {
		return rows.Scan(payload)
	}, "SELECT payload FROM lead_events WHERE kind = 'skill_review' ORDER BY id")
}

func waitForReviews(t *testing.T, server *testserver.Server, count int) []string {
	t.Helper()
	return testkit.WaitForValue(t, func() ([]string, bool) {
		reviews := skillReviews(t, server)
		return reviews, len(reviews) == count
	})
}

// reviewSection gives the lines of the section of payload that starts with the heading.
func reviewSection(t *testing.T, payload, heading string) string {
	t.Helper()
	_, section, found := strings.Cut(payload, "\n## "+heading+"\n")
	if !found {
		t.Fatalf("no section %q in %q", heading, payload)
	}
	section, _, _ = strings.Cut(section, "\n## ")
	return section
}

// addSessions adds count sessions and gives their ids.
func addSessions(t *testing.T, server *testserver.Server, count int) []int64 {
	t.Helper()
	var ids []int64
	for range count {
		session, err := store.New(server.DB).AddSession(t.Context(), store.AddSessionParams{Role: "implementer", Harness: "claude-code", Model: "model", Organization: "owner", Repository: shop, WorkstreamRepository: shop, Workstream: 12, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, session.ID)
	}
	return ids
}

func seedReviewSkills(t *testing.T, server *testserver.Server, sessions []int64, harness string, now time.Time) {
	t.Helper()
	copied := func(session int64, skill string) {
		addSkillRow(t, server, "session_skills", session, "owner", shop, "implementer", harness, skill, "", now.Add(-24*time.Hour))
	}
	used := func(session int64, skill string) {
		addSkillRow(t, server, "skill_uses", session, "owner", shop, "implementer", harness, skill, "mobius", now.Add(-23*time.Hour))
	}
	for _, session := range sessions[:20] {
		copied(session, "crowded")
		copied(session, "balanced")
	}
	for _, session := range sessions[:5] {
		copied(session, "idle")
	}
	used(sessions[0], "crowded")
	used(sessions[0], "balanced")
	used(sessions[1], "balanced")
}

func addReviewSkills(fake *testkit.FakeGitHub) {
	fake.AddRepository(skills)
	addSkillsWorkstream(fake, 20)
	for _, name := range []string{"crowded", "balanced", "idle"} {
		fake.CommitFile(skills, name+"/SKILL.md", "---\nname: "+name+"\ndescription: Skill "+name+".\n---\nUse it.\n", "Add "+name)
	}
}

func TestTheSkillsWorkstreamGetsOneSkillReviewEachWeek(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addReviewSkills(fake)
	server, _ := connect(t, fake, "")

	first := waitForReviews(t, server, 1)
	waitForPolls(t, fake)
	if got := skillReviews(t, server); len(got) != 1 {
		t.Fatalf("reviews in the interval = %d, want 1", len(got))
	}
	server.Engine.SetClock(func() time.Time { return time.Now().Add(8 * 24 * time.Hour) })
	second := waitForReviews(t, server, 2)
	waitForPolls(t, fake)
	if got := skillReviews(t, server); len(got) != 2 {
		t.Fatalf("reviews after the second = %d, want 2", len(got))
	}

	if !strings.Contains(first[0], "No copy and no use") {
		t.Errorf("review = %q, want %q in it", first[0], "No copy and no use")
	}
	for _, review := range []string{first[0], second[1]} {
		for _, want := range []string{"Skill usage from ", "## idle\n\nFolder: idle/\n", "## Many copies, few uses", "## No use in 30 days", "## Used with no copy"} {
			if !strings.Contains(review, want) {
				t.Errorf("review = %q, want %q in it", review, want)
			}
		}
	}
}

func TestAnOrganizationWithNoSkillsWorkstreamGetsNoSkillReview(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	fake.AddRepository(skills)
	server, _ := connect(t, fake, "")

	waitForPolls(t, fake)

	if got := skillReviews(t, server); len(got) != 0 {
		t.Errorf("reviews = %q", got)
	}
}

func TestTheSkillReviewNamesTheSkillsOfTheThreeRules(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addReviewSkills(fake)
	server, _ := connect(t, fake, "")
	waitForReviews(t, server, 1)
	now := time.Now().Add(8 * 24 * time.Hour)
	sessions := addSessions(t, server, 20)
	seedReviewSkills(t, server, sessions, "claude-code", now)
	harnessUse := func(session int64, skill string) {
		addSkillRow(t, server, "skill_uses", session, "owner", shop, "lead_chat", "claude-code", skill, "harness", now.Add(-time.Hour))
	}
	for _, session := range sessions[:3] {
		harnessUse(session, "often")
	}
	for _, session := range sessions[:2] {
		harnessUse(session, "seldom")
	}

	server.Engine.SetClock(func() time.Time { return now })
	review := waitForReviews(t, server, 2)[1]

	if got := reviewSection(t, review, "Many copies, few uses"); !strings.HasSuffix(got, "\n\n- crowded/, role implementer: Had 20, Used 1\n") {
		t.Errorf("many copies = %q", got)
	}
	if got := reviewSection(t, review, "No use in 30 days"); !strings.HasSuffix(got, "\n\n- idle/\n") {
		t.Errorf("no use = %q", got)
	}
	if got := reviewSection(t, review, "Used with no copy"); !strings.HasSuffix(got, "\n\n- often, role lead: Used 3\n") {
		t.Errorf("used with no copy = %q", got)
	}
}

func TestTheSkillReviewDoesNotCountTheCopiesOfAHarnessWithNoSkillUseRecord(t *testing.T) {
	// Serial: the test changes the Harnesses with a skill use record.
	restore := engine.SetRecordedSkillHarnesses(config.ClaudeCode)
	t.Cleanup(restore)
	fake := testkit.NewFakeGitHub(t)
	addReviewSkills(fake)
	server, _ := connect(t, fake, "")
	waitForReviews(t, server, 1)
	now := time.Now().Add(8 * 24 * time.Hour)
	seedReviewSkills(t, server, addSessions(t, server, 20), "devin", now)

	server.Engine.SetClock(func() time.Time { return now })
	review := waitForReviews(t, server, 2)[1]

	if got := reviewSection(t, review, "Many copies, few uses"); !strings.HasSuffix(got, "\n\nNone\n") {
		t.Errorf("many copies = %q", got)
	}
}
