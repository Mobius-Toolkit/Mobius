use std::error::Error;
use std::fmt::Write;

use mobius_github::{PullRequest, Repository};
use mobius_store::Task;

use crate::{Engine, implementer};

// Gives `true` when a failed check run of the head starts a fix round.
pub(crate) async fn on_failure(
    engine: &Engine,
    repository: &Repository,
    task: &Task,
    pull_request: &PullRequest,
) -> Result<bool, Box<dyn Error + Send + Sync>> {
    let mut items = String::new();
    for check_run in repository.check_runs(&pull_request.head.sha).await? {
        let failed = check_run.status == "completed"
            && matches!(
                check_run.conclusion.as_deref(),
                Some("failure" | "timed_out")
            );
        if check_run.name == implementer::CHECK_RUN || !failed {
            continue;
        }
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
    let tasks = engine.store.tasks();
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
        parent: None,
    };
    if let Err(error) = implementer::fix_round(engine, repository, round).await {
        tasks
            .set_state(task.id, "working", "ready_for_review")
            .await?;
        return Err(error);
    }
    Ok(true)
}
