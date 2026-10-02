use std::error::Error;

use humantime_serde::re::humantime::format_duration;
use mobius_domain::InboxKind;
use mobius_github::{PullRequest, Repository};
use mobius_store::Task;
use time::OffsetDateTime;

use crate::labels::NEEDS_HUMAN_LABEL;
use crate::{Engine, TIME_FORMAT, implementer, inbox, lead_events};

pub fn behind(pull_request: &PullRequest) -> bool {
    pull_request.mergeable_state.as_deref() == Some("behind")
}

// Gives `false` when the pull request is stale, so no round starts.
pub(crate) async fn on_conflict(
    engine: &Engine,
    repository: &Repository,
    task: &Task,
    pull_request: PullRequest,
) -> Result<bool, Box<dyn Error + Send + Sync>> {
    if OffsetDateTime::now_utc() - pull_request.created_at > engine.config.stale_pr_age {
        stale(engine, repository, task, &pull_request).await?;
        Ok(false)
    } else {
        implementer::conflict_round(engine, repository, task, pull_request).await?;
        Ok(true)
    }
}

async fn stale(
    engine: &Engine,
    repository: &Repository,
    task: &Task,
    pull_request: &PullRequest,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let name = &repository.full_name;
    if !engine
        .store
        .tasks()
        .set_state(task.id, "ready_for_review", "needs_human")
        .await?
    {
        return Ok(());
    }
    repository.add_label(task.issue, NEEDS_HUMAN_LABEL).await?;
    let title = repository
        .issue(task.issue)
        .await?
        .ok_or_else(|| format!("#{} does not exist.", task.issue))?
        .title;
    let age = format_duration(engine.config.stale_pr_age);
    let reason = if behind(pull_request) {
        "is behind its base branch"
    } else {
        "has a merge conflict"
    };
    inbox::add(
        engine,
        InboxKind::StalePullRequest,
        name,
        task.workstream,
        task.issue,
        &format!(
            "Pull request #{} of #{} \"{title}\" {reason} and is older than {age}.",
            pull_request.number, task.issue
        ),
        &pull_request.html_url,
    )
    .await?;
    let text = format!(
        "{} stale pull request #{} of #{} \"{title}\": it {reason} and is older than {age}. {}",
        OffsetDateTime::now_utc().format(TIME_FORMAT)?,
        pull_request.number,
        task.issue,
        pull_request.html_url
    );
    lead_events::add(
        engine,
        name,
        task.workstream,
        Some(task.issue),
        "stale_pull_request",
        &text,
    )
    .await
}
