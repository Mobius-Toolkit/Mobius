//! Orchestration: profile resolution, signal→task dispatch, run execution,
//! prompt building, worktree management, and human-chat session management.

pub mod dispatcher;
pub mod error;
pub mod prompt;
pub mod resolver;
pub mod session_manager;
pub mod worktree;

pub use dispatcher::{Dispatcher, EmitFn, route_signal};
pub use error::OrchestratorError;
pub use prompt::PromptBuilder;
pub use resolver::ProfileResolver;
pub use session_manager::SessionManager;
pub use worktree::WorktreeManager;
