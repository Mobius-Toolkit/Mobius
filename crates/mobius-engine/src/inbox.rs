use std::error::Error;

use mobius_domain::{InboxItem, InboxKind, Live};

use crate::Engine;

pub(crate) async fn add(
    engine: &Engine,
    kind: InboxKind,
    repository: &str,
    workstream: i64,
    issue: i64,
    text: &str,
    link: &str,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let item = engine
        .store
        .inbox_items()
        .add(kind, repository, workstream, issue, text, link)
        .await?;
    engine.broadcast(Live::Inbox(item));
    Ok(())
}

pub async fn list(engine: &Engine) -> Result<Vec<InboxItem>, Box<dyn Error + Send + Sync>> {
    engine.store.inbox_items().open().await
}

pub async fn dismiss(engine: &Engine, id: i64) -> Result<(), Box<dyn Error + Send + Sync>> {
    let item = engine.store.inbox_items().dismiss(id).await?;
    engine.broadcast(Live::Inbox(item));
    Ok(())
}
