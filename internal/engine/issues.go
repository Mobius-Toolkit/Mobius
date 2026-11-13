package engine

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	gh "github.com/google/go-github/v92/github"

	"github.com/Mobius-Toolkit/Mobius/internal/github"
	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

// searchedComments is the number of comments that searchIssues gives for a closed issue.
const searchedComments = 3

// timeFormat is the format of the times in the text of an issue.
const timeFormat = "2006-01-02 15:04 UTC"

func hasLabel(issue *gh.Issue, name string) bool {
	return slices.ContainsFunc(issue.Labels, func(label *gh.Label) bool { return label.GetName() == name })
}

func hasPullRequestLabel(pullRequest *gh.PullRequest, name string) bool {
	return slices.ContainsFunc(pullRequest.Labels, func(label *gh.Label) bool { return label.GetName() == name })
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
	fmt.Fprintf(&text, "%s#%d %s (%s, %s)\n\n%s\n\n# Comments\n", repository.FullName, number, issue.GetTitle(), kind, issue.GetState(), issue.GetBody())
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

// searchIssues gives the issues of the repository that match the query, open and closed. An issue of an untrusted
// author is absent from the text. A closed issue has its close reason and the last searchedComments comments of trusted
// authors, but no engine notice: a comment of the Mobius App with no role mark is absent. An issue of another
// repository starts with owner/name before its number.
func (e *Engine) searchIssues(ctx context.Context, _ caller, repository github.Repository, input queryInput) (string, error) {
	if empty(input.Query) {
		return "", refuse("query must not be empty.")
	}
	found, err := repository.SearchIssues(ctx, input.Query)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, issue := range found {
		source, ok := e.issueRepository(issue, repository)
		if !ok || issue.IsPullRequest() || !e.TrustedAuthor(source.AppSlug, issue.GetUser().GetLogin()) {
			continue
		}
		trusted := func(login string) bool { return e.TrustedAuthor(source.AppSlug, login) }
		name := ""
		if source.FullName != repository.FullName {
			name = source.FullName
		}
		state := issue.GetState()
		if state == "closed" {
			state += ", " + cmp.Or(issue.GetStateReason(), "no reason")
		}
		fmt.Fprintf(&text, "%s#%d %s (%s)\n\n%s\n", name, issue.GetNumber(), issue.GetTitle(), state, issue.GetBody())
		if issue.GetState() != "closed" {
			text.WriteString("\n")
			continue
		}
		comments, err := source.Comments(ctx, int64(issue.GetNumber()))
		if err != nil {
			return "", err
		}
		comments = slices.DeleteFunc(comments, func(comment *gh.IssueComment) bool {
			login := comment.GetUser().GetLogin()
			if !trusted(login) {
				return true
			}
			return strings.EqualFold(login, appLogin(source.AppSlug)) && runner.WithoutMark(comment.GetBody()) == comment.GetBody()
		})
		text.WriteString("\n# Last comments\n")
		for _, comment := range comments[max(0, len(comments)-searchedComments):] {
			text.WriteString(entry(comment.GetUser().GetLogin(), comment.GetCreatedAt().Time, "", comment.GetBody()))
		}
		text.WriteString("\n")
	}
	if text.Len() == 0 {
		return "No issue matches.", nil
	}
	return text.String(), nil
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

// workstreamOf gives the repository and the number of the nearest Workstream issue above the issue number, or the
// number 0 when the issue is in no Workstream. It reads a parent in another repository through that repository. It
// gives the number 0 when the Mobius App has no access to the repository of a parent.
func (e *Engine) workstreamOf(ctx context.Context, repository github.Repository, number int64) (string, int64, error) {
	for {
		parent, err := repository.Parent(ctx, number)
		if err != nil || parent == nil {
			return "", 0, err
		}
		if name := blockerRepository(parent, repository.FullName); name != repository.FullName {
			var ok bool
			if repository, ok = e.github.Repository(name); !ok {
				return "", 0, nil
			}
		}
		if hasLabel(parent, workstreamLabel) {
			return repository.FullName, int64(parent.GetNumber()), nil
		}
		number = int64(parent.GetNumber())
	}
}

// inWorkstream tells if the issue number of repository is in the Workstream number of workstreamRepository. An issue with a
// Workstream issue of its own below the Workstream belongs to that Workstream.
func (e *Engine) inWorkstream(ctx context.Context, workstreamRepository string, workstream int64, repository github.Repository, number int64) (bool, error) {
	issue, err := repository.Issue(ctx, number)
	if err != nil || issue == nil || hasLabel(issue, workstreamLabel) {
		return false, err
	}
	of, ofNumber, err := e.workstreamOf(ctx, repository, number)
	return of == workstreamRepository && ofNumber == workstream, err
}

// reply replies with text to the review thread that starts with the comment id, or to the conversation comment id,
// of the pull request, and sends the reply at once (sendReply). It gives the id of the outgoing change. It gives false
// when the pull request has no such thread and no such comment.
func (e *Engine) reply(ctx context.Context, repository github.Repository, pullRequest, id int64, text string) (int64, bool, error) {
	target, found, err := findTarget(ctx, repository, pullRequest, id)
	if err != nil || !found {
		return 0, false, err
	}
	change, err := e.sendReply(ctx, repository, pullRequest, target, text)
	return change, true, err
}

// replyTarget is a review thread or a conversation comment of a pull request.
type replyTarget struct {
	// id is the id of the first comment of the review thread, or of the conversation comment.
	id int64
	// review is true for a review thread, and false for a conversation comment.
	review bool
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
			return replyTarget{id: id, review: true}, true, nil
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

// sendReply adds the reply with text to the target on the pull request as an outgoing change, and sends it at once. It
// gives the id of the change. A reply in a review thread resolves the thread. A conversation comment has no thread, so
// the reply is a new comment that quotes it, and nobody can resolve it.
func (e *Engine) sendReply(ctx context.Context, repository github.Repository, pullRequest int64, target replyTarget, text string) (int64, error) {
	field, replyTo := commentField, int64(0)
	if target.review {
		field, replyTo = replyResolveField, target.id
	} else {
		text = quote(target.body) + "\n\n" + text
	}
	id, err := e.addOutgoingChange(ctx, e.queries, repository.FullName, pullRequest, replyTo, field, text)
	if err != nil {
		return 0, err
	}
	return id, e.sendNow(ctx, repository, id)
}

// quote gives text with "> " before each line.
func quote(text string) string {
	var quoted []string
	for line := range strings.Lines(text) {
		quoted = append(quoted, "> "+strings.TrimSuffix(line, "\n"))
	}
	return strings.Join(quoted, "\n")
}
