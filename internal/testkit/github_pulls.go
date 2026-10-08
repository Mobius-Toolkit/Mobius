package testkit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

type reviewJSON struct {
	ID          int64     `json:"id"`
	User        loginJSON `json:"user"`
	CommitID    string    `json:"commit_id"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	SubmittedAt string    `json:"submitted_at"`
}

type reviewCommentJSON struct {
	ID             int64     `json:"id"`
	PullRequestURL string    `json:"pull_request_url"`
	User           loginJSON `json:"user"`
	Body           string    `json:"body"`
	CreatedAt      string    `json:"created_at"`
	UpdatedAt      string    `json:"updated_at"`
	Path           string    `json:"path"`
	Line           int       `json:"line"`
	// InReplyToID is the id of the first comment of the thread. The first comment has no InReplyToID.
	InReplyToID int64 `json:"in_reply_to_id,omitempty"`
}

// SubmittedReview is a review that a client posted with its inline comments.
type SubmittedReview struct {
	CommitID string          `json:"commit_id"`
	Body     string          `json:"body"`
	Event    string          `json:"event"`
	Comments []InlineComment `json:"comments"`
}

// InlineComment is a comment of a submitted review on a line of a file.
type InlineComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

// Thread is a review thread of a pull request.
type Thread struct {
	Resolved bool
	Comments []Comment
}

// AddReview adds a submitted review of author with state, for example "APPROVED", to the pull request, and gives its
// id.
func (g *FakeGitHub) AddReview(repository string, number int64, author, state, body string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	found := g.issues[issueKey{repository, number}]
	g.lastReviewID++
	found.reviews = append(found.reviews, reviewJSON{ID: g.lastReviewID, User: loginJSON{author}, CommitID: g.headOf(repository, number), Body: body, State: state, SubmittedAt: timestamp(g.tick())})
	found.updatedAt = g.tick()
	return g.lastReviewID
}

// DismissReview sets the state of the review id of the pull request to DISMISSED.
func (g *FakeGitHub) DismissReview(repository string, number, id int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	found := g.issues[issueKey{repository, number}]
	for i := range found.reviews {
		if found.reviews[i].ID == id {
			found.reviews[i].State = "DISMISSED"
		}
	}
	found.updatedAt = g.tick()
}

// AddReviewComment adds a review comment of author on line 12 of src/plan.rs to the pull request, and gives its id.
// A reply has the id of the first comment of its thread in inReplyTo. The first comment has 0.
func (g *FakeGitHub) AddReviewComment(repository string, number, inReplyTo int64, author, body string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reviewComment(issueKey{repository, number}, inReplyTo, author, InlineComment{"src/plan.rs", 12, body}).ID
}

// ResolveReviewThread marks the review thread that starts with the comment root as resolved. The update time of the
// pull request does not change, as on GitHub.
func (g *FakeGitHub) ResolveReviewThread(root int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.resolvedThreads[root] = true
}

// UnresolveReviewThread marks the review thread that starts with the comment root as not resolved.
func (g *FakeGitHub) UnresolveReviewThread(root int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.resolvedThreads, root)
}

// SubmittedReviews gives the reviews that clients posted on the pull request, the oldest first.
func (g *FakeGitHub) SubmittedReviews(repository string, number int64) []SubmittedReview {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]SubmittedReview{}, g.issues[issueKey{repository, number}].submittedReviews...)
}

// ReviewThread gives the review thread of the pull request that starts with the comment root.
func (g *FakeGitHub) ReviewThread(repository string, number, root int64) Thread {
	g.mu.Lock()
	defer g.mu.Unlock()
	thread := Thread{Resolved: g.resolvedThreads[root]}
	for _, comment := range g.issues[issueKey{repository, number}].reviewComments {
		if comment.ID == root || comment.InReplyToID == root {
			thread.Comments = append(thread.Comments, Comment{comment.User.Login, comment.Body})
		}
	}
	return thread
}

// reviewComment adds a review comment. The comment ids of all issues are different. The caller must hold the lock.
func (g *FakeGitHub) reviewComment(key issueKey, inReplyTo int64, author string, inline InlineComment) reviewCommentJSON {
	now := g.tick()
	found := g.issues[key]
	g.lastCommentID++
	comment := reviewCommentJSON{
		ID:             g.lastCommentID,
		PullRequestURL: fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", key.repository, key.number),
		User:           loginJSON{author},
		Body:           inline.Body,
		CreatedAt:      timestamp(now),
		UpdatedAt:      timestamp(now),
		Path:           inline.Path,
		Line:           inline.Line,
		InReplyToID:    inReplyTo,
	}
	found.reviewComments = append(found.reviewComments, comment)
	found.updatedAt = now
	return comment
}

func (g *FakeGitHub) reviews(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if key, ok := g.issue(w, r); ok {
		writeJSON(w, http.StatusOK, page(w, r, append([]reviewJSON{}, g.issues[key].reviews...)))
	}
}

func (g *FakeGitHub) reviewComments(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.singleCommentReads++
	if key, ok := g.issue(w, r); ok {
		writeJSON(w, http.StatusOK, page(w, r, append([]reviewCommentJSON{}, g.issues[key].reviewComments...)))
	}
}

// repositoryReviewComments lists the review comments of all pull requests of the repository that changed at or after
// `since`.
func (g *FakeGitHub) repositoryReviewComments(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.repositoryCommentReads++
	comments := []reviewCommentJSON{}
	for key, found := range g.issues {
		if key.repository == repository(r) {
			comments = append(comments, found.reviewComments...)
		}
	}
	comments = changedComments(r, comments, func(comment reviewCommentJSON) (int64, string) { return comment.ID, comment.UpdatedAt })
	writeJSON(w, http.StatusOK, page(w, r, comments))
}

// replyToReviewComment adds a reply of the token owner to the thread that starts with the comment in_reply_to.
func (g *FakeGitHub) replyToReviewComment(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Body      string `json:"body"`
		InReplyTo int64  `json:"in_reply_to"`
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
	for _, comment := range g.issues[key].reviewComments {
		if comment.ID == request.InReplyTo && comment.InReplyToID == 0 {
			writeJSON(w, http.StatusCreated, g.reviewComment(key, request.InReplyTo, caller.login, InlineComment{comment.Path, comment.Line, request.Body}))
			return
		}
	}
	notFound(w)
}

// submitReview adds the review and its inline comments as the token owner.
func (g *FakeGitHub) submitReview(w http.ResponseWriter, r *http.Request) {
	var review SubmittedReview
	if !decode(w, r, &review) {
		return
	}
	caller, _ := g.validToken(r)
	g.mu.Lock()
	defer g.mu.Unlock()
	key, ok := g.issue(w, r)
	if !ok {
		return
	}
	found := g.issues[key]
	g.lastReviewID++
	found.reviews = append(found.reviews, reviewJSON{ID: g.lastReviewID, User: loginJSON{caller.login}, CommitID: review.CommitID, Body: review.Body, State: "COMMENTED", SubmittedAt: timestamp(g.tick())})
	for _, comment := range review.Comments {
		g.reviewComment(key, 0, caller.login, comment)
	}
	found.submittedReviews = append(found.submittedReviews, review)
	writeJSON(w, http.StatusOK, map[string]any{"id": len(found.submittedReviews)})
}

type graphqlThread struct {
	ID         string `json:"id"`
	IsResolved bool   `json:"isResolved"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Comments   any    `json:"comments"`
}

type graphqlComment struct {
	DatabaseID int64             `json:"databaseId"`
	Author     map[string]string `json:"author"`
	Body       string            `json:"body"`
	CreatedAt  string            `json:"createdAt"`
}

// HoldReviewThreads makes the next request for the reviews and the review threads of the pull request wait. A second
// call makes the request after it wait too. The channel closes when the request arrives, and the function lets the
// request go on.
func (g *FakeGitHub) HoldReviewThreads(repository string, number int64) (<-chan struct{}, func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	h := &hold{reached: make(chan struct{}), release: make(chan struct{})}
	key := issueKey{repository, number}
	g.threadHolds[key] = append(g.threadHolds[key], h)
	return h.reached, func() { close(h.release) }
}

// SetGraphQLPageSize makes each connection of the GraphQL answers give at most size nodes in a page.
func (g *FakeGitHub) SetGraphQLPageSize(size int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.graphqlPageSize = size
}

// PullRequestReads gives the number of GraphQL queries for the reviews and the review threads of pull requests. A
// query for the next page of a connection is not one of them.
func (g *FakeGitHub) PullRequestReads() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pullRequestReads
}

var pullRequestAlias = regexp.MustCompile(`pr(\d+): pullRequest\(number: (\d+)\)`)

// graphql answers the query of the reviews and the review threads of pull requests, the query of the next page of a
// connection, and the mutations resolveReviewThread, markPullRequestReadyForReview and convertPullRequestToDraft.
// The node id of a thread is "RT_" and the id of its first comment, the node id of a review is "RV_" and its id, and
// the node id of a pull request is "PR_" and its number.
func (g *FakeGitHub) graphql(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Query     string `json:"query"`
		Variables struct {
			ID    string `json:"id"`
			Owner string `json:"owner"`
			Name  string `json:"name"`
			After string `json:"after"`
		} `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		message(w, http.StatusBadRequest, err.Error())
		return
	}
	variables := request.Variables
	repository := variables.Owner + "/" + variables.Name
	aliases := pullRequestAlias.FindAllStringSubmatch(request.Query, -1)
	var holds []*hold
	g.mu.Lock()
	for _, alias := range aliases {
		number, _ := strconv.ParseInt(alias[2], 10, 64)
		key := issueKey{repository, number}
		if queue := g.threadHolds[key]; len(queue) > 0 {
			holds = append(holds, queue[0])
			g.threadHolds[key] = queue[1:]
		}
	}
	g.mu.Unlock()
	for _, h := range holds {
		close(h.reached)
		<-h.release
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case strings.Contains(request.Query, "markPullRequestReadyForReview"):
		g.markReadyForReview(w, variables.ID)
	case strings.Contains(request.Query, "convertPullRequestToDraft"):
		g.convertToDraft(w, variables.ID)
	case strings.Contains(request.Query, "resolveReviewThread"):
		root, err := strconv.ParseInt(strings.TrimPrefix(variables.ID, "RT_"), 10, 64)
		if err != nil || !strings.HasPrefix(variables.ID, "RT_") {
			writeJSON(w, http.StatusOK, map[string]any{"data": nil, "errors": []map[string]string{{"message": "Could not resolve to a node"}}})
			return
		}
		g.resolvedThreads[root] = true
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"resolveReviewThread": map[string]any{"clientMutationId": nil}}})
	case strings.Contains(request.Query, "node(id:"):
		g.nextPage(w, request.Query, variables.ID, variables.After)
	default:
		g.pullRequestReads++
		pulls := map[string]any{}
		for _, alias := range aliases {
			number, _ := strconv.ParseInt(alias[2], 10, 64)
			key := issueKey{repository, number}
			pulls["pr"+alias[1]] = g.pullRequestNode(key)
			if found, ok := g.issues[key]; ok && len(found.reviews) > 0 {
				g.reviewRead = true
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"repository": pulls}})
	}
}

// pullRequestNode gives the node of the pull request, or nil when the repository has no such pull request. The caller
// must hold the lock.
func (g *FakeGitHub) pullRequestNode(key issueKey) map[string]any {
	found, ok := g.issues[key]
	if !ok || !found.pullRequest {
		return nil
	}
	return map[string]any{
		"id":            fmt.Sprintf("PR_%d", key.number),
		"reviews":       g.connection(g.reviewNodes(found), ""),
		"reviewThreads": g.connection(g.threadNodes(found), ""),
	}
}

// nextPage answers the query of a connection of the node id, from the cursor after. The caller must hold the lock.
func (g *FakeGitHub) nextPage(w http.ResponseWriter, query, id, after string) {
	var page map[string]any
	switch {
	case strings.Contains(query, "... on PullRequestReviewThread"):
		for _, found := range g.issues {
			for _, thread := range g.threadNodes(found) {
				if thread.ID == id {
					page = g.connection(thread.comments, after)
				}
			}
		}
	case strings.Contains(query, "... on PullRequest {"):
		number, _ := strconv.ParseInt(strings.TrimPrefix(id, "PR_"), 10, 64)
		for key, found := range g.issues {
			if key.number != number || !found.pullRequest {
				continue
			}
			if strings.Contains(query, "page: reviews(") {
				page = g.connection(g.reviewNodes(found), after)
			} else {
				page = g.connection(g.threadNodes(found), after)
			}
		}
	}
	if page == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": nil, "errors": []map[string]string{{"message": "Could not resolve to a node"}}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"node": map[string]any{"page": page}}})
}

// connection gives the page of nodes that starts after the cursor after. The cursor is the number of nodes before the
// page. The caller must hold the lock.
func (g *FakeGitHub) connection(nodes any, after string) map[string]any {
	list := reflect.ValueOf(nodes)
	start, _ := strconv.Atoi(after)
	end := list.Len()
	if g.graphqlPageSize > 0 {
		end = min(end, start+g.graphqlPageSize)
	}
	return map[string]any{
		"nodes":    list.Slice(start, end).Interface(),
		"pageInfo": map[string]any{"hasNextPage": end < list.Len(), "endCursor": strconv.Itoa(end)},
	}
}

func (g *FakeGitHub) reviewNodes(found *issue) []map[string]any {
	nodes := []map[string]any{}
	for _, review := range found.reviews {
		nodes = append(nodes, map[string]any{
			"id":          fmt.Sprintf("RV_%d", review.ID),
			"state":       review.State,
			"author":      graphqlActor(review.User.Login),
			"commit":      map[string]string{"oid": review.CommitID},
			"submittedAt": review.SubmittedAt,
		})
	}
	return nodes
}

// threadNode is a review thread with its comments. The caller must hold the lock.
type threadNode struct {
	graphqlThread
	comments []graphqlComment
}

func (g *FakeGitHub) threadNodes(found *issue) []threadNode {
	nodes := []threadNode{}
	for _, root := range found.reviewComments {
		if root.InReplyToID != 0 {
			continue
		}
		thread := threadNode{graphqlThread: graphqlThread{ID: fmt.Sprintf("RT_%d", root.ID), IsResolved: g.resolvedThreads[root.ID], Path: root.Path, Line: root.Line}}
		for _, comment := range found.reviewComments {
			if comment.ID == root.ID || comment.InReplyToID == root.ID {
				thread.comments = append(thread.comments, graphqlComment{comment.ID, graphqlActor(comment.User.Login), comment.Body, comment.CreatedAt})
			}
		}
		thread.Comments = g.connection(thread.comments, "")
		nodes = append(nodes, thread)
	}
	return nodes
}

func graphqlActor(login string) map[string]string {
	if bot, ok := strings.CutSuffix(login, "[bot]"); ok {
		return map[string]string{"__typename": "Bot", "login": bot}
	}
	return map[string]string{"__typename": "User", "login": login}
}
