use std::collections::VecDeque;
use std::error::Error;

use mobius_domain::{Blocker, TaskLine};
use mobius_github::{Issue, Repository};

use crate::{Engine, WORKSTREAM_LABEL, ends, trust, workstreams};

// Issues below a Workstream issue of their own belong to that Workstream.
pub async fn list(
    engine: &Engine,
    repository: &str,
    workstream: i64,
) -> Result<Vec<TaskLine>, Box<dyn Error + Send + Sync>> {
    let repository = engine.repository(repository)?;
    let trusted = trust::trusted_authors(engine, &repository);
    let mut lines = Vec::new();
    let mut parents = VecDeque::from([workstream]);
    while let Some(parent) = parents.pop_front() {
        for issue in repository.sub_issues(parent).await? {
            if issue.has_label(WORKSTREAM_LABEL) {
                continue;
            }
            if issue.state == "open" && trusted(&issue.user.login) {
                let mut line = task_line(&issue);
                if issue.issue_dependencies_summary.blocked_by > 0 {
                    line.blocked_by = blockers(&repository, workstream, issue.number).await?;
                }
                if line.state == "working"
                    && engine
                        .store
                        .tasks()
                        .live(&repository.full_name, issue.number)
                        .await?
                        .is_some_and(|task| task.state == "queued")
                {
                    line.state = "queued".to_string();
                }
                lines.push(line);
            }
            parents.push_back(issue.number);
        }
    }
    Ok(lines)
}

async fn blockers(
    repository: &Repository,
    workstream: i64,
    number: i64,
) -> Result<Vec<Blocker>, Box<dyn Error + Send + Sync>> {
    let mut blockers = Vec::new();
    for blocker in repository.blocked_by(number).await? {
        if blocker.state != "open" {
            continue;
        }
        if ends::in_other_repository(&blocker, &repository.full_name) {
            continue;
        }
        let other_workstream = match workstreams::workstream_of(repository, blocker.number).await? {
            Some(number) if number != workstream => repository.issue(number).await?,
            _ => None,
        };
        blockers.push(Blocker {
            number: blocker.number,
            workstream_title: other_workstream.map(|issue| issue.title),
        });
    }
    Ok(blockers)
}

fn task_line(issue: &Issue) -> TaskLine {
    let state = issue
        .labels
        .iter()
        .find_map(|label| label.name.strip_prefix("mobius:"))
        .unwrap_or("open");
    TaskLine {
        number: issue.number,
        title: issue.title.clone(),
        state: state.to_string(),
        url: issue.html_url.clone(),
        blocked_by: Vec::new(),
    }
}

pub(crate) fn text(lines: &[TaskLine]) -> String {
    lines
        .iter()
        .map(|line| {
            let blockers: Vec<String> = line
                .blocked_by
                .iter()
                .map(|blocker| match &blocker.workstream_title {
                    Some(title) => format!("#{} (Workstream \"{title}\")", blocker.number),
                    None => format!("#{}", blocker.number),
                })
                .collect();
            let blocked_by = if blockers.is_empty() {
                String::new()
            } else {
                format!(", blocked by {}", blockers.join(", "))
            };
            format!(
                "#{} {}: {}{blocked_by}\n",
                line.number, line.title, line.state
            )
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use mobius_github::{DependenciesSummary, Label, User};

    use super::*;

    fn issue(labels: &[&str]) -> Issue {
        Issue {
            id: 100_041,
            number: 41,
            title: "Add plan model".to_string(),
            body: None,
            state: "open".to_string(),
            html_url: "https://github.com/owner/shop/issues/41".to_string(),
            repository_url: "https://api.github.com/repos/owner/shop".to_string(),
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
            issue_dependencies_summary: DependenciesSummary { blocked_by: 0 },
        }
    }

    #[test]
    fn a_task_line_shows_the_mobius_label() {
        assert_eq!(
            text(&[task_line(&issue(&["bug", "mobius:working"]))]),
            "#41 Add plan model: working\n"
        );
    }

    #[test]
    fn a_task_line_shows_its_blockers_and_the_workstream_of_a_blocker_in_another_workstream() {
        let mut line = task_line(&issue(&["mobius:ready"]));
        line.blocked_by = vec![
            Blocker {
                number: 40,
                workstream_title: None,
            },
            Blocker {
                number: 88,
                workstream_title: Some("Billing".to_string()),
            },
        ];

        assert_eq!(
            text(&[line]),
            "#41 Add plan model: ready, blocked by #40, #88 (Workstream \"Billing\")\n"
        );
    }

    #[test]
    fn a_task_line_with_no_mobius_label_shows_open() {
        assert_eq!(
            text(&[task_line(&issue(&["bug"]))]),
            "#41 Add plan model: open\n"
        );
    }
}
