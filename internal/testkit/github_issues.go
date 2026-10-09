package testkit

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

type issueKey struct {
	repository string
	number     int64
}

type issue struct {
	title  string
	body   string
	author string
	state  string
	// stateReason is the reason of the last close, or "".
	stateReason string
	pullRequest bool
	merged      bool
	labels      []string
	updatedAt   int64
	// A sub-issue can live in another repository than its parent.
	subIssues []issueKey
	// The numbers of the blockers. Each blocker is in the repository of the issue.
	blockedBy        []int64
	events           []eventJSON
	comments         []commentJSON
	reviews          []reviewJSON
	reviewComments   []reviewCommentJSON
	submittedReviews []SubmittedReview
}

type loginJSON struct {
	Login string `json:"login"`
}

type nameJSON struct {
	Name string `json:"name"`
}

type eventJSON struct {
	Event     string    `json:"event"`
	Actor     loginJSON `json:"actor"`
	Label     nameJSON  `json:"label"`
	CreatedAt string    `json:"created_at"`
}

type commentJSON struct {
	ID        int64     `json:"id"`
	IssueURL  string    `json:"issue_url"`
	User      loginJSON `json:"user"`
	Body      string    `json:"body"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
	// PerformedViaGitHubApp is the App of a comment that a user wrote through that App, for example the Lead as the Owner.
	PerformedViaGitHubApp *slugJSON `json:"performed_via_github_app,omitempty"`
}

type slugJSON struct {
	Slug string `json:"slug"`
}

// hold makes a request for the events of an issue wait.
type hold struct {
	// reached closes when the request arrives.
	reached chan struct{}
	// release makes the request go on when it closes.
	release chan struct{}
}

type issueJSON struct {
	ID            int64      `json:"id"`
	Number        int64      `json:"number"`
	Title         string     `json:"title"`
	Body          string     `json:"body"`
	User          loginJSON  `json:"user"`
	HTMLURL       string     `json:"html_url"`
	RepositoryURL string     `json:"repository_url"`
	State         string     `json:"state"`
	UpdatedAt     string     `json:"updated_at"`
	Labels        []nameJSON `json:"labels"`
	PullRequest   *urlJSON   `json:"pull_request,omitempty"`
	// GitHub gives no dependency summary for a pull request.
	IssueDependenciesSummary *dependenciesJSON `json:"issue_dependencies_summary,omitempty"`
}

type urlJSON struct {
	URL string `json:"url"`
}

type dependenciesJSON struct {
	// BlockedBy counts the open blockers only.
	BlockedBy      int `json:"blocked_by"`
	TotalBlockedBy int `json:"total_blocked_by"`
}

// Comment is a comment of an issue or a pull request.
type Comment struct {
	Author string
	Body   string
}

// AddIssue adds the open issue number with title and the author "owner".
func (g *FakeGitHub) AddIssue(repository string, number int64, title string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.insertIssue(issueKey{repository, number}, title, "", "owner")
}

// HasIssue tells if the repository has the issue or the pull request number.
func (g *FakeGitHub) HasIssue(repository string, number int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.issues[issueKey{repository, number}]
	return ok
}

// AddPullRequest adds the open pull request number with title and the author "owner".
func (g *FakeGitHub) AddPullRequest(repository string, number int64, title string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := issueKey{repository, number}
	g.insertIssue(key, title, "", "owner")
	g.issues[key].pullRequest = true
}

// AddSubIssue makes child a sub-issue of parent. Both issues are in repository. The change of the link
// changes the update time of no issue.
func (g *FakeGitHub) AddSubIssue(repository string, parent, child int64) {
	g.AddForeignSubIssue(repository, parent, repository, child)
}

// AddForeignSubIssue makes the issue child of childRepository a sub-issue of parent in repository.
func (g *FakeGitHub) AddForeignSubIssue(repository string, parent int64, childRepository string, child int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	found := g.issues[issueKey{repository, parent}]
	found.subIssues = append(found.subIssues, issueKey{childRepository, child})
}

// AddSubIssueOf adds the open issue number with title below parent in one step, so a poll sees both.
func (g *FakeGitHub) AddSubIssueOf(repository string, parent, number int64, title string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := issueKey{repository, number}
	g.insertIssue(key, title, "", "owner")
	found := g.issues[issueKey{repository, parent}]
	found.subIssues = append(found.subIssues, key)
}

// AddBlockedBy makes blocker a blocker of the issue number. Both issues are in repository.
func (g *FakeGitHub) AddBlockedBy(repository string, number, blocker int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	found := g.issues[issueKey{repository, number}]
	found.blockedBy = append(found.blockedBy, blocker)
}

// SetAuthor sets the author of the issue.
func (g *FakeGitHub) SetAuthor(repository string, number int64, author string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	found := g.issues[issueKey{repository, number}]
	found.author = author
	found.updatedAt = g.tick()
}

// SetBody sets the body of the issue.
func (g *FakeGitHub) SetBody(repository string, number int64, body string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	found := g.issues[issueKey{repository, number}]
	found.body = body
	found.updatedAt = g.tick()
}

// SetTitle sets the title of the issue.
func (g *FakeGitHub) SetTitle(repository string, number int64, title string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	found := g.issues[issueKey{repository, number}]
	found.title = title
	found.updatedAt = g.tick()
}

// CloseIssue closes the issue as the owner, with no reason.
func (g *FakeGitHub) CloseIssue(repository string, number int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.setState(issueKey{repository, number}, "closed", "", "owner")
}

// ReopenIssue opens the closed issue again as the owner.
func (g *FakeGitHub) ReopenIssue(repository string, number int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.setState(issueKey{repository, number}, "open", "", "owner")
}

// State gives the state of the issue and the reason of its last close.
func (g *FakeGitHub) State(repository string, number int64) (string, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	found := g.issues[issueKey{repository, number}]
	return found.state, found.stateReason
}

// FailClose makes each close of the issue fail.
func (g *FakeGitHub) FailClose(repository string, number int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failedCloses[issueKey{repository, number}] = true
}

// FailSubIssues makes each read of the sub-issues of the issue fail, or work again when fail is false.
func (g *FakeGitHub) FailSubIssues(repository string, number int64, fail bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failedSubIssues[issueKey{repository, number}] = fail
}

// FailParents makes each read of the parent of the issue fail, or work again when fail is false.
func (g *FakeGitHub) FailParents(repository string, number int64, fail bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failedParents[issueKey{repository, number}] = fail
}

// FailAddComment makes each new comment that Mobius writes on the issue fail, or work again when fail is false.
func (g *FakeGitHub) FailAddComment(repository string, number int64, fail bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failedComments[issueKey{repository, number}] = fail
}

// Issue gives the title and the body of the issue.
func (g *FakeGitHub) Issue(repository string, number int64) (string, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	found := g.issues[issueKey{repository, number}]
	return found.title, found.body
}

// SubIssueNumbers gives the numbers of the sub-issues of the issue.
func (g *FakeGitHub) SubIssueNumbers(repository string, number int64) []int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	var numbers []int64
	for _, child := range g.issues[issueKey{repository, number}].subIssues {
		numbers = append(numbers, child.number)
	}
	return numbers
}

// BlockerNumbers gives the numbers of the blockers of the issue.
func (g *FakeGitHub) BlockerNumbers(repository string, number int64) []int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.issues[issueKey{repository, number}].blockedBy)
}

// Comments gives the comments of the issue.
func (g *FakeGitHub) Comments(repository string, number int64) []Comment {
	g.mu.Lock()
	defer g.mu.Unlock()
	var comments []Comment
	for _, comment := range g.issues[issueKey{repository, number}].comments {
		comments = append(comments, Comment{comment.User.Login, comment.Body})
	}
	return comments
}

// AddComment adds a comment of author to the issue, and gives the id of the comment.
func (g *FakeGitHub) AddComment(repository string, number int64, author, body string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.comment(issueKey{repository, number}, author, body).ID
}

// AddCommentInLastSecond adds a comment of author to the issue with the time of the last write, and gives the id of
// the comment. GitHub writes two comments in one second, and `since` has a resolution of one second.
func (g *FakeGitHub) AddCommentInLastSecond(repository string, number int64, author, body string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.commentAt(issueKey{repository, number}, author, body, g.clock).ID
}

// EditComment replaces the body of the comment id of an issue of the repository. It gives false when the repository
// has no such comment.
func (g *FakeGitHub) EditComment(repository string, id int64, body string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.editComment(repository, id, body)
	return ok
}

// CommentReads gives the number of reads of the comments of one issue or pull request, and the number of reads of the
// comments of a whole repository.
func (g *FakeGitHub) CommentReads() (single, repositories int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.singleCommentReads, g.repositoryCommentReads
}

// listedComment is a comment that the next issue list adds after it builds its page. The list must have the issue of
// the comment, or it must be the next list of the repository when anyList is true.
type listedComment struct {
	key          issueKey
	author, body string
	anyList      bool
}

// AddCommentAfterList adds a comment to the issue number after the next issue list that has the issue. The page of
// that list shows the issue without the comment, but a later read of the comments gives the comment.
func (g *FakeGitHub) AddCommentAfterList(repository string, number int64, author, body string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.commentsAfterList = append(g.commentsAfterList, listedComment{key: issueKey{repository, number}, author: author, body: body})
}

// AddCommentAfterNextList adds a comment to the issue number after the next issue list of the repository, also when
// the page of that list has no such issue.
func (g *FakeGitHub) AddCommentAfterNextList(repository string, number int64, author, body string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.commentsAfterList = append(g.commentsAfterList, listedComment{key: issueKey{repository, number}, author: author, body: body, anyList: true})
}

// AddAppComment adds a comment with body of author to the issue number, written through the Mobius App, as the gh of
// the Lead writes it. It gives the id of the comment.
func (g *FakeGitHub) AddAppComment(repository string, number int64, author, body string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := issueKey{repository, number}
	g.comment(key, author, body)
	comments := g.issues[key].comments
	comments[len(comments)-1].PerformedViaGitHubApp = &slugJSON{AppSlug}
	return comments[len(comments)-1].ID
}

// AddLabel adds label to the issue as actor.
func (g *FakeGitHub) AddLabel(repository string, number int64, label, actor string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.label(issueKey{repository, number}, label, actor)
}

// RemoveLabel removes label from the issue as actor. The issue must have the label.
func (g *FakeGitHub) RemoveLabel(repository string, number int64, label, actor string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.unlabel(issueKey{repository, number}, label, actor) {
		panic(fmt.Sprintf("%s#%d has no label %s", repository, number, label))
	}
}

// LabelActor gives the actor of the last labeled or unlabeled event of label, or "".
func (g *FakeGitHub) LabelActor(repository string, number int64, label string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	events := g.issues[issueKey{repository, number}].events
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Label.Name == label {
			return events[i].Actor.Login
		}
	}
	return ""
}

// Labels gives the labels of the issue, in the order of their addition.
func (g *FakeGitHub) Labels(repository string, number int64) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.issues[issueKey{repository, number}].labels)
}

func (g *FakeGitHub) insertIssue(key issueKey, title, body, author string) {
	g.issues[key] = &issue{title: title, body: body, author: author, state: "open", updatedAt: g.tick()}
}

func (g *FakeGitHub) label(key issueKey, label, actor string) {
	found := g.issues[key]
	// GitHub records no `labeled` event for a label that the issue has.
	if slices.Contains(found.labels, label) {
		return
	}
	now := g.tick()
	found.labels = append(found.labels, label)
	found.updatedAt = now
	found.events = append(found.events, eventJSON{Event: "labeled", Actor: loginJSON{actor}, Label: nameJSON{label}, CreatedAt: timestamp(now)})
}

func (g *FakeGitHub) unlabel(key issueKey, label, actor string) bool {
	found := g.issues[key]
	if !slices.Contains(found.labels, label) {
		return false
	}
	now := g.tick()
	found.labels = slices.DeleteFunc(found.labels, func(name string) bool { return name == label })
	found.updatedAt = now
	found.events = append(found.events, eventJSON{Event: "unlabeled", Actor: loginJSON{actor}, Label: nameJSON{label}, CreatedAt: timestamp(now)})
	return true
}

func (g *FakeGitHub) setState(key issueKey, state, reason, actor string) {
	now := g.tick()
	found := g.issues[key]
	found.state = state
	found.stateReason = reason
	found.updatedAt = now
	event := "closed"
	if state == "open" {
		event = "reopened"
	}
	found.events = append(found.events, eventJSON{Event: event, Actor: loginJSON{actor}, CreatedAt: timestamp(now)})
}

func (g *FakeGitHub) issueJSON(key issueKey) issueJSON {
	found := g.issues[key]
	labels := []nameJSON{}
	for _, label := range found.labels {
		labels = append(labels, nameJSON{label})
	}
	result := issueJSON{
		ID:            key.number + issueIDOffset,
		Number:        key.number,
		Title:         found.title,
		Body:          found.body,
		User:          loginJSON{found.author},
		HTMLURL:       fmt.Sprintf("https://github.com/%s/issues/%d", key.repository, key.number),
		RepositoryURL: "https://api.github.com/repos/" + key.repository,
		State:         found.state,
		UpdatedAt:     timestamp(found.updatedAt),
		Labels:        labels,
	}
	if found.pullRequest {
		result.PullRequest = &urlJSON{fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", key.repository, key.number)}
		return result
	}
	summary := &dependenciesJSON{TotalBlockedBy: len(found.blockedBy)}
	for _, blocker := range found.blockedBy {
		if g.issues[issueKey{key.repository, blocker}].state == "open" {
			summary.BlockedBy++
		}
	}
	result.IssueDependenciesSummary = summary
	return result
}

func repository(r *http.Request) string {
	return r.PathValue("owner") + "/" + r.PathValue("repo")
}

// issue gives the key of the issue in the path of r, when the issue exists. The caller must hold the lock.
func (g *FakeGitHub) issue(w http.ResponseWriter, r *http.Request) (issueKey, bool) {
	number, err := strconv.ParseInt(r.PathValue("number"), 10, 64)
	key := issueKey{repository(r), number}
	if _, ok := g.issues[key]; err != nil || !ok {
		notFound(w)
		return key, false
	}
	return key, true
}

// listIssues gives the issues in the order of their last change. It gives status 304
// with no body when If-None-Match has the ETag of the result, and counts each 304.
func (g *FakeGitHub) listIssues(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var since int64
	if query.Has("since") {
		parsed, err := time.Parse(time.RFC3339, query.Get("since"))
		if err != nil {
			message(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		since = parsed.Unix()
	}
	state := query.Get("state")
	if state == "" {
		state = "open"
	}
	var labels []string
	if query.Has("labels") {
		labels = strings.Split(query.Get("labels"), ",")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var keys []issueKey
	for key, found := range g.issues {
		if key.repository == repository(r) &&
			(state == "all" || state == found.state) &&
			found.updatedAt >= since &&
			!slices.ContainsFunc(labels, func(label string) bool { return !slices.Contains(found.labels, label) }) {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b issueKey) int {
		return cmp.Or(cmp.Compare(g.issues[a].updatedAt, g.issues[b].updatedAt), cmp.Compare(a.number, b.number))
	})
	issues := []issueJSON{}
	for _, key := range keys {
		issues = append(issues, g.issueJSON(key))
	}
	body, err := json.Marshal(page(w, r, issues))
	if err != nil {
		message(w, http.StatusInternalServerError, err.Error())
		return
	}
	sum := sha256.Sum256(body)
	etag := fmt.Sprintf(`"%x"`, sum[:8])
	if r.Header.Get("If-None-Match") == etag {
		g.notModified++
		w.WriteHeader(http.StatusNotModified)
		return
	}
	g.commentsAfterList = slices.DeleteFunc(g.commentsAfterList, func(pending listedComment) bool {
		if !pending.anyList && !slices.Contains(keys, pending.key) || pending.key.repository != repository(r) {
			return false
		}
		g.comment(pending.key, pending.author, pending.body)
		return true
	})
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// NotModifiedCount gives the number of issue list requests that got status 304.
func (g *FakeGitHub) NotModifiedCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.notModified
}

// createIssue creates the issue with the next free number. The token owner is the author.
func (g *FakeGitHub) createIssue(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if !decode(w, r, &request) {
		return
	}
	caller, _ := g.validToken(r)
	g.mu.Lock()
	defer g.mu.Unlock()
	key := issueKey{repository(r), 1}
	for other := range g.issues {
		if other.repository == key.repository {
			key.number = max(key.number, other.number+1)
		}
	}
	g.insertIssue(key, request.Title, request.Body, caller.login)
	writeJSON(w, http.StatusCreated, g.issueJSON(key))
}

// closeIssue closes the issue or the pull request of the path as the token owner, with the reason of the body.
// It takes no other change.
func (g *FakeGitHub) closeIssue(w http.ResponseWriter, r *http.Request) {
	var request struct {
		State       string `json:"state"`
		StateReason string `json:"state_reason"`
	}
	if !decode(w, r, &request) {
		return
	}
	caller, _ := g.validToken(r)
	g.mu.Lock()
	defer g.mu.Unlock()
	key, ok := g.issue(w, r)
	if !ok {
		return
	}
	if request.State != "closed" {
		message(w, http.StatusUnprocessableEntity, "The fake GitHub only closes.")
		return
	}
	if g.failedCloses[key] {
		message(w, http.StatusInternalServerError, "Server Error")
		return
	}
	g.setState(key, "closed", request.StateReason, caller.login)
	writeJSON(w, http.StatusOK, g.issueJSON(key))
}

// HoldIssue makes the next request for the issue number wait until a call of release. The channel closes when the
// request arrives.
func (g *FakeGitHub) HoldIssue(repository string, number int64) (<-chan struct{}, func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h := &hold{reached: make(chan struct{}), release: make(chan struct{})}
	g.issueHolds[issueKey{repository, number}] = h
	return h.reached, func() { close(h.release) }
}

func (g *FakeGitHub) getIssue(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	number, _ := strconv.ParseInt(r.PathValue("number"), 10, 64)
	key := issueKey{repository(r), number}
	h := g.issueHolds[key]
	delete(g.issueHolds, key)
	g.mu.Unlock()
	if h != nil {
		close(h.reached)
		<-h.release
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if key, ok := g.issue(w, r); ok {
		writeJSON(w, http.StatusOK, g.issueJSON(key))
	}
}

func (g *FakeGitHub) subIssues(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key, ok := g.issue(w, r)
	if !ok {
		return
	}
	if g.failedSubIssues[key] {
		message(w, http.StatusInternalServerError, "Server Error")
		return
	}
	children := []issueJSON{}
	for _, child := range g.issues[key].subIssues {
		children = append(children, g.issueJSON(child))
	}
	writeJSON(w, http.StatusOK, page(w, r, children))
}

// parent gives the issue in the same repository that has the issue as a sub-issue.
func (g *FakeGitHub) parent(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key, ok := g.issue(w, r)
	if !ok {
		return
	}
	if g.failedParents[key] {
		message(w, http.StatusInternalServerError, "Server Error")
		return
	}
	for other, found := range g.issues {
		if other.repository == key.repository && slices.Contains(found.subIssues, key) {
			writeJSON(w, http.StatusOK, g.issueJSON(other))
			return
		}
	}
	notFound(w)
}

// addSubIssue moves the issue with the id sub_issue_id below the issue of the path. Both issues are in the repository of the path.
func (g *FakeGitHub) addSubIssue(w http.ResponseWriter, r *http.Request) {
	var request struct {
		SubIssueID    int64 `json:"sub_issue_id"`
		ReplaceParent bool  `json:"replace_parent"`
	}
	if !decode(w, r, &request) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	key, ok := g.issue(w, r)
	if !ok {
		return
	}
	child := issueKey{key.repository, request.SubIssueID - issueIDOffset}
	if _, ok := g.issues[child]; !ok {
		notFound(w)
		return
	}
	for other, found := range g.issues {
		if other.repository != key.repository || !slices.Contains(found.subIssues, child) {
			continue
		}
		if !request.ReplaceParent {
			message(w, http.StatusUnprocessableEntity, "The issue already has a parent.")
			return
		}
		found.subIssues = slices.DeleteFunc(found.subIssues, func(sub issueKey) bool { return sub == child })
	}
	g.issues[key].subIssues = append(g.issues[key].subIssues, child)
	writeJSON(w, http.StatusCreated, g.issueJSON(key))
}

func (g *FakeGitHub) blockedBy(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key, ok := g.issue(w, r)
	if !ok {
		return
	}
	blockers := []issueJSON{}
	for _, blocker := range g.issues[key].blockedBy {
		blockers = append(blockers, g.issueJSON(issueKey{key.repository, blocker}))
	}
	writeJSON(w, http.StatusOK, page(w, r, blockers))
}

// addBlockedBy makes the issue with the id issue_id a blocker of the issue of the path. Both issues are in the repository of the path.
func (g *FakeGitHub) addBlockedBy(w http.ResponseWriter, r *http.Request) {
	var request struct {
		IssueID int64 `json:"issue_id"`
	}
	if !decode(w, r, &request) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	key, ok := g.issue(w, r)
	if !ok {
		return
	}
	blocker := issueKey{key.repository, request.IssueID - issueIDOffset}
	if _, ok := g.issues[blocker]; !ok {
		notFound(w)
		return
	}
	found := g.issues[key]
	found.blockedBy = append(found.blockedBy, blocker.number)
	found.updatedAt = g.tick()
	writeJSON(w, http.StatusCreated, g.issueJSON(blocker))
}

// HoldIssueEvents makes the next request for the events of the issue number wait until a call of release. The channel
// closes when the request arrives.
func (g *FakeGitHub) HoldIssueEvents(repository string, number int64) (<-chan struct{}, func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h := &hold{reached: make(chan struct{}), release: make(chan struct{})}
	g.holds[issueKey{repository, number}] = h
	return h.reached, func() { close(h.release) }
}

func (g *FakeGitHub) issueEvents(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	number, _ := strconv.ParseInt(r.PathValue("number"), 10, 64)
	key := issueKey{repository(r), number}
	h := g.holds[key]
	delete(g.holds, key)
	g.mu.Unlock()
	if h != nil {
		close(h.reached)
		<-h.release
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if key, ok := g.issue(w, r); ok {
		writeJSON(w, http.StatusOK, page(w, r, g.issues[key].events))
	}
}

func (g *FakeGitHub) issueComments(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.singleCommentReads++
	if key, ok := g.issue(w, r); ok {
		writeJSON(w, http.StatusOK, page(w, r, g.issues[key].comments))
	}
}

// repositoryIssueComments lists the comments of all issues and pull requests of the repository that changed at or
// after `since`.
func (g *FakeGitHub) repositoryIssueComments(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.repositoryCommentReads++
	comments := []commentJSON{}
	for key, found := range g.issues {
		if key.repository == repository(r) {
			comments = append(comments, found.comments...)
		}
	}
	comments = changedComments(r, comments, func(comment commentJSON) (int64, string) { return comment.ID, comment.UpdatedAt })
	writeJSON(w, http.StatusOK, page(w, r, comments))
}

// changedComments keeps the comments with an update time at or after `since`, and sorts them by `sort` and `direction`.
// The default sort is created, and the default direction is asc, as on GitHub. fields gives the id and the update time
// of a comment.
func changedComments[T any](r *http.Request, comments []T, fields func(T) (int64, string)) []T {
	query := r.URL.Query()
	if since := query.Get("since"); since != "" {
		comments = slices.DeleteFunc(comments, func(comment T) bool {
			_, updatedAt := fields(comment)
			return updatedAt < since
		})
	}
	slices.SortFunc(comments, func(a, b T) int {
		aID, aUpdatedAt := fields(a)
		bID, bUpdatedAt := fields(b)
		if query.Get("sort") == "updated" {
			return cmp.Or(cmp.Compare(aUpdatedAt, bUpdatedAt), cmp.Compare(aID, bID))
		}
		return cmp.Compare(aID, bID)
	})
	if query.Get("direction") == "desc" {
		slices.Reverse(comments)
	}
	return comments
}

// addComment adds the comment as the token owner: the user of a user token, or the App bot of an installation token.
func (g *FakeGitHub) addComment(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &request) {
		return
	}
	caller, _ := g.validToken(r)
	g.mu.Lock()
	defer g.mu.Unlock()
	key, ok := g.issue(w, r)
	if !ok {
		return
	}
	if g.failedComments[key] {
		message(w, http.StatusInternalServerError, "Server Error")
		return
	}
	writeJSON(w, http.StatusCreated, g.comment(key, caller.login, request.Body))
}

// updateComment replaces the body of the comment of an issue of the repository.
func (g *FakeGitHub) updateComment(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &request) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	g.mu.Lock()
	defer g.mu.Unlock()
	if comment, ok := g.editComment(repository(r), id, request.Body); ok {
		writeJSON(w, http.StatusOK, comment)
		return
	}
	notFound(w)
}

// editComment replaces the body of the comment id of an issue of the repository, and sets its update time. The caller
// must hold the lock.
func (g *FakeGitHub) editComment(repository string, id int64, body string) (commentJSON, bool) {
	for key, found := range g.issues {
		if key.repository != repository {
			continue
		}
		for i := range found.comments {
			if found.comments[i].ID == id {
				now := g.tick()
				found.comments[i].Body = body
				found.comments[i].UpdatedAt = timestamp(now)
				found.updatedAt = now
				return found.comments[i], true
			}
		}
	}
	return commentJSON{}, false
}

// comment adds a comment of author to the issue. The caller must hold the lock.
func (g *FakeGitHub) comment(key issueKey, author, body string) commentJSON {
	return g.commentAt(key, author, body, g.tick())
}

// commentAt adds a comment of author to the issue at the time now, in seconds after the Unix epoch. The caller must
// hold the lock.
func (g *FakeGitHub) commentAt(key issueKey, author, body string, now int64) commentJSON {
	found := g.issues[key]
	g.lastCommentID++
	comment := commentJSON{
		ID:        g.lastCommentID,
		IssueURL:  fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", key.repository, key.number),
		User:      loginJSON{author},
		Body:      body,
		CreatedAt: timestamp(now),
		UpdatedAt: timestamp(now),
	}
	found.comments = append(found.comments, comment)
	found.updatedAt = now
	return comment
}

// labelNames is the body of a label addition. GitHub takes an array of names or an object with the field labels.
type labelNames []string

func (l *labelNames) UnmarshalJSON(data []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		return json.Unmarshal(data, (*[]string)(l))
	}
	var object struct {
		Labels []string `json:"labels"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&object); err != nil {
		return err
	}
	*l = object.Labels
	return nil
}

// addLabels adds the labels as the token owner: the user of a user token, or the App bot of an installation token.
func (g *FakeGitHub) addLabels(w http.ResponseWriter, r *http.Request) {
	var labels labelNames
	if !decode(w, r, &labels) {
		return
	}
	caller, _ := g.validToken(r)
	g.mu.Lock()
	defer g.mu.Unlock()
	key, ok := g.issue(w, r)
	if !ok {
		return
	}
	for _, label := range labels {
		g.label(key, label, caller.login)
	}
	writeJSON(w, http.StatusOK, g.issueJSON(key).Labels)
}

func (g *FakeGitHub) removeLabel(w http.ResponseWriter, r *http.Request) {
	caller, _ := g.validToken(r)
	g.mu.Lock()
	defer g.mu.Unlock()
	key, ok := g.issue(w, r)
	if !ok {
		return
	}
	if !g.unlabel(key, r.PathValue("name"), caller.login) {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, g.issueJSON(key).Labels)
}
