use std::error::Error;

use mobius_github::Repository;

use crate::trust::trusted_author;
use crate::{Engine, WORKING_LABEL, WORKSTREAM_LABEL, activity, dispatch};

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
    dispatch::dispatch_ready(engine, app_slug, repository).await
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
            if event.event == "labeled"
                && label.name == WORKSTREAM_LABEL
                && cursor.since.is_none_or(|since| event.created_at > since)
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
