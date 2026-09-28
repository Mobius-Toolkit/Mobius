use std::error::Error;

use mobius_domain::{InboxKind, Live};
use mobius_github::Repository;
use mobius_store::Task;

use crate::{
    Engine, NEEDS_HUMAN_LABEL, WORKING_LABEL, implementer, judge, lead_events, reviewer,
    workstreams,
};

const LOST_TEXT: &str = "Mobius lost the state of this task. Add mobius:ready to start again.";

// The sessions of the process before the restart have no Harness process, so they end, and the Housekeeper removes their directories.
pub(crate) async fn start(engine: &Engine) -> Result<(), Box<dyn Error + Send + Sync>> {
    let sessions = engine.store.sessions();
    for id in sessions.open_ids().await? {
        sessions.end(id, "restart").await?;
    }
    Ok(())
}

// The work of a repository starts again at its first poll, because the work needs GitHub.
pub(crate) async fn repository(
    engine: &Engine,
    repository: &Repository,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let name = &repository.full_name;
    if engine.recovered.lock().unwrap().contains(name) {
        return Ok(());
    }
    lost_tasks(engine, repository).await?;
    // A later poll finds the lost tasks again, but the restarts run only one time.
    if !engine.recovered.lock().unwrap().insert(name.clone()) {
        return Ok(());
    }
    for task in engine.store.tasks().live_in(name).await? {
        if matches!(task.state.as_str(), "queued" | "working")
            && let Err(error) = restart(engine, repository, &task).await
        {
            eprintln!("mobius: restart of {name}#{}: {error}", task.issue);
        }
    }
    for workstream in engine.store.lead_events().waiting_workstreams(name).await? {
        lead_events::wake(engine, name, workstream);
    }
    Ok(())
}

// An issue with `mobius:working` and no task row lost its task with the store. The Inbox item comes before the label change, so a later poll finds the issue again after a failure.
async fn lost_tasks(
    engine: &Engine,
    repository: &Repository,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let name = &repository.full_name;
    for issue in repository.open_issues_with_label(WORKING_LABEL).await? {
        if issue.pull_request.is_some()
            || engine
                .store
                .tasks()
                .live(name, issue.number)
                .await?
                .is_some()
        {
            continue;
        }
        let workstream = workstreams::workstream_of(repository, issue.number)
            .await?
            .unwrap_or_default();
        let item = engine
            .store
            .inbox_items()
            .add(
                InboxKind::Stopped,
                name,
                workstream,
                issue.number,
                LOST_TEXT,
                &issue.html_url,
            )
            .await?;
        engine.broadcast(Live::Inbox(item));
        repository
            .add_label(issue.number, NEEDS_HUMAN_LABEL)
            .await?;
        repository.remove_label(issue.number, WORKING_LABEL).await?;
    }
    Ok(())
}

// A restart does not count toward `max_worker_restarts`.
async fn restart(
    engine: &Engine,
    repository: &Repository,
    task: &Task,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    match task.worker.as_deref() {
        Some(implementer::ROLE | implementer::CONFLICT_ROUND) => {
            implementer::restart(engine, repository, task).await
        }
        Some(reviewer::ROLE) => reviewer::restart(engine, repository, task).await,
        // The poll gives the items to a new Judge.
        Some(judge::ROLE) => {
            let before = task.worker_input.as_deref().unwrap_or("reviewed");
            engine
                .store
                .tasks()
                .set_state(task.id, "working", before)
                .await?;
            Ok(())
        }
        _ => Ok(()),
    }
}
