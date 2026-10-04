package testkit

import (
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
	title     string
	body      string
	author    string
	state     string
	labels    []string
	updatedAt int64
	// A sub-issue can live in another repository than its parent.
	subIssues []issueKey
	events    []eventJSON
	comments  []commentJSON
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
	User      loginJSON `json:"user"`
	Body      string    `json:"body"`
	CreatedAt string    `json:"created_at"`
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
}

// AddIssue adds the open issue number with title and the author "owner".
func (g *FakeGitHub) AddIssue(repository string, number int64, title string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.insertIssue(issueKey{repository, number}, title, "", "owner")
}

// AddSubIssue makes child a sub-issue of parent. Both issues are in repository.
func (g *FakeGitHub) AddSubIssue(repository string, parent, child int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	found := g.issues[issueKey{repository, parent}]
	found.subIssues = append(found.subIssues, issueKey{repository, child})
}

// AddComment adds a comment of author to the issue, and gives the id of the comment.
func (g *FakeGitHub) AddComment(repository string, number int64, author, body string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.tick()
	found := g.issues[issueKey{repository, number}]
	g.lastCommentID++
	found.comments = append(found.comments, commentJSON{ID: g.lastCommentID, User: loginJSON{author}, Body: body, CreatedAt: timestamp(now)})
	found.updatedAt = now
	return g.lastCommentID
}

// AddLabel adds label to the issue as actor.
func (g *FakeGitHub) AddLabel(repository string, number int64, label, actor string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.label(issueKey{repository, number}, label, actor)
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

func (g *FakeGitHub) issueJSON(key issueKey) issueJSON {
	found := g.issues[key]
	labels := []nameJSON{}
	for _, label := range found.labels {
		labels = append(labels, nameJSON{label})
	}
	return issueJSON{
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

func (g *FakeGitHub) getIssue(w http.ResponseWriter, r *http.Request) {
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
	children := []issueJSON{}
	for _, child := range g.issues[key].subIssues {
		children = append(children, g.issueJSON(child))
	}
	writeJSON(w, http.StatusOK, page(w, r, children))
}

func (g *FakeGitHub) issueEvents(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if key, ok := g.issue(w, r); ok {
		writeJSON(w, http.StatusOK, page(w, r, g.issues[key].events))
	}
}

func (g *FakeGitHub) issueComments(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if key, ok := g.issue(w, r); ok {
		writeJSON(w, http.StatusOK, page(w, r, g.issues[key].comments))
	}
}

// addLabels adds the labels as the token owner: the user of a user token, or the App bot of an installation token.
func (g *FakeGitHub) addLabels(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Labels []string `json:"labels"`
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
	for _, label := range request.Labels {
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
