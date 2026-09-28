use std::collections::HashSet;
use std::error::Error;
use std::fs;

use time::OffsetDateTime;

use crate::{Engine, TIME_FORMAT, implementer, lead_events};

pub(crate) fn spawn(engine: Engine) {
    tokio::spawn(async move {
        loop {
            if let Err(error) = clean(&engine).await {
                eprintln!("mobius: Housekeeper: {error}");
            }
            tokio::time::sleep(engine.config.housekeeper_interval).await;
        }
    });
}

// The name of a directory tells its owner: `task-<issue>` a live task, `review-<id>`, `judge-<id>`, and `research-<id>` a session, and `scratch/<id>` a session. A Lead directory and a bare clone are not below `worktrees/` or `scratch/`.
async fn clean(engine: &Engine) -> Result<(), Box<dyn Error + Send + Sync>> {
    let data_dir = &engine.config.data_dir;
    let _git = engine.git.lock().await;
    let open: HashSet<String> = engine
        .store
        .sessions()
        .open_ids()
        .await?
        .into_iter()
        .map(|id| id.to_string())
        .collect();
    let mut repositories = HashSet::new();
    for (repository, name) in mobius_runner::worktree_dirs(data_dir)? {
        let owned = match name.split_once('-') {
            Some(("task", issue)) => engine
                .store
                .tasks()
                .live_in(&repository)
                .await?
                .iter()
                .any(|task| task.issue.to_string() == issue),
            Some(("review" | "judge" | "research", id)) => open.contains(id),
            _ => true,
        };
        if !owned {
            fs::remove_dir_all(data_dir.join("worktrees").join(&repository).join(&name))?;
        }
        repositories.insert(repository);
    }
    for repository in repositories {
        mobius_runner::prune(data_dir, &repository).await?;
    }
    for id in mobius_runner::scratch_ids(data_dir)? {
        if !open.contains(&id) {
            fs::remove_dir_all(data_dir.join("scratch").join(&id))?;
        }
    }
    Ok(())
}

// At `max_worker_restarts`, the task goes to a human, and the Lead gets a stop event.
pub(crate) async fn restart(
    engine: &Engine,
    repository: &str,
    workstream: i64,
    task: i64,
    number: i64,
    title: &str,
) -> Result<bool, Box<dyn Error + Send + Sync>> {
    let max = engine.config.max_worker_restarts;
    if engine.store.tasks().add_worker_restart(task, max).await? {
        return Ok(true);
    }
    if !implementer::hand_to_human(engine, repository, task, number).await? {
        return Ok(false);
    }
    let text = format!(
        "{} stop of #{number} \"{title}\": the Worker failed after {max} restarts. Mobius added mobius:needs-human.",
        OffsetDateTime::now_utc().format(TIME_FORMAT)?
    );
    lead_events::add(engine, repository, workstream, "stop", &text).await?;
    Ok(false)
}
