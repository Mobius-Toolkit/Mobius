use serde::{Deserialize, Serialize};
use time::OffsetDateTime;

// The queue reason of a session that waits for the end of a pause of its Harness starts with this text.
pub const PAUSED: &str = "paused until ";

// The release tag of this binary, set by the release workflow. A local build has no release tag.
pub const RELEASE_VERSION: Option<&'static str> = option_env!("MOBIUS_VERSION");

fn release_tag(tag: &str) -> Option<(u64, u64, u64)> {
    let mut parts = tag.strip_prefix('v')?.split('.');
    let version = (
        parts.next()?.parse().ok()?,
        parts.next()?.parse().ok()?,
        parts.next()?.parse().ok()?,
    );
    parts.next().is_none().then_some(version)
}

pub fn newer_release(current: &str, latest: &str) -> bool {
    match (release_tag(current), release_tag(latest)) {
        (Some(current), Some(latest)) => latest > current,
        _ => false,
    }
}

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
    pub body: String,
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
    // The reason why the session waits for a slot.
    pub queue_reason: Option<String>,
    // The one issue the session works on. `None` for the chats and the Researcher.
    pub issue: Option<i64>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct AgentNode {
    pub session: Session,
    pub role: String,
    pub title: String,
}

// The open sessions of one role on the "Agents" page, with the role limit.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct AgentGroup {
    pub name: String,
    // The sessions that hold a slot. A queued session shows in `agents` but does not count.
    pub count: u32,
    pub max: u32,
    pub agents: Vec<AgentNode>,
}

// The "Agents" page: the global count and one group for each role.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct ActiveAgents {
    // The sessions that hold a slot and whose role counts toward `max_agents`.
    pub count: u32,
    pub max: u32,
    pub groups: Vec<AgentGroup>,
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_newer_release_tag_counts_each_number() {
        assert!(newer_release("v0.1.57", "v0.2.0"));
        assert!(newer_release("v1.9.9", "v2.0.0"));
        // Numbers, not text: "10" sorts before "9" as text.
        assert!(newer_release("v0.1.9", "v0.1.10"));
    }

    #[test]
    fn an_equal_or_older_release_tag_is_not_newer() {
        assert!(!newer_release("v0.1.57", "v0.1.57"));
        assert!(!newer_release("v0.1.10", "v0.1.9"));
        assert!(!newer_release("v2.0.0", "v1.9.9"));
    }

    #[test]
    fn a_tag_that_does_not_parse_is_not_newer() {
        assert!(!newer_release("v0.1.57", "latest"));
        assert!(!newer_release("v0.1.57", "v0.1"));
        assert!(!newer_release("local", "v0.2.0"));
    }
}
