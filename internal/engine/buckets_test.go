package engine_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
	"github.com/Mobius-Toolkit/Mobius/internal/testkit/testserver"
)

// addSubIssues adds the issues first to last below the issue parent. The issues before closedUntil are closed, in
// the order of their numbers.
func addSubIssues(fake *testkit.FakeGitHub, parent, first, last, closedUntil int64) {
	for number := first; number <= last; number++ {
		fake.AddIssue(shop, number, fmt.Sprintf("Task %d", number))
		fake.AddSubIssue(shop, parent, number)
		if number < closedUntil {
			fake.CloseIssue(shop, number)
		}
	}
}

func addBucket(fake *testkit.FakeGitHub, number int64, title string, closed bool) {
	fake.AddIssue(shop, number, title)
	fake.AddLabel(shop, number, "mobius:bucket", "owner")
	fake.AddSubIssue(shop, 12, number)
	if closed {
		fake.CloseIssue(shop, number)
	}
}

const directSubIssuesTarget = 30

const maxBucketMoves = 100

func addBucketWorkstream(fake *testkit.FakeGitHub) {
	fake.AddIssue(shop, 12, "Integrate loyalty plans")
	fake.AddLabel(shop, 12, "mobius:workstream", "owner")
}

func numbers(first, last int64) []int64 {
	var all []int64
	for number := first; number <= last; number++ {
		all = append(all, number)
	}
	return all
}

func sorted(values []int64) []int64 {
	result := slices.Clone(values)
	slices.Sort(result)
	return result
}

func waitForThirtyDirectSubIssues(t *testing.T, fake *testkit.FakeGitHub) {
	t.Helper()
	testkit.WaitFor(t, func() bool { return len(fake.SubIssueNumbers(shop, 12)) == directSubIssuesTarget })
}

func TestAWorkstreamWithTooManySubIssuesGetsABucketForTheClosedIssuesAndABucketForTheOldestOpenIssues(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addSubIssues(fake, 12, 100, 150, 110)

	server := startCopied(t, fake)

	waitForThirtyDirectSubIssues(t, fake)
	if got := sorted(fake.SubIssueNumbers(shop, 12)); !slices.Equal(got, append(numbers(123, 150), 151, 152)) {
		t.Errorf("sub-issues of the Workstream = %v", got)
	}
	if got := sorted(fake.SubIssueNumbers(shop, 151)); !slices.Equal(got, numbers(100, 109)) {
		t.Errorf("sub-issues of the closed bucket = %v", got)
	}
	if got := sorted(fake.SubIssueNumbers(shop, 152)); !slices.Equal(got, numbers(110, 122)) {
		t.Errorf("sub-issues of the open bucket = %v", got)
	}
	state, reason := fake.State(shop, 151)
	title, body := fake.Issue(shop, 151)
	if state != "closed" || reason != "completed" || title != "Bucket 1 of #12" || !strings.Contains(body, "Workstream #12") || !slices.Equal(fake.Labels(shop, 151), []string{"mobius:bucket"}) {
		t.Errorf("closed bucket = %q, %q, %q, %q, %v", state, reason, title, body, fake.Labels(shop, 151))
	}
	state, _ = fake.State(shop, 152)
	title, _ = fake.Issue(shop, 152)
	if state != "open" || title != "Bucket 2 of #12" || !slices.Equal(fake.Labels(shop, 152), []string{"mobius:bucket"}) {
		t.Errorf("open bucket = %q, %q, %v", state, title, fake.Labels(shop, 152))
	}
	var want []taskLine
	for number := int64(100); number <= 150; number++ {
		state := "open"
		if number < 110 {
			state = "closed"
		}
		want = append(want, line(number, fmt.Sprintf("Task %d", number), state, 0))
	}
	testkit.WaitFor(t, func() bool {
		got := apiData[[]taskLine](t, server, "/api/workstreams/owner/shop/12/tasks")
		return len(got) == len(want) && got[0].Number == 100 && reflect.DeepEqual(sortedLines(got), want)
	})
}

func sortedLines(lines []taskLine) []taskLine {
	result := slices.Clone(lines)
	slices.SortFunc(result, func(a, b taskLine) int { return int(a.Number - b.Number) })
	return result
}

// subIssueReadsOfEachPoll gives the number of reads of the sub-issues of the issue in each poll after the first. The
// copy of the trees reads them one time in each poll. Each poll reads the installations one time.
func subIssueReadsOfEachPoll(fake *testkit.FakeGitHub, number int64) []int {
	var reads []int
	for _, request := range fake.Requests() {
		switch {
		case request.Method != "GET":
		case request.Path == "/app/installations":
			reads = append(reads, 0)
		case request.Path == fmt.Sprintf("/repos/owner/shop/issues/%d/sub_issues", number) && len(reads) > 1:
			reads[len(reads)-1]++
		}
	}
	return reads[min(1, len(reads)):]
}

func TestAWorkstreamWithFiftySubIssuesGetsNoBucketUntilItGetsTheFiftyFirst(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addSubIssues(fake, 12, 100, 149, 110)
	startCopied(t, fake)

	waitForPolls(t, fake)
	for _, request := range fake.Requests() {
		if request.Method == "POST" && strings.Contains(request.Path, "/issues") {
			t.Errorf("request = %s %s", request.Method, request.Path)
		}
	}
	if got := fake.SubIssueNumbers(shop, 12); len(got) != 50 || fake.HasIssue(shop, 151) {
		t.Errorf("sub-issues = %v", got)
	}
	if reads := subIssueReadsOfEachPoll(fake, 12); len(reads) < 3 || slices.Max(reads) > 1 {
		t.Errorf("reads of the sub-issues of #12 in each poll = %v", reads)
	}

	fake.AddSubIssueOf(shop, 12, 150, "Task 150")

	waitForThirtyDirectSubIssues(t, fake)
	if !fake.HasIssue(shop, 151) {
		t.Error("the Workstream got no bucket")
	}
}

func TestAFullClosedBucketGetsASecondClosedBucket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addSubIssues(fake, 12, 100, 149, 105)
	addBucket(fake, 41, "Bucket 1 of #12", true)
	addSubIssues(fake, 41, 200, 299, 300)
	startCopied(t, fake)

	waitForThirtyDirectSubIssues(t, fake)
	if got := sorted(fake.SubIssueNumbers(shop, 300)); !slices.Equal(got, numbers(100, 104)) {
		t.Errorf("sub-issues of the second closed bucket = %v", got)
	}
	if got := fake.SubIssueNumbers(shop, 41); len(got) != 100 {
		t.Errorf("sub-issues of the first closed bucket = %v", got)
	}
	title, _ := fake.Issue(shop, 300)
	if state, _ := fake.State(shop, 300); state != "closed" || title != "Bucket 2 of #12" {
		t.Errorf("second closed bucket = %q, %q", state, title)
	}
}

func TestAClosedBucketWithRoomGetsTheClosedIssuesAndTheStepMakesNoClosedBucket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addSubIssues(fake, 12, 100, 149, 105)
	addBucket(fake, 41, "Bucket 1 of #12", true)
	addSubIssues(fake, 41, 200, 294, 300)
	startCopied(t, fake)

	waitForThirtyDirectSubIssues(t, fake)
	if got := fake.SubIssueNumbers(shop, 41); len(got) != 100 || !slices.Contains(got, 100) || !slices.Contains(got, 104) {
		t.Errorf("sub-issues of the closed bucket = %v", got)
	}
	if state, _ := fake.State(shop, 295); state != "open" || fake.HasIssue(shop, 296) {
		t.Errorf("state of #295 = %q, #296 = %t", state, fake.HasIssue(shop, 296))
	}
}

func TestAClosedIssueOfAnOpenBucketMovesIntoAClosedBucketAndAnEmptyOpenBucketCloses(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addBucket(fake, 41, "Bucket 1 of #12", false)
	addSubIssues(fake, 41, 50, 51, 0)
	fake.CloseIssue(shop, 51)
	addBucket(fake, 42, "Bucket 2 of #12", false)
	addBucket(fake, 43, "Bucket 3 of #12", true)
	startCopied(t, fake)

	testkit.WaitFor(t, func() bool {
		state, _ := fake.State(shop, 42)
		return state == "closed" && slices.Equal(fake.SubIssueNumbers(shop, 43), []int64{51}) && slices.Equal(fake.SubIssueNumbers(shop, 41), []int64{50})
	})
	if _, reason := fake.State(shop, 42); reason != "completed" {
		t.Errorf("reason = %q", reason)
	}
	if got := fake.SubIssueNumbers(shop, 43); !slices.Equal(got, []int64{51}) {
		t.Errorf("sub-issues of the closed bucket = %v", got)
	}
	if got := fake.SubIssueNumbers(shop, 41); !slices.Equal(got, []int64{50}) {
		t.Errorf("sub-issues of the open bucket = %v", got)
	}
	if state, _ := fake.State(shop, 41); state != "open" || fake.HasIssue(shop, 44) {
		t.Errorf("state of #41 = %q, #44 = %t", state, fake.HasIssue(shop, 44))
	}
}

func TestAFailedAddOfANewBucketMakesNoSecondBucketAndTheNextPollFinishesTheBucket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addSubIssues(fake, 12, 100, 150, 100)
	fake.FailAddSubIssue(shop, 12, true)
	startCopied(t, fake)

	waitForPolls(t, fake)
	if !fake.HasIssue(shop, 151) || fake.HasIssue(shop, 152) || len(fake.SubIssueNumbers(shop, 12)) != 51 {
		t.Errorf("issue #151 = %t, #152 = %t, sub-issues = %v", fake.HasIssue(shop, 151), fake.HasIssue(shop, 152), fake.SubIssueNumbers(shop, 12))
	}

	fake.FailAddSubIssue(shop, 12, false)

	waitForThirtyDirectSubIssues(t, fake)
	if got := sorted(fake.SubIssueNumbers(shop, 151)); !slices.Equal(got, numbers(100, 121)) || fake.HasIssue(shop, 152) {
		t.Errorf("sub-issues of #151 = %v, #152 = %t", got, fake.HasIssue(shop, 152))
	}
}

func TestAFailedCloseOfANewClosedBucketMakesNoSecondBucketAndTheNextPollFinishesTheBucket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addSubIssues(fake, 12, 100, 150, 101)
	fake.FailClose(shop, 151)
	startCopied(t, fake)

	waitForPolls(t, fake)
	if !fake.HasIssue(shop, 151) || fake.HasIssue(shop, 152) || len(fake.SubIssueNumbers(shop, 12)) != 51 {
		t.Errorf("issue #151 = %t, #152 = %t, sub-issues = %v", fake.HasIssue(shop, 151), fake.HasIssue(shop, 152), fake.SubIssueNumbers(shop, 12))
	}

	fake.AllowClose(shop, 151)

	waitForThirtyDirectSubIssues(t, fake)
	state, _ := fake.State(shop, 151)
	if got := fake.SubIssueNumbers(shop, 151); state != "closed" || !slices.Equal(got, []int64{100}) || fake.HasIssue(shop, 153) {
		t.Errorf("state of #151 = %q, sub-issues of #151 = %v, #153 = %t", state, got, fake.HasIssue(shop, 153))
	}
}

func TestABucketIssueThatGitHubNoLongerHasLosesItsRowAndTheNextPollMovesTheSubIssues(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addSubIssues(fake, 12, 100, 150, 100)
	fake.FailAddSubIssue(shop, 12, true)
	startCopied(t, fake)

	waitForPolls(t, fake)
	if !fake.HasIssue(shop, 151) {
		t.Fatal("issue #151 does not exist")
	}

	fake.GoneIssue(shop, 151)
	testkit.WaitFor(t, func() bool { return fake.HasIssue(shop, 152) })
	fake.FailAddSubIssue(shop, 12, false)

	waitForThirtyDirectSubIssues(t, fake)
	if got := sorted(fake.SubIssueNumbers(shop, 152)); !slices.Equal(got, numbers(100, 121)) {
		t.Errorf("sub-issues of #152 = %v", got)
	}
}

func TestAWorkstreamWithOneHundredDirectSubIssuesAndNoBucketGetsAClosedBucketThatTakesTheClosedIssues(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addSubIssues(fake, 12, 100, 199, 150)
	startCopied(t, fake)

	waitForThirtyDirectSubIssues(t, fake)
	if got := sorted(fake.SubIssueNumbers(shop, 200)); !slices.Equal(got, numbers(100, 149)) {
		t.Errorf("sub-issues of the closed bucket = %v", got)
	}
	state, _ := fake.State(shop, 200)
	if got := fake.Labels(shop, 200); state != "closed" || !slices.Equal(got, []string{"mobius:bucket"}) {
		t.Errorf("closed bucket = %q, %v", state, got)
	}
	if got := sorted(fake.SubIssueNumbers(shop, 12)); !slices.Contains(got, 200) || !slices.Contains(got, 201) {
		t.Errorf("sub-issues of the Workstream = %v", got)
	}
}

func TestAnOpenBucketThatGitHubRefusesEachOpenIssueStaysAndTheNextPollsMakeNoNewBucket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addSubIssues(fake, 12, 100, 150, 100)
	fake.RefuseOpenBucketAdds(true)
	startCopied(t, fake)

	waitForPolls(t, fake)
	waitForPolls(t, fake)
	if state, _ := fake.State(shop, 151); state != "open" || fake.HasIssue(shop, 152) || len(fake.SubIssueNumbers(shop, 151)) != 0 {
		t.Errorf("state of #151 = %q, #152 = %t, sub-issues of #151 = %v", state, fake.HasIssue(shop, 152), fake.SubIssueNumbers(shop, 151))
	}

	fake.RefuseOpenBucketAdds(false)

	// A poll that runs while the refusal ends refuses its first moves and makes the later ones. When that poll moves
	// two or more issues, the Workstream has 50 or fewer direct sub-issues and the next polls move no more, so the test
	// expects neither the oldest issues nor 30 direct sub-issues.
	testkit.WaitFor(t, func() bool { return len(fake.SubIssueNumbers(shop, 151)) > 0 })
	waitForPolls(t, fake)
	got := sorted(fake.SubIssueNumbers(shop, 151))
	if len(got) > 22 || got[0] < 100 || got[len(got)-1] > 150 || fake.HasIssue(shop, 152) {
		t.Errorf("sub-issues of #151 = %v, #152 = %t", got, fake.HasIssue(shop, 152))
	}
}

func TestRefusedAddsCountTowardTheLimitOfMovesOfAPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	fake.AddIssue(shop, 13, "Second Workstream")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	addSubIssues(fake, 12, 100, 189, 100)
	addSubIssues(fake, 13, 300, 389, 300)
	fake.RefuseOpenBucketAdds(true)
	startCopied(t, fake)

	waitForPolls(t, fake)
	waitForPolls(t, fake)
	var adds []int
	for _, request := range fake.Requests() {
		switch {
		case request.Method == "POST" && strings.HasSuffix(request.Path, "/sub_issues") && len(adds) > 0:
			adds[len(adds)-1]++
		case request.Method == "GET" && request.Path == "/app/installations":
			adds = append(adds, 0)
		}
	}
	if len(adds) < 3 || slices.Max(adds) > maxBucketMoves {
		t.Errorf("adds of a sub-issue in each poll = %v", adds)
	}
}

func TestRefusedMovesBelowANewBucketForAFullWorkstreamStopAtTheLimitOfMovesOfAPoll(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	fake.AddIssue(shop, 13, "Second Workstream")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	addSubIssues(fake, 12, 100, 189, 100)
	addSubIssues(fake, 13, 300, 399, 300)
	fake.RefuseOpenBucketAdds(true)
	startCopied(t, fake)

	waitForPolls(t, fake)
	waitForPolls(t, fake)
	var adds []int
	for _, request := range fake.Requests() {
		switch {
		case request.Method == "POST" && strings.HasSuffix(request.Path, "/sub_issues") && len(adds) > 0:
			adds[len(adds)-1]++
		case request.Method == "GET" && request.Path == "/app/installations":
			adds = append(adds, 0)
		}
	}
	if len(adds) < 3 || slices.Max(adds) > maxBucketMoves+1 {
		t.Errorf("adds of a sub-issue in each poll = %v", adds)
	}
}

func bucketEntries(t *testing.T, server *testserver.Server) string {
	t.Helper()
	var got string
	err := server.DB.QueryRow(`SELECT coalesce(group_concat(json_extract(b.value, '$.number') || ':' || json_extract(b.value, '$.state') || ':' || coalesce(json_extract(b.value, '$.pending'), 0), ','), '')
		FROM (SELECT value FROM workstreams, json_each(metadata, '$.github.buckets') WHERE repository = ? AND number = 12 ORDER BY json_extract(value, '$.number')) b`, shop).Scan(&got)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestTheMovesOfFillBucketsMakeNoIncomingChangeAndTheMetadataOfTheWorkstreamHoldsTheBuckets(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addSubIssues(fake, 12, 100, 150, 110)

	server := startCopied(t, fake)

	waitForThirtyDirectSubIssues(t, fake)
	testkit.WaitFor(t, func() bool {
		var count int
		err := server.DB.QueryRow("SELECT count(*) FROM incoming_changes WHERE repository = ? AND number = 151 AND field = 'state' AND status = 'done'", shop).Scan(&count)
		return err == nil && count == 1
	})
	waitForPolls(t, fake)
	var pending int
	if err := server.DB.QueryRow("SELECT count(*) FROM incoming_changes WHERE status = 'pending'").Scan(&pending); err != nil || pending != 0 {
		t.Errorf("pending incoming changes = %d, %v", pending, err)
	}
	if got, want := bucketEntries(t, server), "151:closed:0,152:open:0"; got != want {
		t.Errorf("buckets = %q, want %q", got, want)
	}
	var moved int
	err := server.DB.QueryRow(`SELECT count(*) FROM issues WHERE repository = ? AND number BETWEEN 100 AND 109 AND json_extract(metadata, '$.github.bucket') = 151`, shop).Scan(&moved)
	if err != nil || moved != 10 {
		t.Errorf("rows below the closed bucket = %d, %v", moved, err)
	}
}

func TestACreateOfABucketThatAnswersWith500AfterGitHubMadeTheBucketMakesNoSecondBucket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addSubIssues(fake, 12, 100, 150, 100)
	fake.LoseIssueCreateAnswers(1)

	server := startCopied(t, fake)

	waitForThirtyDirectSubIssues(t, fake)
	waitForPolls(t, fake)
	if got := sorted(fake.SubIssueNumbers(shop, 151)); !slices.Equal(got, numbers(100, 121)) || fake.HasIssue(shop, 152) {
		t.Errorf("sub-issues of #151 = %v, #152 = %t", got, fake.HasIssue(shop, 152))
	}
	if got, want := bucketEntries(t, server), "151:open:0"; got != want {
		t.Errorf("buckets = %q, want %q", got, want)
	}
}

func TestTheSendOfCreateIssueGivesTheIssueOfAFullWorkstreamToTheOpenBucket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addBucket(fake, 41, "Bucket 1 of #12", false)
	fake.AddIssue(shop, 50, "Task 50")
	fake.AddSubIssue(shop, 41, 50)
	fake.RefuseAddSubIssues(shop, 12, true)
	server, _ := connect(t, fake, call("create_issue", createPlanModel))
	testkit.WaitFor(t, func() bool { return bucketEntries(t, server) == "41:open:0" })

	session := run(t, server, leadSpec(t), "Plan the work.")

	if got := reply(t, server, session); got != "Created owner/shop#51." {
		t.Fatalf("reply = %q", got)
	}
	if got := sorted(fake.SubIssueNumbers(shop, 41)); !slices.Equal(got, []int64{50, 51}) {
		t.Errorf("sub-issues of the bucket = %v", got)
	}
	if got := fake.SubIssueNumbers(shop, 12); !slices.Equal(got, []int64{41}) {
		t.Errorf("sub-issues of the Workstream = %v", got)
	}
	waitForPolls(t, fake)
	var root, bucket int64
	err := server.DB.QueryRow(`SELECT i.parent IS NULL, coalesce(json_extract(i.metadata, '$.github.bucket'), 0)
		FROM issues i JOIN workstreams w ON w.id = i.workstream WHERE w.repository = ? AND w.number = 12 AND i.number = 51`, shop).Scan(&root, &bucket)
	if err != nil || root != 1 || bucket != 41 {
		t.Errorf("row of #51: no parent = %d, bucket = %d, %v", root, bucket, err)
	}
	var pending int
	if err := server.DB.QueryRow("SELECT count(*) FROM incoming_changes WHERE status = 'pending'").Scan(&pending); err != nil || pending != 0 {
		t.Errorf("pending incoming changes = %d, %v", pending, err)
	}
}

func TestAClosedBucketThatGitHubDoesNotLinkAtFirstStaysPendingAndTheNextPollLinksItAndMakesNoSecondClosedBucket(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	addSubIssues(fake, 12, 100, 150, 101)
	fake.FailAddSubIssue(shop, 12, true)
	server := startCopied(t, fake)

	waitForPolls(t, fake)
	if state, _ := fake.State(shop, 151); state != "closed" || fake.HasIssue(shop, 152) {
		t.Fatalf("state of #151 = %q, #152 = %t", state, fake.HasIssue(shop, 152))
	}
	if got, want := bucketEntries(t, server), "151:closed:1"; got != want {
		t.Errorf("buckets = %q, want %q", got, want)
	}

	fake.FailAddSubIssue(shop, 12, false)

	waitForThirtyDirectSubIssues(t, fake)
	if got := fake.SubIssueNumbers(shop, 151); !slices.Equal(got, []int64{100}) || fake.HasIssue(shop, 153) {
		t.Errorf("sub-issues of #151 = %v, #153 = %t", got, fake.HasIssue(shop, 153))
	}
}

func TestAMoveOfASubIssueFromAnotherRepositoryMarksThatIssueAndNotTheIssueWithTheSameNumberInTheWorkstreamRepository(t *testing.T) {
	t.Parallel()
	fake := testkit.NewFakeGitHub(t)
	addBucketWorkstream(fake)
	fake.AddRepository(garden)
	fake.AddIssue(garden, 7, "Water the plants")
	fake.AddForeignSubIssue(shop, 12, garden, 7)
	fake.AddIssue(shop, 13, "Billing")
	fake.AddLabel(shop, 13, "mobius:workstream", "owner")
	fake.AddIssue(shop, 7, "Add invoices")
	fake.AddSubIssue(shop, 13, 7)
	addSubIssues(fake, 12, 100, 150, 100)

	server := startCopied(t, fake)

	waitForThirtyDirectSubIssues(t, fake)
	bucketOf := func(repository string) int64 {
		var bucket int64
		err := server.DB.QueryRow(`SELECT coalesce(json_extract(metadata, '$.github.bucket'), 0) FROM issues WHERE repository = ? AND number = 7`, repository).Scan(&bucket)
		if err != nil {
			t.Fatal(err)
		}
		return bucket
	}
	testkit.WaitFor(t, func() bool { return bucketOf(garden) != 0 })
	if got := bucketOf(shop); got != 0 {
		t.Errorf("bucket of owner/shop#7 = %d", got)
	}
	var own int
	err := server.DB.QueryRow(`SELECT count(*) FROM own_writes WHERE field = 'bucket' AND number = 7 AND repository = ?`, garden).Scan(&own)
	if err != nil || own != 1 {
		t.Errorf("own writes of owner/garden#7 = %d, %v", own, err)
	}
	err = server.DB.QueryRow(`SELECT count(*) FROM own_writes WHERE field = 'bucket' AND number = 7 AND repository = ?`, shop).Scan(&own)
	if err != nil || own != 0 {
		t.Errorf("own writes of owner/shop#7 = %d, %v", own, err)
	}
}
