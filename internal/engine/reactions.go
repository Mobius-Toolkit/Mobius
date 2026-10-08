package engine

import (
	"context"
	"slices"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
)

// The reactions that show a user if Mobius acts on a comment.
const (
	gotReaction      = "eyes"
	declinedReaction = "confused"
)

// The replies to a comment that Mobius does not act on. Each reply gives the reason and the correct action.
const (
	noWorkstreamReply = "Mobius does not act on this comment. This issue is in no Workstream. To start work on it, add the label `mobius:ready`."
	closedIssueReply  = "Mobius does not act on a comment on a closed issue. To make Mobius act, reopen the issue and write the comment again."
	noTaskReply       = "Mobius does not act on this comment. This pull request has no live task. To request work, add the label `mobius:ready` to an issue."
	closedThreadReply = "Mobius does not act on this comment. Mobius acts only on an open review thread that a trusted author starts. " +
		"This thread is resolved, or an untrusted author started it. To make Mobius act, unresolve the thread or write a new comment on the pull request."
)

// acknowledge adds the reaction of an agent that gets the conversation comments.
func acknowledge(ctx context.Context, repository github.Repository, comments []*gh.IssueComment) error {
	for _, comment := range comments {
		if _, err := repository.ReactToComment(ctx, comment.GetID(), gotReaction); err != nil {
			return err
		}
	}
	return nil
}

// declineComments adds the reaction of a refusal to each conversation comment, and replies with reply on the issue or the pull
// request number. A comment that already has the reaction gets no second reply.
func declineComments(ctx context.Context, repository github.Repository, number int64, comments []*gh.IssueComment, reply string) error {
	for _, comment := range comments {
		added, err := repository.ReactToComment(ctx, comment.GetID(), declinedReaction)
		if err != nil {
			return err
		}
		if !added {
			continue
		}
		if _, err := repository.AddComment(ctx, number, reply); err != nil {
			return err
		}
	}
	return nil
}

// declineReviewComment adds the reaction of a refusal to the review comment, and replies with reply in its thread.
func declineReviewComment(ctx context.Context, repository github.Repository, number int64, comment *gh.PullRequestComment, reply string) error {
	added, err := repository.ReactToReviewComment(ctx, comment.GetID(), declinedReaction)
	if err != nil || !added {
		return err
	}
	root := comment.GetInReplyTo()
	if root == 0 {
		root = comment.GetID()
	}
	return repository.ReplyToReviewComment(ctx, number, root, reply)
}

// answerReviewComments reacts to each review comment of a trusted user: the Judge gets the comments of an open
// thread, and Mobius does not act on the others. A comment of a thread that the poll does not know is the second case.
func (e *Engine) answerReviewComments(ctx context.Context, repository github.Repository, number int64, comments []*gh.PullRequestComment) error {
	trusted := func(login string) bool { return e.TrustedAuthor(repository.AppSlug, login) }
	for _, comment := range comments {
		if !e.trustedUser(comment.GetUser().GetLogin()) {
			continue
		}
		open := slices.ContainsFunc(e.pull(repository, number).Threads, func(thread github.ReviewThread) bool {
			return openThread(thread, trusted, appLogin(repository.AppSlug)) &&
				slices.ContainsFunc(thread.Comments, func(inThread github.ThreadComment) bool { return inThread.ID == comment.GetID() })
		})
		if !open {
			if err := declineReviewComment(ctx, repository, number, comment, closedThreadReply); err != nil {
				return err
			}
			continue
		}
		if _, err := repository.ReactToReviewComment(ctx, comment.GetID(), gotReaction); err != nil {
			return err
		}
	}
	return nil
}
