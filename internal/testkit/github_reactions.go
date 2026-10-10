package testkit

import (
	"net/http"
	"strconv"
)

// Reaction is a reaction that a client added to a comment.
type Reaction struct {
	User    string `json:"user"`
	Content string `json:"content"`
}

type storedReaction struct {
	id int64
	Reaction
}

type reactionKey struct {
	repository string
	comment    int64
}

// Reactions gives the reactions of the comment id of the repository, in the order in which clients added them. The
// ids of the conversation comments and of the review comments are different.
func (g *FakeGitHub) Reactions(repository string, id int64) []Reaction {
	g.mu.Lock()
	defer g.mu.Unlock()
	var reactions []Reaction
	for _, stored := range g.reactions[reactionKey{repository, id}] {
		reactions = append(reactions, stored.Reaction)
	}
	return reactions
}

// AddReaction adds the reaction content of the user login to the comment id of the repository.
func (g *FakeGitHub) AddReaction(repository string, id int64, login, content string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.storeReaction(reactionKey{repository, id}, Reaction{login, content})
}

// FailReactions makes each new reaction with the content fail, or work again when fail is false.
func (g *FakeGitHub) FailReactions(content string, fail bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failedReactions[content] = fail
}

// storeReaction stores the reaction when the comment has no reaction like it, and gives the stored reaction and whether
// it is new. The caller holds the lock.
func (g *FakeGitHub) storeReaction(key reactionKey, reaction Reaction) (storedReaction, bool) {
	for _, existing := range g.reactions[key] {
		if existing.Reaction == reaction {
			return existing, false
		}
	}
	g.nextReactionID++
	stored := storedReaction{g.nextReactionID, reaction}
	g.reactions[key] = append(g.reactions[key], stored)
	return stored, true
}

func hasIssueComment(g *FakeGitHub, key issueKey, id int64) bool {
	for _, comment := range g.issues[key].comments {
		if comment.ID == id {
			return true
		}
	}
	return false
}

func hasReviewComment(g *FakeGitHub, key issueKey, id int64) bool {
	for _, comment := range g.issues[key].reviewComments {
		if comment.ID == id {
			return true
		}
	}
	return false
}

// hasComment tells whether an issue or a pull request of the repository has the comment id. The caller holds the lock.
func (g *FakeGitHub) hasComment(repository string, id int64, has func(*FakeGitHub, issueKey, int64) bool) bool {
	for key := range g.issues {
		if key.repository == repository && has(g, key, id) {
			return true
		}
	}
	return false
}

func reactionJSON(stored storedReaction) map[string]any {
	return map[string]any{"id": stored.id, "user": map[string]string{"login": stored.User}, "content": stored.Content}
}

// addIssueCommentReaction adds a reaction of the token owner to a conversation comment.
func (g *FakeGitHub) addIssueCommentReaction(w http.ResponseWriter, r *http.Request) {
	g.addReaction(w, r, hasIssueComment)
}

// addReviewCommentReaction adds a reaction of the token owner to a review comment.
func (g *FakeGitHub) addReviewCommentReaction(w http.ResponseWriter, r *http.Request) {
	g.addReaction(w, r, hasReviewComment)
}

// addReaction adds the reaction when has tells that an issue or a pull request of the repository has the comment. A
// second reaction with the same content of the same user gives the first reaction again, with the status 200.
func (g *FakeGitHub) addReaction(w http.ResponseWriter, r *http.Request, has func(*FakeGitHub, issueKey, int64) bool) {
	var request struct {
		Content string `json:"content"`
	}
	if !decode(w, r, &request) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	caller, _ := g.validToken(r)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failedReactions[request.Content] {
		message(w, http.StatusInternalServerError, "Server Error")
		return
	}
	if !g.hasComment(repository(r), id, has) {
		notFound(w)
		return
	}
	stored, created := g.storeReaction(reactionKey{repository(r), id}, Reaction{caller.login, request.Content})
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, reactionJSON(stored))
}

// listIssueCommentReactions lists the reactions of a conversation comment.
func (g *FakeGitHub) listIssueCommentReactions(w http.ResponseWriter, r *http.Request) {
	g.listReactions(w, r, hasIssueComment)
}

// listReviewCommentReactions lists the reactions of a review comment.
func (g *FakeGitHub) listReviewCommentReactions(w http.ResponseWriter, r *http.Request) {
	g.listReactions(w, r, hasReviewComment)
}

// listReactions lists the reactions of the comment, only those with the content of the query when it has one.
func (g *FakeGitHub) listReactions(w http.ResponseWriter, r *http.Request, has func(*FakeGitHub, issueKey, int64) bool) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	content := r.URL.Query().Get("content")
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.hasComment(repository(r), id, has) {
		notFound(w)
		return
	}
	reactions := []map[string]any{}
	for _, stored := range g.reactions[reactionKey{repository(r), id}] {
		if content == "" || stored.Content == content {
			reactions = append(reactions, reactionJSON(stored))
		}
	}
	writeJSON(w, http.StatusOK, reactions)
}

// deleteIssueCommentReaction deletes a reaction of a conversation comment.
func (g *FakeGitHub) deleteIssueCommentReaction(w http.ResponseWriter, r *http.Request) {
	g.deleteReaction(w, r, hasIssueComment)
}

// deleteReviewCommentReaction deletes a reaction of a review comment.
func (g *FakeGitHub) deleteReviewCommentReaction(w http.ResponseWriter, r *http.Request) {
	g.deleteReaction(w, r, hasReviewComment)
}

func (g *FakeGitHub) deleteReaction(w http.ResponseWriter, r *http.Request, has func(*FakeGitHub, issueKey, int64) bool) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	reactionID, _ := strconv.ParseInt(r.PathValue("reaction"), 10, 64)
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.hasComment(repository(r), id, has) {
		notFound(w)
		return
	}
	key := reactionKey{repository(r), id}
	for i, stored := range g.reactions[key] {
		if stored.id == reactionID {
			g.reactions[key] = append(g.reactions[key][:i], g.reactions[key][i+1:]...)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	notFound(w)
}
