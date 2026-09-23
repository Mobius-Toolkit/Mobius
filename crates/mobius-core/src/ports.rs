use crate::id::*;
use crate::model::*;
use std::future::Future;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum StoreError {
    #[error("not found: {0}")]
    NotFound(String),
    #[error("conflict: {0}")]
    Conflict(String),
    #[error("database error: {0}")]
    Database(String),
    #[error("serialization error: {0}")]
    Serialization(String),
}

pub type StoreResult<T> = Result<T, StoreError>;

// Trait methods are desugared (`fn -> impl Future + Send` rather than
// `async fn`) so callers in generic code can prove the returned futures are
// `Send` — required by axum handlers and spawned tasks. Implementations may
// still write `async fn` bodies.

pub trait OrganizationRepo: Send + Sync {
    fn get_organization(
        &self,
        id: OrganizationId,
    ) -> impl Future<Output = StoreResult<Option<Organization>>> + Send;
    fn find_organization_by_slug(
        &self,
        slug: &str,
    ) -> impl Future<Output = StoreResult<Option<Organization>>> + Send;
    fn list_organizations(&self) -> impl Future<Output = StoreResult<Vec<Organization>>> + Send;
    fn insert_organization(
        &self,
        org: &Organization,
    ) -> impl Future<Output = StoreResult<()>> + Send;
    fn update_organization(
        &self,
        org: &Organization,
    ) -> impl Future<Output = StoreResult<()>> + Send;
    fn delete_organization(
        &self,
        id: OrganizationId,
    ) -> impl Future<Output = StoreResult<()>> + Send;
}

pub trait RepositoryRepo: Send + Sync {
    fn get_repository(
        &self,
        id: RepositoryId,
    ) -> impl Future<Output = StoreResult<Option<Repository>>> + Send;
    fn find_repository_by_owner_name(
        &self,
        owner: &str,
        name: &str,
    ) -> impl Future<Output = StoreResult<Option<Repository>>> + Send;
    fn list_repositories(&self) -> impl Future<Output = StoreResult<Vec<Repository>>> + Send;
    fn insert_repository(&self, repo: &Repository) -> impl Future<Output = StoreResult<()>> + Send;
    fn update_repository(&self, repo: &Repository) -> impl Future<Output = StoreResult<()>> + Send;
    fn delete_repository(&self, id: RepositoryId) -> impl Future<Output = StoreResult<()>> + Send;
}

pub trait ProjectRepo: Send + Sync {
    fn get_project(
        &self,
        id: ProjectId,
    ) -> impl Future<Output = StoreResult<Option<Project>>> + Send;
    fn find_project_by_slug(
        &self,
        repository_id: RepositoryId,
        slug: &str,
    ) -> impl Future<Output = StoreResult<Option<Project>>> + Send;
    fn list_projects(&self) -> impl Future<Output = StoreResult<Vec<Project>>> + Send;
    fn list_projects_by_repository(
        &self,
        repository_id: RepositoryId,
    ) -> impl Future<Output = StoreResult<Vec<Project>>> + Send;
    fn insert_project(&self, project: &Project) -> impl Future<Output = StoreResult<()>> + Send;
    fn update_project(&self, project: &Project) -> impl Future<Output = StoreResult<()>> + Send;
    fn delete_project(&self, id: ProjectId) -> impl Future<Output = StoreResult<()>> + Send;
}

pub trait AgentRepo: Send + Sync {
    fn get_agent(&self, id: AgentId) -> impl Future<Output = StoreResult<Option<Agent>>> + Send;
    fn find_agent_by_name(
        &self,
        name: &str,
    ) -> impl Future<Output = StoreResult<Option<Agent>>> + Send;
    fn list_agents(&self) -> impl Future<Output = StoreResult<Vec<Agent>>> + Send;
    fn list_agents_by_project(
        &self,
        project_id: ProjectId,
    ) -> impl Future<Output = StoreResult<Vec<Agent>>> + Send;
    fn insert_agent(&self, agent: &Agent) -> impl Future<Output = StoreResult<()>> + Send;
    fn update_agent(&self, agent: &Agent) -> impl Future<Output = StoreResult<()>> + Send;
    fn delete_agent(&self, id: AgentId) -> impl Future<Output = StoreResult<()>> + Send;
}

pub trait ModelProfileRepo: Send + Sync {
    fn get_model_profile(
        &self,
        id: ModelProfileId,
    ) -> impl Future<Output = StoreResult<Option<ModelProfile>>> + Send;
    fn find_model_profile_by_name(
        &self,
        name: &str,
    ) -> impl Future<Output = StoreResult<Option<ModelProfile>>> + Send;
    fn list_model_profiles(&self) -> impl Future<Output = StoreResult<Vec<ModelProfile>>> + Send;
    fn insert_model_profile(
        &self,
        profile: &ModelProfile,
    ) -> impl Future<Output = StoreResult<()>> + Send;
    fn update_model_profile(
        &self,
        profile: &ModelProfile,
    ) -> impl Future<Output = StoreResult<()>> + Send;
    fn delete_model_profile(
        &self,
        id: ModelProfileId,
    ) -> impl Future<Output = StoreResult<()>> + Send;
}

pub trait HarnessRepo: Send + Sync {
    fn get_harness(
        &self,
        id: HarnessId,
    ) -> impl Future<Output = StoreResult<Option<Harness>>> + Send;
    fn find_harness_by_name(
        &self,
        name: &str,
    ) -> impl Future<Output = StoreResult<Option<Harness>>> + Send;
    fn list_harnesses(&self) -> impl Future<Output = StoreResult<Vec<Harness>>> + Send;
    fn insert_harness(&self, harness: &Harness) -> impl Future<Output = StoreResult<()>> + Send;
    fn update_harness(&self, harness: &Harness) -> impl Future<Output = StoreResult<()>> + Send;
    fn delete_harness(&self, id: HarnessId) -> impl Future<Output = StoreResult<()>> + Send;
}

pub trait TaskRepo: Send + Sync {
    fn get_task(&self, id: TaskId) -> impl Future<Output = StoreResult<Option<Task>>> + Send;
    fn list_tasks(&self) -> impl Future<Output = StoreResult<Vec<Task>>> + Send;
    fn list_tasks_by_project(
        &self,
        project_id: ProjectId,
    ) -> impl Future<Output = StoreResult<Vec<Task>>> + Send;
    fn insert_task(&self, task: &Task) -> impl Future<Output = StoreResult<()>> + Send;
    fn update_task(&self, task: &Task) -> impl Future<Output = StoreResult<()>> + Send;
    fn delete_task(&self, id: TaskId) -> impl Future<Output = StoreResult<()>> + Send;
}

pub trait RunRepo: Send + Sync {
    fn get_run(&self, id: RunId) -> impl Future<Output = StoreResult<Option<Run>>> + Send;
    fn list_runs(&self) -> impl Future<Output = StoreResult<Vec<Run>>> + Send;
    fn list_runs_by_task(
        &self,
        task_id: TaskId,
    ) -> impl Future<Output = StoreResult<Vec<Run>>> + Send;
    fn insert_run(&self, run: &Run) -> impl Future<Output = StoreResult<()>> + Send;
    fn update_run(&self, run: &Run) -> impl Future<Output = StoreResult<()>> + Send;
    fn delete_run(&self, id: RunId) -> impl Future<Output = StoreResult<()>> + Send;
}

pub trait SignalRepo: Send + Sync {
    fn get_signal(&self, id: SignalId) -> impl Future<Output = StoreResult<Option<Signal>>> + Send;
    fn find_signal_by_dedupe_key(
        &self,
        dedupe_key: &str,
    ) -> impl Future<Output = StoreResult<Option<Signal>>> + Send;
    fn list_signals(&self) -> impl Future<Output = StoreResult<Vec<Signal>>> + Send;
    fn insert_signal(&self, signal: &Signal) -> impl Future<Output = StoreResult<()>> + Send;
    fn delete_signal(&self, id: SignalId) -> impl Future<Output = StoreResult<()>> + Send;
}

pub trait MemoryRepo: Send + Sync {
    fn get_memory_entry(
        &self,
        id: MemoryEntryId,
    ) -> impl Future<Output = StoreResult<Option<MemoryEntry>>> + Send;
    fn list_memory_entries(&self) -> impl Future<Output = StoreResult<Vec<MemoryEntry>>> + Send;
    fn list_memory_by_scope(
        &self,
        scope: &MemoryScope,
    ) -> impl Future<Output = StoreResult<Vec<MemoryEntry>>> + Send;
    fn insert_memory_entry(
        &self,
        entry: &MemoryEntry,
    ) -> impl Future<Output = StoreResult<()>> + Send;
    fn update_memory_entry(
        &self,
        entry: &MemoryEntry,
    ) -> impl Future<Output = StoreResult<()>> + Send;
    fn delete_memory_entry(
        &self,
        id: MemoryEntryId,
    ) -> impl Future<Output = StoreResult<()>> + Send;
}

pub trait ConversationRepo: Send + Sync {
    fn get_conversation(
        &self,
        id: ConversationId,
    ) -> impl Future<Output = StoreResult<Option<Conversation>>> + Send;
    fn list_conversations(&self) -> impl Future<Output = StoreResult<Vec<Conversation>>> + Send;
    fn insert_conversation(
        &self,
        conversation: &Conversation,
    ) -> impl Future<Output = StoreResult<()>> + Send;
    fn update_conversation(
        &self,
        conversation: &Conversation,
    ) -> impl Future<Output = StoreResult<()>> + Send;
    fn delete_conversation(
        &self,
        id: ConversationId,
    ) -> impl Future<Output = StoreResult<()>> + Send;
}

pub trait MessageRepo: Send + Sync {
    fn get_message(
        &self,
        id: MessageId,
    ) -> impl Future<Output = StoreResult<Option<Message>>> + Send;
    fn list_messages_by_conversation(
        &self,
        conversation_id: ConversationId,
    ) -> impl Future<Output = StoreResult<Vec<Message>>> + Send;
    fn insert_message(&self, message: &Message) -> impl Future<Output = StoreResult<()>> + Send;
    fn update_message(&self, message: &Message) -> impl Future<Output = StoreResult<()>> + Send;
    fn delete_message(&self, id: MessageId) -> impl Future<Output = StoreResult<()>> + Send;
}

pub trait PermissionRequestRepo: Send + Sync {
    fn get_permission_request(
        &self,
        id: PermissionRequestId,
    ) -> impl Future<Output = StoreResult<Option<PermissionRequest>>> + Send;
    fn list_permission_requests(
        &self,
    ) -> impl Future<Output = StoreResult<Vec<PermissionRequest>>> + Send;
    fn list_pending_permission_requests(
        &self,
    ) -> impl Future<Output = StoreResult<Vec<PermissionRequest>>> + Send;
    fn insert_permission_request(
        &self,
        request: &PermissionRequest,
    ) -> impl Future<Output = StoreResult<()>> + Send;
    fn update_permission_request(
        &self,
        request: &PermissionRequest,
    ) -> impl Future<Output = StoreResult<()>> + Send;
}

/// `Arc<T>` delegates to the inner store impl, so `Arc<InMemoryStore>` and
/// `Arc<SqliteStore>` satisfy the same `Store` bound.
macro_rules! delegate_to_arc {
    // rustfmt renders the items as `fn …();,` — accept `;`-terminated items
    // separated by commas.
    ($trait:ident, $(fn $m:ident(&self $(, $a:ident : $t:ty)* $(,)?) -> $out:ty;),* $(,)?) => {
        impl<T: $trait + ?Sized> $trait for std::sync::Arc<T> {
            $(
                fn $m(&self $(, $a: $t)*) -> impl Future<Output = $out> + Send {
                    (**self).$m($($a),*)
                }
            )*
        }
    };
}

delegate_to_arc!(
    OrganizationRepo,
    fn get_organization(&self, id: OrganizationId) -> StoreResult<Option<Organization>>;,
    fn find_organization_by_slug(&self, slug: &str) -> StoreResult<Option<Organization>>;,
    fn list_organizations(&self) -> StoreResult<Vec<Organization>>;,
    fn insert_organization(&self, org: &Organization) -> StoreResult<()>;,
    fn update_organization(&self, org: &Organization) -> StoreResult<()>;,
    fn delete_organization(&self, id: OrganizationId) -> StoreResult<()>;
);

delegate_to_arc!(
    RepositoryRepo,
    fn get_repository(&self, id: RepositoryId) -> StoreResult<Option<Repository>>;,
    fn find_repository_by_owner_name(
        &self,
        owner: &str,
        name: &str,
    ) -> StoreResult<Option<Repository>>;,
    fn list_repositories(&self) -> StoreResult<Vec<Repository>>;,
    fn insert_repository(&self, repo: &Repository) -> StoreResult<()>;,
    fn update_repository(&self, repo: &Repository) -> StoreResult<()>;,
    fn delete_repository(&self, id: RepositoryId) -> StoreResult<()>;
);

delegate_to_arc!(
    ProjectRepo,
    fn get_project(&self, id: ProjectId) -> StoreResult<Option<Project>>;,
    fn find_project_by_slug(
        &self,
        repository_id: RepositoryId,
        slug: &str,
    ) -> StoreResult<Option<Project>>;,
    fn list_projects(&self) -> StoreResult<Vec<Project>>;,
    fn list_projects_by_repository(&self, repository_id: RepositoryId)
    -> StoreResult<Vec<Project>>;,
    fn insert_project(&self, project: &Project) -> StoreResult<()>;,
    fn update_project(&self, project: &Project) -> StoreResult<()>;,
    fn delete_project(&self, id: ProjectId) -> StoreResult<()>;
);

delegate_to_arc!(
    AgentRepo,
    fn get_agent(&self, id: AgentId) -> StoreResult<Option<Agent>>;,
    fn find_agent_by_name(&self, name: &str) -> StoreResult<Option<Agent>>;,
    fn list_agents(&self) -> StoreResult<Vec<Agent>>;,
    fn list_agents_by_project(&self, project_id: ProjectId) -> StoreResult<Vec<Agent>>;,
    fn insert_agent(&self, agent: &Agent) -> StoreResult<()>;,
    fn update_agent(&self, agent: &Agent) -> StoreResult<()>;,
    fn delete_agent(&self, id: AgentId) -> StoreResult<()>;
);

delegate_to_arc!(
    ModelProfileRepo,
    fn get_model_profile(&self, id: ModelProfileId) -> StoreResult<Option<ModelProfile>>;,
    fn find_model_profile_by_name(&self, name: &str) -> StoreResult<Option<ModelProfile>>;,
    fn list_model_profiles(&self) -> StoreResult<Vec<ModelProfile>>;,
    fn insert_model_profile(&self, profile: &ModelProfile) -> StoreResult<()>;,
    fn update_model_profile(&self, profile: &ModelProfile) -> StoreResult<()>;,
    fn delete_model_profile(&self, id: ModelProfileId) -> StoreResult<()>;
);

delegate_to_arc!(
    HarnessRepo,
    fn get_harness(&self, id: HarnessId) -> StoreResult<Option<Harness>>;,
    fn find_harness_by_name(&self, name: &str) -> StoreResult<Option<Harness>>;,
    fn list_harnesses(&self) -> StoreResult<Vec<Harness>>;,
    fn insert_harness(&self, harness: &Harness) -> StoreResult<()>;,
    fn update_harness(&self, harness: &Harness) -> StoreResult<()>;,
    fn delete_harness(&self, id: HarnessId) -> StoreResult<()>;
);

delegate_to_arc!(
    TaskRepo,
    fn get_task(&self, id: TaskId) -> StoreResult<Option<Task>>;,
    fn list_tasks(&self) -> StoreResult<Vec<Task>>;,
    fn list_tasks_by_project(&self, project_id: ProjectId) -> StoreResult<Vec<Task>>;,
    fn insert_task(&self, task: &Task) -> StoreResult<()>;,
    fn update_task(&self, task: &Task) -> StoreResult<()>;,
    fn delete_task(&self, id: TaskId) -> StoreResult<()>;
);

delegate_to_arc!(
    RunRepo,
    fn get_run(&self, id: RunId) -> StoreResult<Option<Run>>;,
    fn list_runs(&self) -> StoreResult<Vec<Run>>;,
    fn list_runs_by_task(&self, task_id: TaskId) -> StoreResult<Vec<Run>>;,
    fn insert_run(&self, run: &Run) -> StoreResult<()>;,
    fn update_run(&self, run: &Run) -> StoreResult<()>;,
    fn delete_run(&self, id: RunId) -> StoreResult<()>;
);

delegate_to_arc!(
    SignalRepo,
    fn get_signal(&self, id: SignalId) -> StoreResult<Option<Signal>>;,
    fn find_signal_by_dedupe_key(&self, dedupe_key: &str) -> StoreResult<Option<Signal>>;,
    fn list_signals(&self) -> StoreResult<Vec<Signal>>;,
    fn insert_signal(&self, signal: &Signal) -> StoreResult<()>;,
    fn delete_signal(&self, id: SignalId) -> StoreResult<()>;
);

delegate_to_arc!(
    MemoryRepo,
    fn get_memory_entry(&self, id: MemoryEntryId) -> StoreResult<Option<MemoryEntry>>;,
    fn list_memory_entries(&self) -> StoreResult<Vec<MemoryEntry>>;,
    fn list_memory_by_scope(&self, scope: &MemoryScope) -> StoreResult<Vec<MemoryEntry>>;,
    fn insert_memory_entry(&self, entry: &MemoryEntry) -> StoreResult<()>;,
    fn update_memory_entry(&self, entry: &MemoryEntry) -> StoreResult<()>;,
    fn delete_memory_entry(&self, id: MemoryEntryId) -> StoreResult<()>;
);

delegate_to_arc!(
    ConversationRepo,
    fn get_conversation(&self, id: ConversationId) -> StoreResult<Option<Conversation>>;,
    fn list_conversations(&self) -> StoreResult<Vec<Conversation>>;,
    fn insert_conversation(&self, conversation: &Conversation) -> StoreResult<()>;,
    fn update_conversation(&self, conversation: &Conversation) -> StoreResult<()>;,
    fn delete_conversation(&self, id: ConversationId) -> StoreResult<()>;
);

delegate_to_arc!(
    MessageRepo,
    fn get_message(&self, id: MessageId) -> StoreResult<Option<Message>>;,
    fn list_messages_by_conversation(
        &self,
        conversation_id: ConversationId,
    ) -> StoreResult<Vec<Message>>;,
    fn insert_message(&self, message: &Message) -> StoreResult<()>;,
    fn update_message(&self, message: &Message) -> StoreResult<()>;,
    fn delete_message(&self, id: MessageId) -> StoreResult<()>;
);

delegate_to_arc!(
    PermissionRequestRepo,
    fn get_permission_request(
        &self,
        id: PermissionRequestId,
    ) -> StoreResult<Option<PermissionRequest>>;,
    fn list_permission_requests(&self) -> StoreResult<Vec<PermissionRequest>>;,
    fn list_pending_permission_requests(&self) -> StoreResult<Vec<PermissionRequest>>;,
    fn insert_permission_request(&self, request: &PermissionRequest) -> StoreResult<()>;,
    fn update_permission_request(&self, request: &PermissionRequest) -> StoreResult<()>;
);

/// Convenience supertrait implemented by every store that implements all port
/// traits (blanket impl).
pub trait Store:
    OrganizationRepo
    + RepositoryRepo
    + ProjectRepo
    + AgentRepo
    + ModelProfileRepo
    + HarnessRepo
    + TaskRepo
    + RunRepo
    + SignalRepo
    + MemoryRepo
    + ConversationRepo
    + MessageRepo
    + PermissionRequestRepo
{
}

impl<T> Store for T where
    T: OrganizationRepo
        + RepositoryRepo
        + ProjectRepo
        + AgentRepo
        + ModelProfileRepo
        + HarnessRepo
        + TaskRepo
        + RunRepo
        + SignalRepo
        + MemoryRepo
        + ConversationRepo
        + MessageRepo
        + PermissionRequestRepo
{
}
