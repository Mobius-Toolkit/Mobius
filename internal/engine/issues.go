package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
)

// timeFormat is the format of the times in the text of an issue.
const timeFormat = "2006-01-02 15:04 UTC"

func hasLabel(issue *gh.Issue, name string) bool {
	return slices.ContainsFunc(issue.Labels, func(label *gh.Label) bool { return label.GetName() == name })
}

// inOtherRepository tells if the issue is in another repository than repository. After a transfer,
// GitHub gives the issue with the URL of its new repository.
func inOtherRepository(issue *gh.Issue, repository string) bool {
	return otherRepository(issue.GetRepositoryURL(), repository)
}

// otherRepository tells if repositoryURL, the API URL of a repository, names another repository than repository.
func otherRepository(repositoryURL, repository string) bool {
	return !strings.HasSuffix(strings.ToLower(repositoryURL), "/repos/"+strings.ToLower(repository))
}

// readIssue gives the text of the issue or the pull request number. An issue, a review thread, a review or
// a comment of an untrusted author is absent from the text.
func (e *Engine) readIssue(ctx context.Context, repository github.Repository, number int64) (string, error) {
	trusted := func(login string) bool { return e.TrustedAuthor(repository.AppSlug, login) }
	issue, err := repository.Issue(ctx, number)
	if err != nil {
		return "", err
	}
	if issue == nil || !trusted(issue.GetUser().GetLogin()) {
		return "", refuse("#%d is not an issue or a pull request of %s.", number, repository.FullName)
	}
	kind := "issue"
	if issue.IsPullRequest() {
		kind = "pull request"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "#%d %s (%s, %s)\n\n%s\n\n# Comments\n", number, issue.GetTitle(), kind, issue.GetState(), issue.GetBody())
	comments, err := conversationComments(ctx, repository, number, trusted)
	if err != nil {
		return "", err
	}
	text.WriteString(comments)
	if !issue.IsPullRequest() {
		return text.String(), nil
	}
	text.WriteString("\n# Reviews\n")
	reviews, err := repository.Reviews(ctx, number)
	if err != nil {
		return "", err
	}
	for _, review := range reviews {
		if login := review.GetUser().GetLogin(); review.SubmittedAt != nil && trusted(login) {
			text.WriteString(entry(login, review.GetSubmittedAt().Time, ", "+review.GetState(), review.GetBody()))
		}
	}
	text.WriteString("\n# Review threads\n")
	reviewThreads, err := reviewThreads(ctx, repository, number, trusted)
	if err != nil {
		return "", err
	}
	return text.String() + reviewThreads, nil
}

// readPullRequestComments gives the text of the conversation comments and the review threads of the pull request number.
// A comment of an untrusted author is absent from the text.
func (e *Engine) readPullRequestComments(ctx context.Context, repository github.Repository, number int64) (string, error) {
	trusted := func(login string) bool { return e.TrustedAuthor(repository.AppSlug, login) }
	comments, err := conversationComments(ctx, repository, number, trusted)
	if err != nil {
		return "", err
	}
	reviewThreads, err := reviewThreads(ctx, repository, number, trusted)
	if err != nil {
		return "", err
	}
	return "# Pull request comments\n" + comments + "\n# Review threads\n" + reviewThreads, nil
}

// conversationComments gives the text of the conversation comments of trusted authors on the issue or the pull request number.
func conversationComments(ctx context.Context, repository github.Repository, number int64, trusted func(string) bool) (string, error) {
	comments, err := repository.Comments(ctx, number)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, comment := range comments {
		if login := comment.GetUser().GetLogin(); trusted(login) {
			text.WriteString(entry(login, comment.GetCreatedAt().Time, "", comment.GetBody()))
		}
	}
	return text.String(), nil
}

// reviewThreads gives the text of each review thread of the pull request number that a trusted author started.
func reviewThreads(ctx context.Context, repository github.Repository, number int64, trusted func(string) bool) (string, error) {
	comments, err := repository.ReviewComments(ctx, number)
	if err != nil {
		return "", err
	}
	return threads(comments, trusted), nil
}

// threads gives the text of each review thread of comments that a trusted author started.
func threads(comments []*gh.PullRequestComment, trusted func(string) bool) string {
	var text strings.Builder
	for _, root := range comments {
		if root.InReplyTo == nil && trusted(root.GetUser().GetLogin()) {
			text.WriteString(thread(comments, root, trusted))
		}
	}
	return text.String()
}

// fixThreads gives the text of each review thread of the pull request number that starts with a comment of roots,
// with the action fix.
func fixThreads(ctx context.Context, repository github.Repository, number int64, roots []int64, trusted func(string) bool) (string, error) {
	comments, err := repository.ReviewComments(ctx, number)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, root := range comments {
		if slices.Contains(roots, root.GetID()) {
			text.WriteString(thread(comments, root, trusted) + "\nAction: fix\n")
		}
	}
	return text.String(), nil
}

// thread gives the text of the review thread that starts with root. GitHub gives each reply
// the id of the first comment of its thread as in_reply_to_id.
func thread(comments []*gh.PullRequestComment, root *gh.PullRequestComment, trusted func(string) bool) string {
	line := ""
	if root.Line != nil {
		line = fmt.Sprintf(" line %d", root.GetLine())
	}
	text := fmt.Sprintf("\nThread %d, %s%s:\n", root.GetID(), root.GetPath(), line)
	for _, comment := range comments {
		login := comment.GetUser().GetLogin()
		if (comment.GetID() == root.GetID() || comment.GetInReplyTo() == root.GetID()) && trusted(login) {
			text += entry(login, comment.GetCreatedAt().Time, "", comment.GetBody())
		}
	}
	return text
}

func entry(login string, t time.Time, state, body string) string {
	return fmt.Sprintf("\n@%s, %s%s:\n%s\n", login, t.UTC().Format(timeFormat), state, body)
}

// workstreamOf gives the number of the nearest Workstream issue above the issue number, or 0 when the issue is in no Workstream.
func workstreamOf(ctx context.Context, repository github.Repository, number int64) (int64, error) {
	for {
		parent, err := repository.Parent(ctx, number)
		if err != nil || parent == nil {
			return 0, err
		}
		if hasLabel(parent, workstreamLabel) {
			return int64(parent.GetNumber()), nil
		}
		number = int64(parent.GetNumber())
	}
}

// inWorkstream tells if the issue number is in the Workstream. An issue with a Workstream issue of its own
// below the Workstream belongs to that Workstream.
func inWorkstream(ctx context.Context, repository github.Repository, workstream, number int64) (bool, error) {
	issue, err := repository.Issue(ctx, number)
	if err != nil || issue == nil || hasLabel(issue, workstreamLabel) {
		return false, err
	}
	of, err := workstreamOf(ctx, repository, number)
	return of == workstream, err
}

// reply replies with text to the review thread that starts with the comment id, or to the conversation comment id,
// of the pull request. It gives false when the pull request has no such thread and no such comment.
func reply(ctx context.Context, repository github.Repository, pullRequest, id int64, text string) (bool, error) {
	target, found, err := findTarget(ctx, repository, pullRequest, id)
	if err != nil || !found {
		return false, err
	}
	return true, target.post(ctx, repository, pullRequest, text)
}

// replyTarget is a review thread or a conversation comment of a pull request.
type replyTarget struct {
	// id is the id of the first comment of the review thread, or of the conversation comment.
	id int64
	// thread is the GraphQL node id of the review thread, or "" for a conversation comment.
	thread string
	// body is the text of the conversation comment.
	body string
}

// heldReply is a reply of the Implementer that waits for the push of its commits.
type heldReply struct {
	target replyTarget
	text   string
}

// findTarget gives the review thread that starts with the comment id, or the conversation comment id, of the pull
// request. It gives false when the pull request has no such thread and no such comment.
func findTarget(ctx context.Context, repository github.Repository, pullRequest, id int64) (replyTarget, bool, error) {
	threads, err := repository.ReviewThreads(ctx, pullRequest)
	if err != nil {
		return replyTarget{}, false, err
	}
	for _, thread := range threads {
		if thread.Comment == id {
			return replyTarget{id: id, thread: thread.ID}, true, nil
		}
	}
	comments, err := repository.Comments(ctx, pullRequest)
	if err != nil {
		return replyTarget{}, false, err
	}
	for _, comment := range comments {
		if comment.GetID() == id {
			return replyTarget{id: id, body: comment.GetBody()}, true, nil
		}
	}
	return replyTarget{}, false, nil
}

// post replies with text to the target on the pull request. A reply to a review thread resolves the thread.
func (t replyTarget) post(ctx context.Context, repository github.Repository, pullRequest int64, text string) error {
	if t.thread != "" {
		if _, _, err := repository.Client.PullRequests.CreateCommentInReplyTo(ctx, repository.Owner(), repository.Name(), int(pullRequest), text, t.id); err != nil {
			return err
		}
		return repository.ResolveReviewThread(ctx, t.thread)
	}
	// A conversation comment has no thread, so the reply is a new comment that quotes it, and nobody can resolve it.
	_, err := repository.AddComment(ctx, pullRequest, quote(t.body)+"\n\n"+text)
	return err
}

// quote gives text with "> " before each line.
func quote(text string) string {
	var quoted []string
	for line := range strings.Lines(text) {
		quoted = append(quoted, "> "+strings.TrimSuffix(line, "\n"))
	}
	return strings.Join(quoted, "\n")
}
