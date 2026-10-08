package engine

import (
	"context"
	"log"
	"slices"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// The reactions that show a user if Mobius acts on a comment.
const (
	gotReaction      = "eyes"
	declinedReaction = "confused"
	launchedReaction = "rocket"
)

// The replies to a comment that Mobius does not act on. Each reply gives the reason and the correct action.
const (
	noWorkstreamReply = "Mobius does not act on this comment. This issue is in no Workstream. To start work on it, add the label `mobius:ready`."
	closedIssueReply  = "Mobius does not act on a comment on a closed issue. To make Mobius act, reopen the issue and write the comment again."
	noTaskReply       = "Mobius does not act on this comment. This pull request has no live task. To request work, add the label `mobius:ready` to an issue."
	closedThreadReply = "Mobius does not act on this comment. Mobius acts only on an open review thread that a trusted author starts. " +
		"This thread is resolved, or an untrusted author started it. To make Mobius act, unresolve the thread and write the comment again, or write a new comment on the pull request."
)

// acknowledge adds the reaction of an agent that gets the conversation comments.
func acknowledge(ctx context.Context, repository github.Repository, comments []*gh.IssueComment) error {
	for _, comment := range comments {
		if err := repository.ReactToComment(ctx, comment.GetID(), gotReaction); err != nil {
			return err
		}
	}
	return nil
}

// declineComments adds the reaction of a refusal to each conversation comment, and replies with reply on the issue or the pull
// request number. The store holds the comments that have a reply, so a poll that runs again writes no second reply.
func (e *Engine) declineComments(ctx context.Context, repository github.Repository, number int64, comments []*gh.IssueComment, reply string) error {
	for _, comment := range comments {
		if err := repository.ReactToComment(ctx, comment.GetID(), declinedReaction); err != nil {
			return err
		}
		answered, err := e.commentAnswered(ctx, repository, false, comment.GetID())
		if err != nil {
			return err
		}
		if answered {
			continue
		}
		if _, err := repository.AddComment(ctx, number, reply); err != nil {
			return err
		}
		if err := e.markCommentAnswered(ctx, repository, false, comment.GetID()); err != nil {
			return err
		}
	}
	return nil
}

// declineReviewComment adds the reaction of a refusal to the review comment, and replies with reply in its thread. The
// store holds the comments that have a reply, so a poll that runs again writes no second reply.
func (e *Engine) declineReviewComment(ctx context.Context, repository github.Repository, number int64, comment *gh.PullRequestComment, reply string) error {
	if err := repository.ReactToReviewComment(ctx, comment.GetID(), declinedReaction); err != nil {
		return err
	}
	answered, err := e.commentAnswered(ctx, repository, true, comment.GetID())
	if err != nil || answered {
		return err
	}
	root := comment.GetInReplyTo()
	if root == 0 {
		root = comment.GetID()
	}
	if err := repository.ReplyToReviewComment(ctx, number, root, reply); err != nil {
		return err
	}
	return e.markCommentAnswered(ctx, repository, true, comment.GetID())
}

func (e *Engine) commentAnswered(ctx context.Context, repository github.Repository, review bool, id int64) (bool, error) {
	return e.queries.IsCommentAnswered(ctx, store.IsCommentAnsweredParams{Repository: repository.FullName, Review: review, Comment: id})
}

func (e *Engine) markCommentAnswered(ctx context.Context, repository github.Repository, review bool, id int64) error {
	return e.queries.MarkCommentAnswered(ctx, store.MarkCommentAnsweredParams{Repository: repository.FullName, Review: review, Comment: id})
}

// answerReviewComments declines each review comment of a trusted user that is not in an open thread, and gives the
// comments that are in an open thread. An agent gets the comments of an open thread. A comment of a thread that the
// poll does not know is not in an open thread. The caller adds the reaction of the comments that it gives, with
// acknowledgeReviewComments.
func (e *Engine) answerReviewComments(ctx context.Context, repository github.Repository, number int64, comments []*gh.PullRequestComment) ([]*gh.PullRequestComment, error) {
	trusted := func(login string) bool { return e.TrustedAuthor(repository.AppSlug, login) }
	var open []*gh.PullRequestComment
	for _, comment := range comments {
		if !e.trustedUser(comment.GetUser().GetLogin()) {
			continue
		}
		isOpen := slices.ContainsFunc(e.pull(repository, number).Threads, func(thread github.ReviewThread) bool {
			return openThread(thread, trusted, appLogin(repository.AppSlug)) &&
				slices.ContainsFunc(thread.Comments, func(inThread github.ThreadComment) bool { return inThread.ID == comment.GetID() })
		})
		if !isOpen {
			if err := e.declineReviewComment(ctx, repository, number, comment, closedThreadReply); err != nil {
				return nil, err
			}
			continue
		}
		open = append(open, comment)
	}
	return open, nil
}

// acknowledgeReviewComments adds the reaction of an agent that gets the review comments.
func acknowledgeReviewComments(ctx context.Context, repository github.Repository, comments []*gh.PullRequestComment) error {
	for _, comment := range comments {
		if err := repository.ReactToReviewComment(ctx, comment.GetID(), gotReaction); err != nil {
			return err
		}
	}
	return nil
}

// launched adds the reaction of an agent that starts its work on the comment id. A failed call does not stop the agent.
// GitHub keeps one reaction of a kind for each user, so a second call changes nothing.
func launched(ctx context.Context, repository github.Repository, review bool, id int64) {
	react := repository.ReactToComment
	if review {
		react = repository.ReactToReviewComment
	}
	if err := react(ctx, id, launchedReaction); err != nil {
		log.Printf("react to comment %d of %s: %v", id, repository.FullName, err)
	}
}

// launchEventComment adds the reaction of a Lead turn that starts on the event to the comment of the event.
func (e *Engine) launchEventComment(ctx context.Context, event *store.LeadEvent) {
	repository, err := e.repository(event.Repository)
	if err != nil {
		log.Printf("react to comment %d of %s: %v", event.Comment.Int64, event.Repository, err)
		return
	}
	launched(ctx, repository, event.Review.Bool, event.Comment.Int64)
}

// launchTriagerComments adds the reaction of a Triager run that starts to each comment of an event that is newer than the
// last comment of the Mobius App. The Triager read the older comments in an earlier run.
func (e *Engine) launchTriagerComments(ctx context.Context, repository github.Repository, comments []*gh.IssueComment) {
	var readAt time.Time
	for _, comment := range comments {
		if strings.EqualFold(comment.GetUser().GetLogin(), appLogin(repository.AppSlug)) {
			readAt = laterOf(readAt, comment.GetCreatedAt().Time)
		}
	}
	for _, comment := range comments {
		if e.commentIsEvent(repository.AppSlug, comment) && comment.GetCreatedAt().After(readAt) {
			launched(ctx, repository, false, comment.GetID())
		}
	}
}
