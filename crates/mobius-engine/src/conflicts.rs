use std::error::Error;

use humantime_serde::re::humantime::format_duration;
use mobius_domain::InboxKind;
use mobius_github::{PullRequest, Repository};
use mobius_store::Task;
use time::OffsetDateTime;

use crate::{Engine, NEEDS_HUMAN_LABEL, TIME_FORMAT, implementer, inbox, lead_events};

pub(crate) async fn check(
    engine: &Engine,
    repository: &Repository,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let tasks = engine
        .store
        .tasks()
        .in_state(&repository.full_name, "ready_for_review")
        .await?;
    for task in tasks {
        let Some(number) = task.pull_request else {
            continue;
        };
        let pull_request = repository.pull_request(number).await?;
        if pull_request.mergeable != Some(false) {
            continue;
        }
        if OffsetDateTime::now_utc() - pull_request.created_at > engine.config.stale_pr_age {
            stale(engine, repository, &task, &pull_request).await?;
        } else {
            implementer::conflict_round(engine, repository, &task, pull_request).await?;
        }
    }
    Ok(())
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
    inbox::add(
        engine,
        InboxKind::StalePullRequest,
        name,
        task.workstream,
        task.issue,
        &format!(
            "Pull request #{} of #{} \"{title}\" has a merge conflict and is older than {age}.",
            pull_request.number, task.issue
        ),
        &pull_request.html_url,
    )
    .await?;
    let text = format!(
        "{} stale pull request #{} of #{} \"{title}\": it has a merge conflict and is older than {age}. {}",
        OffsetDateTime::now_utc().format(TIME_FORMAT)?,
        pull_request.number,
        task.issue,
        pull_request.html_url
    );
    lead_events::add(engine, name, task.workstream, "stale_pull_request", &text).await
}
