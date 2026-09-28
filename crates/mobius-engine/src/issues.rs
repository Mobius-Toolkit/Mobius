use std::error::Error;

use mobius_github::{Repository, ReviewComment};
use time::OffsetDateTime;

use crate::TIME_FORMAT;

// An issue, a review thread, or a comment of an author that `trusted` refuses is absent from the text.
pub(crate) async fn read_issue(
    repository: &Repository,
    number: i64,
    trusted: impl Fn(&str) -> bool,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let issue = repository
        .issue(number)
        .await?
        .filter(|issue| trusted(&issue.user.login))
        .ok_or_else(|| {
            format!(
                "#{number} is not an issue or a pull request of {}.",
                repository.full_name
            )
        })?;
    let kind = match issue.pull_request {
        Some(_) => "pull request",
        None => "issue",
    };
    let mut text = format!(
        "#{number} {} ({kind}, {})\n\n{}\n\n# Comments\n",
        issue.title,
        issue.state,
        issue.body.unwrap_or_default()
    );
    for comment in repository.issue_comments(number).await? {
        if trusted(&comment.user.login) {
            text.push_str(&entry(
                &comment.user.login,
                comment.created_at,
                "",
                &comment.body,
            )?);
        }
    }
    if issue.pull_request.is_none() {
        return Ok(text);
    }
    text.push_str("\n# Reviews\n");
    for review in repository.reviews(number).await? {
        if let Some(time) = review.submitted_at
            && trusted(&review.user.login)
        {
            let state = format!(", {}", review.state);
            text.push_str(&entry(&review.user.login, time, &state, &review.body)?);
        }
    }
    text.push_str("\n# Review threads\n");
    text.push_str(&review_threads(repository, number, trusted).await?);
    Ok(text)
}

pub(crate) async fn review_threads(
    repository: &Repository,
    number: i64,
    trusted: impl Fn(&str) -> bool,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let comments = repository.review_comments(number).await?;
    let mut text = String::new();
    for root in &comments {
        if root.in_reply_to_id.is_none() && trusted(&root.user.login) {
            text.push_str(&thread(&comments, root, &trusted)?);
        }
    }
    Ok(text)
}

// Gives each thread that starts with a comment in `roots`, with the action `fix`.
pub(crate) async fn fix_threads(
    repository: &Repository,
    number: i64,
    roots: &[i64],
    trusted: impl Fn(&str) -> bool,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let comments = repository.review_comments(number).await?;
    let mut text = String::new();
    for root in comments
        .iter()
        .filter(|comment| roots.contains(&comment.id))
    {
        text.push_str(&thread(&comments, root, &trusted)?);
        text.push_str("\nAction: fix\n");
    }
    Ok(text)
}

// GitHub gives each reply the id of the first comment of its thread as `in_reply_to_id`.
pub(crate) fn thread(
    comments: &[ReviewComment],
    root: &ReviewComment,
    trusted: impl Fn(&str) -> bool,
) -> Result<String, time::error::Format> {
    let line = root
        .line
        .map(|line| format!(" line {line}"))
        .unwrap_or_default();
    let mut text = format!("\nThread {}, {}{line}:\n", root.id, root.path);
    for comment in comments {
        if (comment.id == root.id || comment.in_reply_to_id == Some(root.id))
            && trusted(&comment.user.login)
        {
            text.push_str(&entry(
                &comment.user.login,
                comment.created_at,
                "",
                &comment.body,
            )?);
        }
    }
    Ok(text)
}

pub(crate) fn entry(
    login: &str,
    time: OffsetDateTime,
    state: &str,
    body: &str,
) -> Result<String, time::error::Format> {
    Ok(format!(
        "\n@{login}, {}{state}:\n{body}\n",
        time.format(TIME_FORMAT)?
    ))
}
