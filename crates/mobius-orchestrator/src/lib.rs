//! Orchestration: coordinator provisioning, prompt building, profile
//! resolution, signal→task dispatch, task runs as conversations, research
//! sessions, worktree management, and chat session management.

pub mod agents;
pub mod dispatcher;
pub mod error;
pub mod prompt;
pub mod research;
pub mod resolver;
pub mod session_manager;
pub mod worktree;

pub use agents::AgentProvisioner;
pub use dispatcher::{CreateTaskSpec, Dispatcher, EmitFn, route_signal};
pub use error::OrchestratorError;
pub use prompt::{
    ChatContext, MemoryGroup, ResearchContext, RunContext, chat_preamble, updates_block,
};
pub use research::{ResearchService, StartResearch};
pub use resolver::ProfileResolver;
pub use session_manager::{OpenConversation, SessionManager};
pub use worktree::WorktreeManager;
