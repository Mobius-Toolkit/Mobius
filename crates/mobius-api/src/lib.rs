//! Wire DTOs shared by `mobius-server` and `mobius-ui`. Create/Update request
//! bodies live here; entities themselves live in `mobius-core`.

use mobius_core::*;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

/// Validation error body for HTTP 422 responses.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ApiError {
    pub message: String,
    #[serde(default)]
    pub fields: BTreeMap<String, String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UpsertOrganization {
    pub name: String,
    pub slug: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreateRepository {
    pub organization_id: OrganizationId,
    pub owner: String,
    pub name: String,
    #[serde(default)]
    pub provider: Option<RepoProvider>,
    #[serde(default)]
    pub default_branch: Option<String>,
    #[serde(default)]
    pub local_path: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UpdateRepository {
    pub owner: String,
    pub name: String,
    pub provider: RepoProvider,
    pub default_branch: String,
    #[serde(default)]
    pub local_path: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreateProject {
    #[serde(default)]
    pub organization_id: Option<OrganizationId>,
    /// Routing hint only — a project may span zero or more repositories.
    #[serde(default)]
    pub repository_ids: Vec<RepositoryId>,
    pub name: String,
    pub slug: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub scope: ProjectScope,
    #[serde(default)]
    pub status: Option<ProjectStatus>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UpdateProject {
    pub name: String,
    pub slug: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub scope: ProjectScope,
    pub status: ProjectStatus,
    /// Routing hint only — replaces the project's repository hints.
    #[serde(default)]
    pub repository_ids: Vec<RepositoryId>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreateHarness {
    pub name: String,
    pub command: String,
    #[serde(default)]
    pub args: Vec<String>,
    #[serde(default)]
    pub env: BTreeMap<String, String>,
    #[serde(default)]
    pub default_permission_policy: Option<PermissionPolicy>,
    #[serde(default)]
    pub model_arg_template: Vec<String>,
    #[serde(default)]
    pub enabled: Option<bool>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UpdateHarness {
    pub name: String,
    pub command: String,
    #[serde(default)]
    pub args: Vec<String>,
    #[serde(default)]
    pub env: BTreeMap<String, String>,
    pub default_permission_policy: PermissionPolicy,
    #[serde(default)]
    pub model_arg_template: Vec<String>,
    pub enabled: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreateModelProfile {
    pub name: String,
    pub harness_id: HarnessId,
    #[serde(default)]
    pub model: Option<String>,
    #[serde(default)]
    pub effort: Option<Effort>,
    #[serde(default)]
    pub config: BTreeMap<String, String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UpdateModelProfile {
    pub name: String,
    pub harness_id: HarnessId,
    #[serde(default)]
    pub model: Option<String>,
    #[serde(default)]
    pub effort: Option<Effort>,
    #[serde(default)]
    pub config: BTreeMap<String, String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreateAgent {
    pub name: String,
    pub role: AgentRole,
    /// Required for non-organization agents; organization agents may derive
    /// it from `project_id` or the single organization.
    #[serde(default)]
    pub organization_id: Option<OrganizationId>,
    /// Default profile applied to activities without an override.
    pub default_profile: ModelProfileId,
    #[serde(default)]
    pub profile_overrides: BTreeMap<Activity, ModelProfileId>,
    #[serde(default)]
    pub project_id: Option<ProjectId>,
    #[serde(default)]
    pub repository_id: Option<RepositoryId>,
    #[serde(default)]
    pub instructions: String,
    #[serde(default)]
    pub permission_policy: Option<PermissionPolicy>,
    #[serde(default)]
    pub status: Option<AgentStatus>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UpdateAgent {
    pub name: String,
    pub role: AgentRole,
    pub organization_id: OrganizationId,
    pub default_profile: ModelProfileId,
    #[serde(default)]
    pub profile_overrides: BTreeMap<Activity, ModelProfileId>,
    #[serde(default)]
    pub project_id: Option<ProjectId>,
    #[serde(default)]
    pub repository_id: Option<RepositoryId>,
    #[serde(default)]
    pub instructions: String,
    pub permission_policy: PermissionPolicy,
    pub status: AgentStatus,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ManualSignal {
    pub title: String,
    #[serde(default)]
    pub body: String,
    #[serde(default)]
    pub repository_id: Option<RepositoryId>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreateConversation {
    /// Optional; resolved from the project or the single organization.
    #[serde(default)]
    pub organization_id: Option<OrganizationId>,
    #[serde(default)]
    pub project_id: Option<ProjectId>,
    #[serde(default)]
    pub title: Option<String>,
    #[serde(default)]
    pub model_profile_id: Option<ModelProfileId>,
}

/// Conversation with its persisted messages, returned by `GET /conversations/{id}`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ConversationView {
    #[serde(flatten)]
    pub conversation: Conversation,
    #[serde(default)]
    pub messages: Vec<Message>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PostMessage {
    pub text: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SetConversationConfig {
    pub config_id: String,
    pub value: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ResolvePermission {
    pub option_id: String,
}

// ------------------------------------------------------------------ Tasks

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreateTask {
    pub project_id: ProjectId,
    /// Repository the task executes against (required to *run*, optional to
    /// propose).
    #[serde(default)]
    pub repository_id: Option<RepositoryId>,
    pub title: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub kind: Option<TaskKind>,
    #[serde(default)]
    pub parent_task_id: Option<TaskId>,
    #[serde(default)]
    pub priority: Option<Priority>,
    /// Set when the task is created from inside a conversation (CLI).
    #[serde(default)]
    pub origin_conversation_id: Option<ConversationId>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UpdateTask {
    #[serde(default)]
    pub title: Option<String>,
    #[serde(default)]
    pub description: Option<String>,
    #[serde(default)]
    pub kind: Option<TaskKind>,
    #[serde(default)]
    pub repository_id: Option<RepositoryId>,
    #[serde(default)]
    pub priority: Option<Priority>,
    /// Only legal transitions are accepted (`can_transition_to`).
    #[serde(default)]
    pub status: Option<TaskStatus>,
}

/// `GET /tasks/{id}`: the task with its runs and child tasks.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TaskView {
    #[serde(flatten)]
    pub task: Task,
    #[serde(default)]
    pub runs: Vec<Run>,
    #[serde(default)]
    pub children: Vec<Task>,
}

// ------------------------------------------------------------------ Research

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StartResearch {
    /// Optional; resolved from `project_id` or the single organization.
    #[serde(default)]
    pub organization_id: Option<OrganizationId>,
    #[serde(default)]
    pub project_id: Option<ProjectId>,
    /// Repositories the researcher may read (must have local checkouts).
    #[serde(default)]
    pub repositories: Vec<RepositoryId>,
    pub question: String,
    /// The chat that spawned this research (drives the updates block).
    #[serde(default)]
    pub origin_conversation_id: Option<ConversationId>,
    #[serde(default)]
    pub model_profile_id: Option<ModelProfileId>,
}

// ------------------------------------------------------------------ Memory

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreateMemoryEntry {
    pub scope: MemoryScope,
    pub kind: MemoryKind,
    pub content: String,
    /// Set when the entry is added from inside a conversation (CLI).
    #[serde(default)]
    pub source_conversation_id: Option<ConversationId>,
}
