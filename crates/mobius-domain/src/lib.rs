use serde::{Deserialize, Serialize};
use time::OffsetDateTime;

// The queue reason of a session that waits for the end of a pause of its Harness starts with this text.
pub const PAUSED: &str = "paused until ";

pub fn organization(repository: &str) -> &str {
    repository.split('/').next().unwrap_or_default()
}

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
    // A direct sub-issue of the Workstream has depth 0, and each deeper level adds one.
    pub depth: i64,
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
    Researcher,
    Triager,
    Mobius,
}

impl Author {
    pub const ALL: [Author; 6] = [
        Author::Owner,
        Author::Lead,
        Author::TellOwner,
        Author::Researcher,
        Author::Triager,
        Author::Mobius,
    ];

    pub fn name(self) -> &'static str {
        match self {
            Author::Owner => "Owner",
            Author::Lead => "Lead",
            Author::TellOwner => "tell_owner",
            Author::Researcher => "Researcher",
            Author::Triager => "Triager",
            Author::Mobius => "Mobius",
        }
    }
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct ChatMessage {
    pub id: i64,
    pub organization: String,
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
    pub organization: String,
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
    pub organization: String,
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
    UsageLimit,
    LeadFailed,
    Stopped,
}

impl InboxKind {
    pub const ALL: [InboxKind; 7] = [
        InboxKind::Question,
        InboxKind::Lead,
        InboxKind::ReadyForReview,
        InboxKind::StalePullRequest,
        InboxKind::UsageLimit,
        InboxKind::LeadFailed,
        InboxKind::Stopped,
    ];

    pub fn name(self) -> &'static str {
        match self {
            InboxKind::Question => "question",
            InboxKind::Lead => "Lead",
            InboxKind::ReadyForReview => "ready for review",
            InboxKind::StalePullRequest => "stale pull request",
            InboxKind::UsageLimit => "usage limit",
            InboxKind::LeadFailed => "Lead failed",
            InboxKind::Stopped => "stopped",
        }
    }
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct InboxItem {
    pub id: i64,
    pub kind: InboxKind,
    pub organization: String,
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
        organization: String,
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
    // The Triager chat created this Workstream.
    WorkstreamCreated {
        repository: String,
        number: i64,
    },
}
