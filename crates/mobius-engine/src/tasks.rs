use std::collections::VecDeque;
use std::error::Error;

use mobius_github::{Issue, Repository};

use crate::WORKSTREAM_LABEL;

// Issues below a Workstream issue of their own belong to that Workstream.
pub(crate) async fn task_list(
    repository: &Repository,
    workstream: i64,
    trusted: impl Fn(&str) -> bool,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let mut lines = String::new();
    let mut parents = VecDeque::from([workstream]);
    while let Some(parent) = parents.pop_front() {
        for issue in repository.sub_issues(parent).await? {
            if issue.has_label(WORKSTREAM_LABEL) {
                continue;
            }
            if issue.state == "open" && trusted(&issue.user.login) {
                lines.push_str(&task_line(&issue));
            }
            parents.push_back(issue.number);
        }
    }
    Ok(lines)
}

fn task_line(issue: &Issue) -> String {
    let state = issue
        .labels
        .iter()
        .find_map(|label| label.name.strip_prefix("mobius:"))
        .unwrap_or("open");
    format!("#{} {}: {state}\n", issue.number, issue.title)
}

#[cfg(test)]
mod tests {
    use mobius_github::{Label, User};

    use super::*;

    fn issue(labels: &[&str]) -> Issue {
        Issue {
            number: 41,
            title: "Add plan model".to_string(),
            body: None,
            state: "open".to_string(),
            html_url: "https://github.com/owner/shop/issues/41".to_string(),
            updated_at: time::OffsetDateTime::UNIX_EPOCH,
            labels: labels
                .iter()
                .map(|name| Label {
                    name: name.to_string(),
                })
                .collect(),
            pull_request: None,
            user: User {
                login: "owner".to_string(),
            },
        }
    }

    #[test]
    fn a_task_line_shows_the_mobius_label() {
        assert_eq!(
            task_line(&issue(&["bug", "mobius:working"])),
            "#41 Add plan model: working\n"
        );
    }

    #[test]
    fn a_task_line_with_no_mobius_label_shows_open() {
        assert_eq!(task_line(&issue(&["bug"])), "#41 Add plan model: open\n");
    }
}
