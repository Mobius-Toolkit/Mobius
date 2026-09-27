use std::collections::VecDeque;
use std::error::Error;

use mobius_domain::FeedRow;
use tokio::sync::broadcast::Receiver;

use crate::Engine;

const BACKLOG_SIZE: i64 = 100;

pub struct Feed {
    backlog: VecDeque<FeedRow>,
    updates: Receiver<FeedRow>,
    last_id: i64,
}

impl Feed {
    // Gives `None` when the live channel drops rows that this feed did not read. The client then opens a new feed after its last row.
    pub async fn next(&mut self) -> Option<FeedRow> {
        if let Some(row) = self.backlog.pop_front() {
            self.last_id = row.id;
            return Some(row);
        }
        loop {
            let row = self.updates.recv().await.ok()?;
            if row.id > self.last_id {
                self.last_id = row.id;
                return Some(row);
            }
        }
    }
}

pub async fn feed(
    engine: &Engine,
    after: Option<i64>,
) -> Result<Feed, Box<dyn Error + Send + Sync>> {
    let updates = engine.live.subscribe();
    let backlog = match after {
        Some(id) => engine.store.events().after(id).await?,
        None => engine.store.events().latest(BACKLOG_SIZE).await?,
    };
    Ok(Feed {
        backlog: backlog.into(),
        updates,
        last_id: after.unwrap_or(0),
    })
}

pub(crate) async fn add(
    engine: &Engine,
    repository: &str,
    workstream: i64,
    issue: i64,
    actor: &str,
    text: &str,
    link: &str,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let row = engine
        .store
        .events()
        .add(repository, workstream, issue, actor, text, link)
        .await?;
    // With no open feed, the channel has no receiver and the send fails.
    let _ = engine.live.send(row);
    Ok(())
}
