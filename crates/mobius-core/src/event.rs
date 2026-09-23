use crate::id::*;
use crate::model::*;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use strum::{Display, EnumString};

/// Kind of entity a [`DomainEvent::EntityChanged`] refers to. Emitted by every
/// CRUD mutation so API clients (the UI) can refresh lists.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Display, EnumString)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum EntityKind {
    Organization,
    Repository,
    Project,
    Agent,
    ModelProfile,
    Harness,
    Task,
    Run,
    Signal,
    MemoryEntry,
    Conversation,
    Message,
    PermissionRequest,
    Research,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "type", content = "data", rename_all = "snake_case")]
pub enum DomainEvent {
    SignalIngested {
        signal: Signal,
    },
    TaskCreated {
        task: Task,
    },
    TaskStatusChanged {
        task_id: TaskId,
        from: TaskStatus,
        to: TaskStatus,
    },
    RunStarted {
        run: Run,
    },
    RunUpdated {
        run_id: RunId,
        status: RunStatus,
        message: Option<String>,
    },
    RunFinished {
        run: Run,
    },
    MemoryWritten {
        entry: MemoryEntry,
    },
    /// A prompt turn completed and its agent message is final. `run_turn`
    /// awaits this; humans see it as the end of streaming.
    TurnFinished {
        conversation_id: ConversationId,
        human_message_id: Option<MessageId>,
        agent_message_id: MessageId,
    },
    ResearchStarted {
        research: Research,
    },
    ResearchFinished {
        research: Research,
    },
    AgentStatusChanged {
        agent_id: AgentId,
        status: AgentStatus,
    },
    ConversationCreated {
        conversation: Conversation,
    },
    ConversationStatusChanged {
        conversation_id: ConversationId,
        status: ConversationStatus,
    },
    ConversationConfigChanged {
        conversation_id: ConversationId,
        config_options: Vec<SessionConfigOption>,
    },
    MessageAppended {
        conversation_id: ConversationId,
        message: Message,
    },
    /// A block completed mid-turn; the in-flight message snapshot so clients
    /// can render tool cards/thoughts without waiting for `MessageAppended`.
    MessageUpdated {
        conversation_id: ConversationId,
        message: Message,
    },
    /// Incremental text for a message currently being assembled.
    MessageDelta {
        conversation_id: ConversationId,
        message_id: MessageId,
        block_index: usize,
        text: String,
    },
    PermissionRequested {
        request: PermissionRequest,
    },
    PermissionResolved {
        permission_request_id: PermissionRequestId,
        option_id: String,
    },
    EntityChanged {
        kind: EntityKind,
        id: String,
    },
}

/// Server-assigned envelope giving every event a monotonic id for SSE
/// `Last-Event-ID` replay.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct EventEnvelope {
    pub id: u64,
    pub at: DateTime<Utc>,
    pub event: DomainEvent,
}
