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

type reactionKey struct {
	repository string
	comment    int64
}

// Reactions gives the reactions of the comment id of the repository, in the order in which clients added them. The
// ids of the conversation comments and of the review comments are different.
func (g *FakeGitHub) Reactions(repository string, id int64) []Reaction {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Reaction(nil), g.reactions[reactionKey{repository, id}]...)
}

// FailReactions makes each new reaction with the content fail, or work again when fail is false.
func (g *FakeGitHub) FailReactions(content string, fail bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failedReactions[content] = fail
}

// addIssueCommentReaction adds a reaction of the token owner to a conversation comment.
func (g *FakeGitHub) addIssueCommentReaction(w http.ResponseWriter, r *http.Request) {
	g.addReaction(w, r, func(key issueKey, id int64) bool {
		for _, comment := range g.issues[key].comments {
			if comment.ID == id {
				return true
			}
		}
		return false
	})
}

// addReviewCommentReaction adds a reaction of the token owner to a review comment.
func (g *FakeGitHub) addReviewCommentReaction(w http.ResponseWriter, r *http.Request) {
	g.addReaction(w, r, func(key issueKey, id int64) bool {
		for _, comment := range g.issues[key].reviewComments {
			if comment.ID == id {
				return true
			}
		}
		return false
	})
}

// addReaction adds the reaction when has tells that an issue or a pull request of the repository has the comment. A
// second reaction with the same content of the same user gives the first reaction again, with the status 200.
func (g *FakeGitHub) addReaction(w http.ResponseWriter, r *http.Request, has func(issueKey, int64) bool) {
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
	found := false
	for key := range g.issues {
		if key.repository == repository(r) && has(key, id) {
			found = true
		}
	}
	if !found {
		notFound(w)
		return
	}
	key := reactionKey{repository(r), id}
	reaction := Reaction{caller.login, request.Content}
	for _, existing := range g.reactions[key] {
		if existing == reaction {
			writeJSON(w, http.StatusOK, map[string]any{"content": reaction.Content})
			return
		}
	}
	g.reactions[key] = append(g.reactions[key], reaction)
	writeJSON(w, http.StatusCreated, map[string]any{"content": reaction.Content})
}
