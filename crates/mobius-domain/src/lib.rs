use serde::{Deserialize, Serialize};
use time::OffsetDateTime;

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Harness {
    ClaudeCode,
    Antigravity,
    Devin,
}

impl Harness {
    pub const ALL: [Harness; 3] = [Harness::ClaudeCode, Harness::Antigravity, Harness::Devin];

    pub fn name(self) -> &'static str {
        match self {
            Harness::ClaudeCode => "claude-code",
            Harness::Antigravity => "antigravity",
            Harness::Devin => "devin",
        }
    }
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct DeviceLogin {
    pub id: i64,
    pub user_agent: String,
    pub created_at: OffsetDateTime,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Devices {
    pub this_device: i64,
    pub logins: Vec<DeviceLogin>,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct ManifestForm {
    pub url: String,
    pub manifest: String,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Workstream {
    pub repository: String,
    pub number: i64,
    pub title: String,
    pub autopilot: bool,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct TaskLine {
    pub number: i64,
    pub title: String,
    pub state: String,
    pub url: String,
    // It holds only the open blockers.
    pub blocked_by: Vec<Blocker>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Blocker {
    pub number: i64,
    // It holds a title only when the blocker is in another Workstream.
    pub workstream_title: Option<String>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct FeedRow {
    pub id: i64,
    pub time: OffsetDateTime,
    pub repository: String,
    pub workstream: i64,
    pub issue: i64,
    pub actor: String,
    pub text: String,
    pub link: String,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub enum Author {
    Owner,
    Lead,
    TellOwner,
}

impl Author {
    pub const ALL: [Author; 3] = [Author::Owner, Author::Lead, Author::TellOwner];

    pub fn name(self) -> &'static str {
        match self {
            Author::Owner => "Owner",
            Author::Lead => "Lead",
            Author::TellOwner => "tell_owner",
        }
    }
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct ChatMessage {
    pub id: i64,
    pub repository: String,
    pub workstream: i64,
    pub author: Author,
    pub time: OffsetDateTime,
    pub text: String,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct ChatView {
    pub messages: Vec<ChatMessage>,
    pub writing: bool,
    pub lead: Harness,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Unread {
    pub repository: String,
    pub workstream: i64,
    pub count: i64,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Session {
    pub id: i64,
    pub role: String,
    pub harness: Harness,
    pub model: String,
    pub repository: String,
    pub workstream: i64,
    pub acp_session_id: Option<String>,
    pub started_at: OffsetDateTime,
    pub ended_at: Option<OffsetDateTime>,
    pub end_reason: Option<String>,
    // The reason why the session waits for a Worker slot.
    pub queue_reason: Option<String>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct AgentNode {
    pub session: Session,
    pub role: String,
    pub title: String,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct TranscriptRow {
    pub id: i64,
    pub session: i64,
    pub time: OffsetDateTime,
    pub kind: String,
    pub json: String,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct TranscriptLine {
    pub id: i64,
    pub time: OffsetDateTime,
    pub kind: String,
    pub text: String,
    pub harness_tool_name: Option<String>,
    pub body: Option<String>,
    pub folded: bool,
    pub error: bool,
    pub raw: String,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub enum InboxKind {
    Question,
    Lead,
    ReadyForReview,
    StalePullRequest,
}

impl InboxKind {
    pub const ALL: [InboxKind; 4] = [
        InboxKind::Question,
        InboxKind::Lead,
        InboxKind::ReadyForReview,
        InboxKind::StalePullRequest,
    ];

    pub fn name(self) -> &'static str {
        match self {
            InboxKind::Question => "question",
            InboxKind::Lead => "Lead",
            InboxKind::ReadyForReview => "ready for review",
            InboxKind::StalePullRequest => "stale pull request",
        }
    }
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct InboxItem {
    pub id: i64,
    pub kind: InboxKind,
    pub repository: String,
    pub workstream: i64,
    pub issue: i64,
    pub text: String,
    pub link: String,
    pub time: OffsetDateTime,
    pub dismissed_at: Option<OffsetDateTime>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub enum Live {
    Feed(FeedRow),
    Message(ChatMessage),
    Lead {
        repository: String,
        workstream: i64,
        writing: bool,
        error: Option<String>,
    },
    Unread(Unread),
    Agent(AgentNode),
    Inbox(InboxItem),
    // The Workstream list changed, for example its Autopilot.
    Workstreams,
}
