use std::error::Error;

use mobius_domain::Live;
use mobius_github::Repository;
use time::OffsetDateTime;

use crate::trust::trusted_author;
use crate::workstreams::AUTOPILOT_LABEL;
use crate::{Engine, WORKING_LABEL, WORKSTREAM_LABEL, activity, dispatch, ends, lead_events};

const ISSUES: &str = "issues";

pub(crate) fn spawn(engine: Engine) {
    tokio::spawn(async move {
        loop {
            if let Err(error) = poll(&engine).await {
                eprintln!("mobius: GitHub poll: {error}");
            }
            tokio::time::sleep(engine.config.poll_interval).await;
        }
    });
}

async fn poll(engine: &Engine) -> Result<(), Box<dyn Error + Send + Sync>> {
    let Some(app) = engine.store.github_app().get().await? else {
        return Ok(());
    };
    let repositories = engine
        .github
        .repositories(app.app_id, &app.private_key)
        .await?;
    *engine.repositories.write().unwrap() = repositories.clone();
    ends::lost_access(engine, &repositories).await?;
    for repository in &repositories {
        if let Err(error) = poll_repository(engine, &app.slug, repository).await {
            eprintln!("mobius: GitHub poll of {}: {error}", repository.full_name);
        }
    }
    Ok(())
}

async fn poll_repository(
    engine: &Engine,
    app_slug: &str,
    repository: &Repository,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    changed_issues(engine, app_slug, repository).await?;
    dispatch::dispatch_ready(engine, app_slug, repository).await?;
    ends::check(engine, app_slug, repository).await
}

async fn changed_issues(
    engine: &Engine,
    app_slug: &str,
    repository: &Repository,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let name = &repository.full_name;
    let cursor = engine.store.sync_cursors().get(name, ISSUES).await?;
    let Some(page) = repository
        .issues_since(cursor.since, cursor.etag.as_deref())
        .await?
    else {
        return Ok(());
    };
    for issue in &page.issues {
        if issue.pull_request.is_some() {
            dispatch::pull_request_comments(engine, repository, issue.number, cursor.since).await?;
            continue;
        }
        if issue.has_label(WORKING_LABEL) {
            dispatch::comment_events(engine, app_slug, repository, issue, cursor.since).await?;
        }
        if !issue.has_label(WORKSTREAM_LABEL) {
            continue;
        }
        for event in repository.issue_events(issue.number).await? {
            let (Some(actor), Some(label)) = (event.actor, event.label) else {
                continue;
            };
            if cursor.since.is_some_and(|since| event.created_at <= since) {
                continue;
            }
            // With a new Autopilot, a `mobius:ready` of the Mobius App can dispatch, so the ready list must not answer `304`.
            if label.name == AUTOPILOT_LABEL {
                engine
                    .store
                    .sync_cursors()
                    .set(name, dispatch::READY_CURSOR, None, None)
                    .await?;
                engine.broadcast(Live::Workstreams);
            }
            if event.event == "labeled"
                && label.name == WORKSTREAM_LABEL
                && trusted_author(&engine.config, app_slug, &actor.login)
            {
                activity::add(
                    engine,
                    name,
                    issue.number,
                    issue.number,
                    &actor.login,
                    &format!("New Workstream \"{}\"", issue.title),
                    &issue.html_url,
                )
                .await?;
                // At the first poll of a repository, Mobius cannot see which Workstream is new.
                if cursor.since.is_none() {
                    continue;
                }
                let text = dispatch::event_text(
                    OffsetDateTime::now_utc(),
                    "creation of Workstream",
                    issue,
                    &actor.login,
                    issue.body.as_deref().unwrap_or_default(),
                )?;
                lead_events::add(engine, name, issue.number, "creation", &text).await?;
            }
        }
    }
    let since = page
        .issues
        .iter()
        .map(|issue| issue.updated_at)
        .max()
        .or(cursor.since);
    engine
        .store
        .sync_cursors()
        .set(name, ISSUES, since, page.etag.as_deref())
        .await
}
