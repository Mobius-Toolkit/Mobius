use mobius_core::{StoreError, TaskStatus};
use mobius_harness::HarnessError;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum OrchestratorError {
    #[error("store error: {0}")]
    Store(#[from] StoreError),
    #[error("harness error: {0}")]
    Harness(#[from] HarnessError),
    #[error("not found: {0}")]
    NotFound(String),
    #[error("invalid task transition {from} -> {to}")]
    InvalidTransition { from: TaskStatus, to: TaskStatus },
    #[error("session not live: {0}")]
    NotLive(String),
    /// Missing or inconsistent configuration / entity wiring — the API
    /// maps this to 422 Unprocessable Entity.
    #[error("configuration error: {0}")]
    Config(String),
    /// A `run_turn` turn failed (prompt error, session exited, …).
    #[error("turn failed: {0}")]
    Turn(String),
    #[error("io error: {0}")]
    Io(#[from] std::io::Error),
}
