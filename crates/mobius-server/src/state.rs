use crate::events::EventBus;
use mobius_core::DomainEvent;
use mobius_ingest::ManualSourceHandle;
use mobius_orchestrator::{Dispatcher, ResearchService, SessionManager};
use mobius_store::SqliteStore;
use std::sync::Arc;

#[derive(Clone)]
pub struct AppState {
    pub store: Arc<SqliteStore>,
    pub events: Arc<EventBus>,
    pub sessions: Arc<SessionManager<SqliteStore>>,
    pub dispatcher: Arc<Dispatcher<SqliteStore>>,
    pub research: Arc<ResearchService<SqliteStore>>,
    pub manual: ManualSourceHandle,
    /// Flips to `true` when graceful shutdown begins; long-lived streams
    /// (SSE) watch it so `axum::serve` can actually return.
    pub shutdown: tokio::sync::watch::Receiver<bool>,
}

/// `emit` closure shared by dispatcher/session-manager/registry: publishes
/// every domain event onto the bus.
pub fn emit_for(events: Arc<EventBus>) -> Arc<dyn Fn(DomainEvent) + Send + Sync> {
    Arc::new(move |e| {
        events.publish(e);
    })
}
