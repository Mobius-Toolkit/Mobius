package engine_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Mobius-Toolkit/Mobius/internal/testkit"
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
		return len(got) == len(want) && got[0].Number == 123 && reflect.DeepEqual(sortedLines(got), want)
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
		return state == "closed"
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

	waitForThirtyDirectSubIssues(t, fake)
	if got := sorted(fake.SubIssueNumbers(shop, 151)); !slices.Equal(got, numbers(100, 121)) || fake.HasIssue(shop, 152) {
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
