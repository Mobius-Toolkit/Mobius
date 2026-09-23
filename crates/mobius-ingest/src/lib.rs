//! Signal ingestion: the `SignalSource` contract, a polling `SourceRegistry`
//! with dedupe, and the in-process `ManualSource` used by the REST API.

use mobius_core::{DomainEvent, Signal, SignalId, SignalKind, SignalRepo, SourceKind, StoreError};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::future::Future;
use std::pin::Pin;
use thiserror::Error;
use tokio::sync::mpsc;

/// Futures are boxed here (not `impl Future`) so `SignalSource` stays
/// dyn-compatible for the `Box<dyn SignalSource>` registry.
pub type PollFuture<'a> =
    Pin<Box<dyn Future<Output = Result<(Vec<Signal>, SourceCursor), IngestError>> + Send + 'a>>;

#[derive(Debug, Error)]
pub enum IngestError {
    #[error("store error: {0}")]
    Store(#[from] StoreError),
    #[error("source error: {0}")]
    Source(String),
}

/// Opaque per-source checkpoint persisted by the caller between polls.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(transparent)]
pub struct SourceCursor(pub serde_json::Value);

/// One source of inbound signals. Fully abstracted: sources poll (or drain a
/// channel) and return new signals plus the next cursor.
pub trait SignalSource: Send + Sync {
    fn name(&self) -> &str;
    fn poll(&mut self, cursor: Option<SourceCursor>) -> PollFuture<'_>;
}

/// A human-entered signal pushed through `POST /api/v1/signals/manual`.
#[derive(Debug, Clone)]
pub struct ManualSignal {
    pub title: String,
    pub body: String,
    pub repository_id: Option<mobius_core::RepositoryId>,
}

/// In-process source backed by an mpsc channel.
pub struct ManualSource {
    rx: mpsc::Receiver<ManualSignal>,
}

/// Push half of the [`ManualSource`] channel, held by the server.
#[derive(Clone)]
pub struct ManualSourceHandle {
    tx: mpsc::Sender<ManualSignal>,
}

impl ManualSourceHandle {
    pub async fn push(&self, signal: ManualSignal) -> Result<(), IngestError> {
        self.tx
            .send(signal)
            .await
            .map_err(|e| IngestError::Source(e.to_string()))
    }
}

pub fn manual_source() -> (ManualSource, ManualSourceHandle) {
    let (tx, rx) = mpsc::channel(256);
    (ManualSource { rx }, ManualSourceHandle { tx })
}

impl SignalSource for ManualSource {
    fn name(&self) -> &str {
        "manual"
    }

    fn poll(&mut self, _cursor: Option<SourceCursor>) -> PollFuture<'_> {
        Box::pin(async move {
            let mut out = Vec::new();
            while let Ok(input) = self.rx.try_recv() {
                let now = chrono::Utc::now();
                out.push(Signal {
                    id: SignalId::new(),
                    source: SourceKind::Manual,
                    kind: SignalKind::HumanRequest,
                    repository_id: input.repository_id,
                    project_id: None,
                    dedupe_key: format!("manual:{}", uuid::Uuid::new_v4()),
                    title: input.title,
                    body: input.body,
                    payload: serde_json::Value::Null,
                    occurred_at: now,
                    ingested_at: now,
                });
            }
            Ok((out, SourceCursor(serde_json::Value::Null)))
        })
    }
}

/// Insert signals that aren't already known (dedupe_key) and emit
/// `SignalIngested` + `EntityChanged` for each. Returns the count inserted.
pub async fn ingest_signals<S, F>(
    store: &S,
    signals: Vec<Signal>,
    emit: &F,
) -> Result<usize, IngestError>
where
    S: SignalRepo,
    F: Fn(DomainEvent),
{
    let mut count = 0;
    for signal in signals {
        if store
            .find_signal_by_dedupe_key(&signal.dedupe_key)
            .await?
            .is_some()
        {
            continue;
        }
        store.insert_signal(&signal).await?;
        emit(DomainEvent::SignalIngested {
            signal: signal.clone(),
        });
        emit(DomainEvent::EntityChanged {
            kind: mobius_core::EntityKind::Signal,
            id: signal.id.to_string(),
        });
        count += 1;
    }
    Ok(count)
}

/// Holds boxed sources and drives `poll → dedupe → insert → emit`.
pub struct SourceRegistry {
    sources: Vec<Box<dyn SignalSource>>,
    cursors: HashMap<String, SourceCursor>,
}

impl Default for SourceRegistry {
    fn default() -> Self {
        Self::new()
    }
}

impl SourceRegistry {
    pub fn new() -> Self {
        Self {
            sources: Vec::new(),
            cursors: HashMap::new(),
        }
    }

    pub fn add(&mut self, source: Box<dyn SignalSource>) {
        self.sources.push(source);
    }

    /// Poll every source once, inserting new signals (deduped by
    /// `dedupe_key`) and invoking `emit` with `SignalIngested` for each.
    /// Returns the number of new signals.
    pub async fn poll_once<S, F>(&mut self, store: &S, emit: &F) -> Result<usize, IngestError>
    where
        S: SignalRepo,
        F: Fn(DomainEvent),
    {
        let mut count = 0;
        for source in &mut self.sources {
            let cursor = self.cursors.get(source.name()).cloned();
            match source.poll(cursor).await {
                Ok((signals, next)) => {
                    self.cursors.insert(source.name().to_string(), next);
                    count += ingest_signals(store, signals, emit).await?;
                }
                Err(e) => {
                    tracing::warn!(source = source.name(), error = %e, "poll failed");
                }
            }
        }
        Ok(count)
    }

    /// Poll forever at `interval`. Designed to be `tokio::spawn`ed.
    pub async fn run_polling_loop<S, F>(mut self, interval: std::time::Duration, store: S, emit: F)
    where
        S: SignalRepo,
        F: Fn(DomainEvent),
    {
        loop {
            if let Err(e) = self.poll_once(&store, &emit).await {
                tracing::warn!(error = %e, "poll cycle failed");
            }
            tokio::time::sleep(interval).await;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use mobius_core::InMemoryStore;
    use std::sync::atomic::{AtomicUsize, Ordering};

    #[tokio::test]
    async fn manual_source_roundtrip_and_dedupe() {
        let (mut source, handle) = manual_source();
        handle
            .push(ManualSignal {
                title: "hello".into(),
                body: "world".into(),
                repository_id: None,
            })
            .await
            .expect("push");
        let (signals, _) = source.poll(None).await.expect("poll");
        assert_eq!(signals.len(), 1);
        assert_eq!(signals[0].title, "hello");
        // drained
        let (signals, _) = source.poll(None).await.expect("poll2");
        assert!(signals.is_empty());
    }

    #[tokio::test]
    async fn registry_dedupes() {
        let store = InMemoryStore::new();
        let (source, handle) = manual_source();
        let mut registry = SourceRegistry::new();
        registry.add(Box::new(source));
        let count = AtomicUsize::new(0);
        let emit = |_: DomainEvent| {
            count.fetch_add(1, Ordering::SeqCst);
        };

        handle
            .push(ManualSignal {
                title: "t".into(),
                body: "b".into(),
                repository_id: None,
            })
            .await
            .expect("push");
        assert_eq!(registry.poll_once(&store, &emit).await.expect("poll"), 1);
        // Manual dedupe keys are unique per message; re-poll yields nothing.
        assert_eq!(registry.poll_once(&store, &emit).await.expect("poll2"), 0);
        assert_eq!(store.list_signals().await.expect("list").len(), 1);
    }
}
