use std::error::Error;
use std::fmt::Write;

use mobius_github::{CheckRun, PullRequest, Repository};
use mobius_store::Task;

use crate::{Engine, implementer, lead};

// Gives the completed check runs of other apps that failed on the head.
pub async fn failed(
    repository: &Repository,
    head_sha: &str,
) -> Result<Vec<CheckRun>, Box<dyn Error + Send + Sync>> {
    Ok(repository
        .check_runs(head_sha)
        .await?
        .into_iter()
        .filter(|check_run| {
            check_run.name != implementer::CHECK_RUN
                && check_run.status == "completed"
                && matches!(
                    check_run.conclusion.as_deref(),
                    Some("failure" | "timed_out" | "cancelled")
                )
        })
        .collect())
}

// Gives `true` when a failed check run of the head starts a fix round. A head gets one round.
pub(crate) async fn on_failure(
    engine: &Engine,
    repository: &Repository,
    task: &Task,
    pull_request: &PullRequest,
) -> Result<bool, Box<dyn Error + Send + Sync>> {
    let tasks = engine.store.tasks();
    let head = &pull_request.head.sha;
    if tasks.check_head(task.id).await?.as_deref() == Some(head) {
        return Ok(false);
    }
    let mut items = String::new();
    for check_run in failed(repository, head).await? {
        write!(
            items,
            "\nCheck run \"{}\", {}:\n{}\n\n{}\n",
            check_run.name,
            check_run.html_url.unwrap_or_default(),
            check_run.output.title.unwrap_or_default(),
            check_run.output.summary.unwrap_or_default()
        )?;
        for annotation in repository.check_run_annotations(check_run.id).await? {
            writeln!(
                items,
                "- {} line {}: {}",
                annotation.path, annotation.start_line, annotation.message
            )?;
        }
        items.push_str("\nAction: fix\n");
    }
    if items.is_empty() {
        return Ok(false);
    }
    let title = repository
        .issue(task.issue)
        .await?
        .ok_or_else(|| format!("#{} does not exist.", task.issue))?
        .title;
    let parent =
        lead::newest_session(engine, &repository.full_name, task.workstream, task.issue).await?;
    if !tasks
        .set_state(task.id, "ready_for_review", "working")
        .await?
    {
        return Ok(true);
    }
    let round = implementer::Round {
        repository: repository.full_name.clone(),
        workstream: task.workstream,
        task: task.id,
        number: task.issue,
        title,
        branch: task.branch.clone().unwrap_or_default(),
        pull_request: pull_request.clone(),
        check_run: None,
        counts: true,
        items,
        parent,
    };
    if let Err(error) = implementer::fix_round(engine, repository, round).await {
        tasks
            .set_state(task.id, "working", "ready_for_review")
            .await?;
        return Err(error);
    }
    tasks.set_check_head(task.id, head).await?;
    Ok(true)
}
