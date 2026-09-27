use std::error::Error;

use mobius_github::Repository;
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
    // GitHub gives each reply the id of the first comment of its thread as `in_reply_to_id`.
    let comments = repository.review_comments(number).await?;
    for root in &comments {
        if root.in_reply_to_id.is_some() || !trusted(&root.user.login) {
            continue;
        }
        let line = root
            .line
            .map(|line| format!(" line {line}"))
            .unwrap_or_default();
        text.push_str(&format!("\nThread {}, {}{line}:\n", root.id, root.path));
        for comment in &comments {
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
    }
    Ok(text)
}

fn entry(
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
