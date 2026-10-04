package testkit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type reviewJSON struct {
	User        loginJSON `json:"user"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	SubmittedAt string    `json:"submitted_at"`
}

type reviewCommentJSON struct {
	ID        int64     `json:"id"`
	User      loginJSON `json:"user"`
	Body      string    `json:"body"`
	CreatedAt string    `json:"created_at"`
	Path      string    `json:"path"`
	Line      int       `json:"line"`
	// InReplyToID is the id of the first comment of the thread. The first comment has no InReplyToID.
	InReplyToID int64 `json:"in_reply_to_id,omitempty"`
}

// Thread is a review thread of a pull request.
type Thread struct {
	Resolved bool
	Comments []Comment
}

// AddReview adds a submitted review of author with state, for example "APPROVED", to the pull request.
func (g *FakeGitHub) AddReview(repository string, number int64, author, state, body string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	found := g.issues[issueKey{repository, number}]
	found.reviews = append(found.reviews, reviewJSON{User: loginJSON{author}, Body: body, State: state, SubmittedAt: timestamp(g.tick())})
}

// AddReviewComment adds a review comment of author on line 12 of src/plan.rs to the pull request, and gives its id.
// A reply has the id of the first comment of its thread in inReplyTo. The first comment has 0.
func (g *FakeGitHub) AddReviewComment(repository string, number, inReplyTo int64, author, body string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reviewComment(issueKey{repository, number}, inReplyTo, author, body).ID
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
func (g *FakeGitHub) reviewComment(key issueKey, inReplyTo int64, author, body string) reviewCommentJSON {
	now := g.tick()
	found := g.issues[key]
	g.lastCommentID++
	comment := reviewCommentJSON{
		ID:          g.lastCommentID,
		User:        loginJSON{author},
		Body:        body,
		CreatedAt:   timestamp(now),
		Path:        "src/plan.rs",
		Line:        12,
		InReplyToID: inReplyTo,
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
	if key, ok := g.issue(w, r); ok {
		writeJSON(w, http.StatusOK, page(w, r, append([]reviewCommentJSON{}, g.issues[key].reviewComments...)))
	}
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
			writeJSON(w, http.StatusCreated, g.reviewComment(key, request.InReplyTo, caller.login, request.Body))
			return
		}
	}
	notFound(w)
}

type graphqlThread struct {
	ID         string `json:"id"`
	IsResolved bool   `json:"isResolved"`
	Comments   struct {
		Nodes []graphqlComment `json:"nodes"`
	} `json:"comments"`
}

type graphqlComment struct {
	DatabaseID int64 `json:"databaseId"`
	Author     struct {
		Typename string `json:"__typename"`
		Login    string `json:"login"`
	} `json:"author"`
}

// graphql answers the review threads query and the mutation resolveReviewThread, each in one page.
// The node id of a thread is "RT_" and the id of its first comment.
func (g *FakeGitHub) graphql(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Query     string `json:"query"`
		Variables struct {
			ID     string `json:"id"`
			Owner  string `json:"owner"`
			Name   string `json:"name"`
			Number int64  `json:"number"`
		} `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		message(w, http.StatusBadRequest, err.Error())
		return
	}
	variables := request.Variables
	g.mu.Lock()
	defer g.mu.Unlock()
	if strings.Contains(request.Query, "resolveReviewThread") {
		root, err := strconv.ParseInt(strings.TrimPrefix(variables.ID, "RT_"), 10, 64)
		if err != nil || !strings.HasPrefix(variables.ID, "RT_") {
			writeJSON(w, http.StatusOK, map[string]any{"data": nil, "errors": []map[string]string{{"message": "Could not resolve to a node"}}})
			return
		}
		g.resolvedThreads[root] = true
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"resolveReviewThread": map[string]any{"clientMutationId": nil}}})
		return
	}
	found, ok := g.issues[issueKey{variables.Owner + "/" + variables.Name, variables.Number}]
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": nil}}})
		return
	}
	threads := []graphqlThread{}
	for _, root := range found.reviewComments {
		if root.InReplyToID != 0 {
			continue
		}
		thread := graphqlThread{ID: fmt.Sprintf("RT_%d", root.ID), IsResolved: g.resolvedThreads[root.ID]}
		for _, comment := range found.reviewComments {
			if comment.ID != root.ID && comment.InReplyToID != root.ID {
				continue
			}
			node := graphqlComment{DatabaseID: comment.ID}
			node.Author.Typename, node.Author.Login = "User", comment.User.Login
			if bot, ok := strings.CutSuffix(comment.User.Login, "[bot]"); ok {
				node.Author.Typename, node.Author.Login = "Bot", bot
			}
			thread.Comments.Nodes = append(thread.Comments.Nodes, node)
		}
		threads = append(threads, thread)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
		"reviewThreads": map[string]any{
			"nodes":    threads,
			"pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil},
		},
	}}}})
}
