use std::error::Error;

use mobius_domain::Workstream;
use mobius_github::{Issue, IssueEvent, Repository};

use crate::config::Config;
use crate::{Engine, WORKSTREAM_LABEL};

pub(crate) const AUTOPILOT_LABEL: &str = "mobius:autopilot";

pub async fn list(engine: &Engine) -> Result<Vec<Workstream>, Box<dyn Error + Send + Sync>> {
    let repositories = engine.repositories.read().unwrap().clone();
    let mut workstreams = Vec::new();
    for repository in repositories {
        for issue in repository.open_issues_with_label(WORKSTREAM_LABEL).await? {
            workstreams.push(Workstream {
                repository: repository.full_name.clone(),
                number: issue.number,
                autopilot: issue_autopilot(engine, &repository, &issue).await?,
                title: issue.title,
            });
        }
    }
    Ok(workstreams)
}

pub(crate) async fn workstream_of(
    repository: &Repository,
    number: i64,
) -> Result<Option<i64>, Box<dyn Error + Send + Sync>> {
    let mut number = number;
    while let Some(parent) = repository.parent(number).await? {
        if parent.has_label(WORKSTREAM_LABEL) {
            return Ok(Some(parent.number));
        }
        number = parent.number;
    }
    Ok(None)
}

pub(crate) async fn autopilot(
    engine: &Engine,
    repository: &Repository,
    workstream: i64,
) -> Result<bool, Box<dyn Error + Send + Sync>> {
    match repository.issue(workstream).await? {
        Some(issue) => issue_autopilot(engine, repository, &issue).await,
        None => Ok(false),
    }
}

async fn issue_autopilot(
    engine: &Engine,
    repository: &Repository,
    issue: &Issue,
) -> Result<bool, Box<dyn Error + Send + Sync>> {
    if !issue.has_label(AUTOPILOT_LABEL) {
        return Ok(false);
    }
    let events = repository.issue_events(issue.number).await?;
    Ok(added_by_trusted_user(&engine.config, &events))
}

// The last `labeled` event of `mobius:autopilot` decides, so a trusted bot or the Mobius App cannot turn Autopilot on.
fn added_by_trusted_user(config: &Config, events: &[IssueEvent]) -> bool {
    events
        .iter()
        .rev()
        .find(|event| {
            event.event == "labeled"
                && event
                    .label
                    .as_ref()
                    .is_some_and(|label| label.name == AUTOPILOT_LABEL)
        })
        .and_then(|event| event.actor.as_ref())
        .is_some_and(|actor| {
            config
                .trusted_users
                .iter()
                .any(|user| user.eq_ignore_ascii_case(&actor.login))
        })
}

#[cfg(test)]
mod tests {
    use mobius_github::{Label, User};
    use time::OffsetDateTime;

    use super::*;

    fn config() -> Config {
        crate::config::parse(
            r#"
access_password = "correct horse"
trusted_users = ["owner"]
trusted_bots = ["coderabbitai[bot]"]

[roles]
lead        = { harness = "claude-code", model = "opus",    effort = "high" }
triager     = { harness = "claude-code", model = "sonnet",  effort = "medium" }
implementer = { harness = "devin",       model = "swe-1.5", effort = "high" }
researcher  = { harness = "antigravity", model = "gemini-3-pro" }
reviewer    = { harness = "claude-code", model = "opus",    effort = "high" }
judge       = { harness = "claude-code", model = "haiku",   effort = "low" }
"#,
        )
        .unwrap()
    }

    fn event(event: &str, label: &str, actor: &str) -> IssueEvent {
        IssueEvent {
            event: event.to_string(),
            actor: Some(User {
                login: actor.to_string(),
            }),
            label: Some(Label {
                name: label.to_string(),
            }),
            created_at: OffsetDateTime::UNIX_EPOCH,
        }
    }

    #[test]
    fn autopilot_of_a_trusted_user_counts() {
        assert!(added_by_trusted_user(
            &config(),
            &[
                event("labeled", "mobius:autopilot", "Owner"),
                event("labeled", "bug", "mallory")
            ]
        ));
    }

    #[test]
    fn autopilot_of_a_bot_the_mobius_app_or_a_stranger_does_not_count() {
        for actor in ["coderabbitai[bot]", "mobius-app[bot]", "mallory"] {
            assert!(!added_by_trusted_user(
                &config(),
                &[
                    event("labeled", "mobius:autopilot", "owner"),
                    event("unlabeled", "mobius:autopilot", actor),
                    event("labeled", "mobius:autopilot", actor)
                ]
            ));
        }
    }
}
