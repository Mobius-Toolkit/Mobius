use std::error::Error;

use mobius_github::Repository;

use crate::labels::{AUTOPILOT_LABEL, WORKSTREAM_LABEL};
use crate::trust::{app_login, trusted_author};
use crate::{Engine, dispatch, ends, tasks, workstreams};

// Starts the tasks of each open Workstream with Autopilot, in the order of the sub-issues, while a worker slot is free.
// An issue that had a task before, also an ended one, does not start again: only the Owner or a trusted user restarts it.
pub(crate) async fn start(
    engine: &Engine,
    app_slug: &str,
    repository: &Repository,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    // The drain holds each new dispatch.
    if engine.drain.on() {
        return Ok(());
    }
    let name = &repository.full_name;
    for workstream in repository.open_issues_with_label(WORKSTREAM_LABEL).await? {
        if !workstream.has_label(AUTOPILOT_LABEL)
            || !workstreams::issue_autopilot(engine, repository, &workstream).await?
        {
            continue;
        }
        let mut frames = vec![(
            0,
            repository.sub_issues(workstream.number).await?.into_iter(),
        )];
        while let Some((_, issue)) = tasks::next_issue(&mut frames) {
            if issue.has_label(WORKSTREAM_LABEL) || ends::in_other_repository(&issue, name) {
                continue;
            }
            if issue.state == "open"
                && trusted_author(&engine.config, app_slug, &issue.user.login)
                && issue.issue_dependencies_summary.blocked_by == 0
                && !engine.store.tasks().exists(name, issue.number).await?
            {
                if engine.store.tasks().active_count().await? >= i64::from(engine.config.max_agents)
                {
                    return Ok(());
                }
                dispatch::dispatch(
                    engine,
                    repository,
                    &issue,
                    workstream.number,
                    &app_login(app_slug),
                )
                .await?;
            }
            frames.push((0, repository.sub_issues(issue.number).await?.into_iter()));
        }
    }
    Ok(())
}
