package engine

import (
	"context"
	"database/sql"
	"log"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// addOrKeepComment adds a comment to the issue or the pull request number. When GitHub refuses the comment, the
// comment goes to comment_writes and the next poll sends it again.
func (e *Engine) addOrKeepComment(ctx context.Context, repository github.Repository, number int64, body string) error {
	_, err := repository.AddComment(ctx, number, body)
	if err == nil {
		return nil
	}
	log.Printf("add a comment to %s#%d: %v", repository.FullName, number, err)
	return e.queries.AddCommentWrite(ctx, store.AddCommentWriteParams{
		Repository: repository.FullName,
		Number:     sql.NullInt64{Int64: number, Valid: true},
		Body:       body,
	})
}

// keepRefusedReply sends the reply with send. When GitHub refuses the reply with a 4xx status, the reply goes to
// comment_writes, with replyTo as the first comment of the review thread (0 for a reply on the conversation), and the next
// poll sends it again. Another error returns, so the next poll runs the step again.
func (e *Engine) keepRefusedReply(ctx context.Context, repository github.Repository, number, replyTo int64, body string, err error) error {
	if !refusedByGitHub(err) {
		return err
	}
	log.Printf("reply on %s#%d: %v", repository.FullName, number, err)
	return e.queries.AddCommentWrite(ctx, store.AddCommentWriteParams{
		Repository: repository.FullName,
		Number:     sql.NullInt64{Int64: number, Valid: true},
		ReplyTo:    sql.NullInt64{Int64: replyTo, Valid: replyTo != 0},
		Body:       body,
	})
}

// updateOrKeepComment replaces the body of the comment id. When GitHub refuses the update, the update goes to
// comment_writes and the next poll sends it again.
func (e *Engine) updateOrKeepComment(ctx context.Context, repository github.Repository, id int64, body string) error {
	err := repository.UpdateComment(ctx, id, body)
	if err == nil {
		return nil
	}
	log.Printf("update the comment %d of %s: %v", id, repository.FullName, err)
	return e.queries.AddCommentWrite(ctx, store.AddCommentWriteParams{
		Repository: repository.FullName,
		Comment:    sql.NullInt64{Int64: id, Valid: true},
		Body:       body,
	})
}

// sendCommentWrites sends each comment write of the repository that an earlier step kept. A write that fails stays
// for the next poll. A write that GitHub answers with 404 or 410 has no target, so it goes away.
func (e *Engine) sendCommentWrites(ctx context.Context, repository github.Repository) error {
	writes, err := e.queries.ListCommentWrites(ctx, repository.FullName)
	if err != nil {
		return err
	}
	for _, write := range writes {
		switch {
		case write.ReplyTo.Valid:
			err = repository.ReplyToReviewComment(ctx, write.Number.Int64, write.ReplyTo.Int64, write.Body)
		case write.Number.Valid:
			_, err = repository.AddComment(ctx, write.Number.Int64, write.Body)
		default:
			err = repository.UpdateComment(ctx, write.Comment.Int64, write.Body)
		}
		if err != nil && !github.NotFound(err) {
			log.Printf("send a comment write of %s: %v", repository.FullName, err)
			continue
		}
		if err := e.queries.DeleteCommentWrite(ctx, write.ID); err != nil {
			return err
		}
	}
	return nil
}
