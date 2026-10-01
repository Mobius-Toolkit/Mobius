use std::error::Error;

use mobius_github::Repository;
use serde::Deserialize;

use crate::labels::{READY_LABEL, WORKSTREAM_LABEL};
use crate::{Engine, trust, workstreams};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct NewIssue {
    pub(crate) title: String,
    pub(crate) body: String,
    pub(crate) parent: i64,
    pub(crate) blocked_by: Vec<i64>,
    pub(crate) ready: bool,
}

pub(crate) async fn create_issue(
    engine: &Engine,
    repository: &Repository,
    workstream: i64,
    new: &NewIssue,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    if new.parent != workstream && !in_workstream(repository, workstream, new.parent).await? {
        return Err(format!("#{} is not in this Workstream.", new.parent).into());
    }
    if new.ready && !workstreams::autopilot(engine, repository, workstream).await? {
        return Err("ready needs Autopilot on the Workstream issue.".into());
    }
    let mut blocker_ids = Vec::new();
    for number in &new.blocked_by {
        let blocker = repository
            .issue(*number)
            .await?
            .filter(|issue| issue.pull_request.is_none())
            .ok_or_else(|| format!("#{number} is not an issue of the repository."))?;
        if blocker_ids.contains(&blocker.id) {
            return Err(format!("#{number} is two times in blocked_by.").into());
        }
        blocker_ids.push(blocker.id);
    }
    let issue = repository.create_issue(&new.title, &new.body).await?;
    repository.add_sub_issue(new.parent, issue.id).await?;
    for id in blocker_ids {
        repository.add_blocked_by(issue.number, id).await?;
    }
    if new.ready {
        repository.add_label(issue.number, READY_LABEL).await?;
    }
    Ok(format!("Created #{}.", issue.number))
}

pub(crate) async fn mark_ready(
    engine: &Engine,
    repository: &Repository,
    workstream: i64,
    number: i64,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let trusted = trust::trusted_authors(engine, repository);
    if !repository
        .issue(number)
        .await?
        .is_some_and(|issue| trusted(&issue.user.login))
    {
        return Err(format!("#{number} is not an issue of a trusted author.").into());
    }
    if !in_workstream(repository, workstream, number).await? {
        return Err(format!("#{number} is not in this Workstream.").into());
    }
    if !workstreams::autopilot(engine, repository, workstream).await? {
        return Err("mark_ready needs Autopilot on the Workstream issue.".into());
    }
    repository.add_label(number, READY_LABEL).await?;
    Ok(format!("Marked #{number} ready."))
}

// An issue with a Workstream issue of its own below this Workstream belongs to that Workstream.
pub(crate) async fn in_workstream(
    repository: &Repository,
    workstream: i64,
    number: i64,
) -> Result<bool, Box<dyn Error + Send + Sync>> {
    let Some(issue) = repository.issue(number).await? else {
        return Ok(false);
    };
    Ok(!issue.has_label(WORKSTREAM_LABEL)
        && workstreams::workstream_of(repository, number).await? == Some(workstream))
}
