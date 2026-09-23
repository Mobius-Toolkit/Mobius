use crate::id::*;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::path::PathBuf;
use strum::{Display, EnumString};

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Organization {
    pub id: OrganizationId,
    pub name: String,
    pub slug: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Display, EnumString)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum RepoProvider {
    GitHub,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Repository {
    pub id: RepositoryId,
    pub organization_id: OrganizationId,
    pub owner: String,
    pub name: String,
    pub provider: RepoProvider,
    pub default_branch: String,
    pub local_path: Option<PathBuf>,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct ProjectScope {
    #[serde(default)]
    pub paths: Vec<String>,
    #[serde(default)]
    pub labels: Vec<String>,
    #[serde(default)]
    pub keywords: Vec<String>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Display, EnumString)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum ProjectStatus {
    Active,
    Paused,
    Archived,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Project {
    pub id: ProjectId,
    pub repository_id: RepositoryId,
    pub name: String,
    pub slug: String,
    pub description: String,
    pub scope: ProjectScope,
    pub status: ProjectStatus,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Display, EnumString)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum PermissionPolicy {
    ReadOnly,
    WorkspaceEdits,
    Auto,
    AskHuman,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Harness {
    pub id: HarnessId,
    pub name: String,
    pub command: String,
    #[serde(default)]
    pub args: Vec<String>,
    #[serde(default)]
    pub env: BTreeMap<String, String>,
    pub default_permission_policy: PermissionPolicy,
    /// Extra argv template appended at spawn when a profile sets a model,
    /// e.g. `["--model", "{model}"]`.
    #[serde(default)]
    pub model_arg_template: Vec<String>,
    pub enabled: bool,
    pub created_at: DateTime<Utc>,
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, Display, EnumString,
)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum Effort {
    Low,
    Medium,
    High,
    Max,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ModelProfile {
    pub id: ModelProfileId,
    pub name: String,
    pub harness_id: HarnessId,
    pub model: Option<String>,
    pub effort: Option<Effort>,
    /// Raw ACP `session/set_config_option` overrides applied verbatim
    /// (`configId` -> `value`).
    #[serde(default)]
    pub config: BTreeMap<String, String>,
    pub created_at: DateTime<Utc>,
}

#[derive(
    Debug,
    Clone,
    Copy,
    PartialEq,
    Eq,
    PartialOrd,
    Ord,
    Hash,
    Serialize,
    Deserialize,
    Display,
    EnumString,
)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum Activity {
    Plan,
    Implement,
    Research,
    Review,
    Triage,
    Housekeeping,
    Chat,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Display, EnumString)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum TaskKind {
    Implement,
    Research,
    Review,
    Housekeeping,
    Triage,
}

impl From<TaskKind> for Activity {
    fn from(kind: TaskKind) -> Self {
        match kind {
            TaskKind::Implement => Activity::Implement,
            TaskKind::Research => Activity::Research,
            TaskKind::Review => Activity::Review,
            TaskKind::Housekeeping => Activity::Housekeeping,
            TaskKind::Triage => Activity::Triage,
        }
    }
}

/// Which model profile an agent uses per activity. The default applies to any
/// activity without an explicit override.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ActivityProfiles {
    pub default: ModelProfileId,
    #[serde(default)]
    pub overrides: BTreeMap<Activity, ModelProfileId>,
}

impl ActivityProfiles {
    pub fn profile_for(&self, activity: Activity) -> ModelProfileId {
        self.overrides
            .get(&activity)
            .copied()
            .unwrap_or(self.default)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Display, EnumString)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum AgentRole {
    Project,
    Researcher,
    Reviewer,
    Housekeeper,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Display, EnumString)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum AgentStatus {
    Idle,
    Busy,
    Disabled,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Agent {
    pub id: AgentId,
    pub name: String,
    pub role: AgentRole,
    /// Model profile selection per activity; the harness is derived from the
    /// resolved profile's `harness_id`.
    pub profiles: ActivityProfiles,
    /// `Some` for project agents; `None` for repo/org-level role agents.
    pub project_id: Option<ProjectId>,
    pub repository_id: Option<RepositoryId>,
    /// System-prompt template prepended to task/chat prompts.
    pub instructions: String,
    pub permission_policy: PermissionPolicy,
    pub status: AgentStatus,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Display, EnumString)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum TaskStatus {
    Proposed,
    Approved,
    Queued,
    Running,
    NeedsReview,
    Done,
    Failed,
    Cancelled,
}

impl TaskStatus {
    pub fn can_transition_to(&self, next: &TaskStatus) -> bool {
        use TaskStatus::*;
        matches!(
            (self, next),
            (Proposed, Approved)
                | (Proposed, Cancelled)
                | (Approved, Queued)
                | (Approved, Cancelled)
                | (Queued, Running)
                | (Queued, Cancelled)
                | (Running, NeedsReview)
                | (Running, Failed)
                | (Running, Cancelled)
                | (NeedsReview, Done)
                | (NeedsReview, Queued)
                | (NeedsReview, Failed)
                | (NeedsReview, Cancelled)
                | (Failed, Queued)
        )
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Display, EnumString)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum Priority {
    Low,
    Normal,
    High,
    Urgent,
}

#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct TaskOrigin {
    #[serde(default)]
    pub signal_id: Option<SignalId>,
    #[serde(default)]
    pub github_issue: Option<u64>,
    #[serde(default)]
    pub github_pr: Option<u64>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Task {
    pub id: TaskId,
    pub project_id: ProjectId,
    /// Owning agent (the project's project agent for signal-routed tasks).
    pub agent_id: AgentId,
    #[serde(default)]
    pub parent_task_id: Option<TaskId>,
    pub title: String,
    pub description: String,
    pub kind: TaskKind,
    pub status: TaskStatus,
    #[serde(default)]
    pub origin: TaskOrigin,
    pub priority: Priority,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Display, EnumString)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum RunStatus {
    Pending,
    Starting,
    Running,
    AwaitingPermission,
    Succeeded,
    Failed,
    Cancelled,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Run {
    pub id: RunId,
    pub task_id: TaskId,
    pub agent_id: AgentId,
    pub activity: Activity,
    pub model_profile_id: ModelProfileId,
    pub harness_id: HarnessId,
    #[serde(default)]
    pub worktree_path: Option<PathBuf>,
    #[serde(default)]
    pub branch: Option<String>,
    #[serde(default)]
    pub acp_session_id: Option<String>,
    pub status: RunStatus,
    #[serde(default)]
    pub summary: Option<String>,
    pub started_at: DateTime<Utc>,
    #[serde(default)]
    pub finished_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Display, EnumString)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum SourceKind {
    GitHub,
    Manual,
    Sentry,
    Chat,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "type", content = "value", rename_all = "snake_case")]
pub enum SignalKind {
    IssueOpened,
    IssueCommented,
    IssueLabeled,
    PullRequestOpened,
    PullRequestReviewRequested,
    PullRequestCommented,
    HumanRequest,
    Custom(String),
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Signal {
    pub id: SignalId,
    pub source: SourceKind,
    pub kind: SignalKind,
    #[serde(default)]
    pub repository_id: Option<RepositoryId>,
    #[serde(default)]
    pub project_id: Option<ProjectId>,
    /// Idempotency key, e.g. `github:{owner}/{repo}:issue:{n}:comment:{id}`.
    pub dedupe_key: String,
    pub title: String,
    pub body: String,
    #[serde(default)]
    pub payload: serde_json::Value,
    pub occurred_at: DateTime<Utc>,
    pub ingested_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "level", content = "id", rename_all = "snake_case")]
pub enum MemoryScope {
    Organization(OrganizationId),
    Repository(RepositoryId),
    Project(ProjectId),
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, Display, EnumString,
)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum MemoryKind {
    Fact,
    Decision,
    Convention,
    Gotcha,
    Summary,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct MemoryEntry {
    pub id: MemoryEntryId,
    pub scope: MemoryScope,
    pub kind: MemoryKind,
    pub content: String,
    #[serde(default)]
    pub source_run_id: Option<RunId>,
    #[serde(default)]
    pub superseded_by: Option<MemoryEntryId>,
    pub created_at: DateTime<Utc>,
}

/// Mirror of the ACP `configOptions[]` entry shape (wasm-safe, decoupled from
/// the protocol crate so it can live in persisted rows and wire DTOs).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SessionConfigSelectOption {
    pub value: String,
    pub name: String,
    #[serde(default)]
    pub description: Option<String>,
}

/// Mirror of one ACP session config option as advertised by `session/new`
/// and returned by `session/set_config_option`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SessionConfigOption {
    pub id: String,
    #[serde(default)]
    pub category: Option<String>,
    pub name: String,
    #[serde(default)]
    pub description: Option<String>,
    #[serde(rename = "type", default)]
    pub kind: Option<String>,
    #[serde(default)]
    pub current_value: Option<String>,
    #[serde(default)]
    pub options: Vec<SessionConfigSelectOption>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Display, EnumString)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum ConversationStatus {
    Idle,
    Streaming,
    AwaitingPermission,
    Closed,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Conversation {
    pub id: ConversationId,
    pub agent_id: AgentId,
    pub activity: Activity,
    pub model_profile_id: ModelProfileId,
    #[serde(default)]
    pub repository_id: Option<RepositoryId>,
    pub workdir: PathBuf,
    pub title: String,
    #[serde(default)]
    pub acp_session_id: Option<String>,
    pub status: ConversationStatus,
    #[serde(default)]
    pub config_options: Vec<SessionConfigOption>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Display, EnumString)]
#[serde(rename_all = "snake_case")]
#[strum(serialize_all = "snake_case")]
pub enum MessageAuthor {
    Human,
    Agent,
    System,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum ContentBlock {
    Text {
        text: String,
    },
    Thought {
        text: String,
    },
    ToolCall {
        id: String,
        title: String,
        tool_kind: Option<String>,
        status: Option<String>,
        #[serde(default)]
        raw: serde_json::Value,
    },
    Plan {
        #[serde(default)]
        raw: serde_json::Value,
    },
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Message {
    pub id: MessageId,
    pub conversation_id: ConversationId,
    pub author: MessageAuthor,
    #[serde(default)]
    pub blocks: Vec<ContentBlock>,
    pub created_at: DateTime<Utc>,
}

/// Summary of an ACP tool call embedded in permission requests.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ToolCallInfo {
    pub tool_call_id: String,
    #[serde(default)]
    pub title: Option<String>,
    #[serde(default)]
    pub kind: Option<String>,
    #[serde(default)]
    pub raw: serde_json::Value,
}

/// Mirror of an ACP permission option (`optionId`, `name`, `kind`).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PermissionOptionInfo {
    pub option_id: String,
    pub name: String,
    pub kind: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "state", rename_all = "snake_case")]
pub enum PermissionRequestStatus {
    Pending,
    Resolved { option_id: String },
    Expired,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct PermissionRequest {
    pub id: PermissionRequestId,
    #[serde(default)]
    pub conversation_id: Option<ConversationId>,
    #[serde(default)]
    pub run_id: Option<RunId>,
    pub tool_call: ToolCallInfo,
    #[serde(default)]
    pub options: Vec<PermissionOptionInfo>,
    pub status: PermissionRequestStatus,
    pub created_at: DateTime<Utc>,
    #[serde(default)]
    pub resolved_at: Option<DateTime<Utc>>,
}
