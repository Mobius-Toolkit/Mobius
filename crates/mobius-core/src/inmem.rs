//! In-memory implementation of the port traits, for unit tests and fixtures.

use crate::id::*;
use crate::model::*;
use crate::ports::*;
use std::sync::Mutex;

#[derive(Default)]
struct Inner {
    organizations: Vec<Organization>,
    repositories: Vec<Repository>,
    projects: Vec<Project>,
    agents: Vec<Agent>,
    model_profiles: Vec<ModelProfile>,
    harnesses: Vec<Harness>,
    tasks: Vec<Task>,
    runs: Vec<Run>,
    signals: Vec<Signal>,
    memory_entries: Vec<MemoryEntry>,
    conversations: Vec<Conversation>,
    messages: Vec<Message>,
    permission_requests: Vec<PermissionRequest>,
}

#[derive(Default)]
pub struct InMemoryStore {
    inner: Mutex<Inner>,
}

impl InMemoryStore {
    pub fn new() -> Self {
        Self::default()
    }
}

fn lock<'a>(inner: &'a Mutex<Inner>) -> Result<std::sync::MutexGuard<'a, Inner>, StoreError> {
    inner
        .lock()
        .map_err(|e| StoreError::Database(format!("mutex poisoned: {e}")))
}

fn upsert<T: Clone, F: Fn(&T) -> bool>(items: &mut Vec<T>, item: &T, pred: F) {
    if let Some(existing) = items.iter_mut().find(|i| pred(i)) {
        *existing = item.clone();
    } else {
        items.push(item.clone());
    }
}

fn get_by<T: Clone, F: Fn(&T) -> bool>(items: &[T], pred: F) -> Option<T> {
    items.iter().find(|i| pred(i)).cloned()
}

impl OrganizationRepo for InMemoryStore {
    async fn get_organization(&self, id: OrganizationId) -> StoreResult<Option<Organization>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.organizations, |o| o.id == id))
    }
    async fn find_organization_by_slug(&self, slug: &str) -> StoreResult<Option<Organization>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.organizations, |o| o.slug == slug))
    }
    async fn list_organizations(&self) -> StoreResult<Vec<Organization>> {
        Ok(lock(&self.inner)?.organizations.clone())
    }
    async fn insert_organization(&self, org: &Organization) -> StoreResult<()> {
        lock(&self.inner)?.organizations.push(org.clone());
        Ok(())
    }
    async fn update_organization(&self, org: &Organization) -> StoreResult<()> {
        upsert(&mut lock(&self.inner)?.organizations, org, |o| {
            o.id == org.id
        });
        Ok(())
    }
    async fn delete_organization(&self, id: OrganizationId) -> StoreResult<()> {
        lock(&self.inner)?.organizations.retain(|o| o.id != id);
        Ok(())
    }
}

impl RepositoryRepo for InMemoryStore {
    async fn get_repository(&self, id: RepositoryId) -> StoreResult<Option<Repository>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.repositories, |r| r.id == id))
    }
    async fn find_repository_by_owner_name(
        &self,
        owner: &str,
        name: &str,
    ) -> StoreResult<Option<Repository>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.repositories, |r| {
            r.owner == owner && r.name == name
        }))
    }
    async fn list_repositories(&self) -> StoreResult<Vec<Repository>> {
        Ok(lock(&self.inner)?.repositories.clone())
    }
    async fn insert_repository(&self, repo: &Repository) -> StoreResult<()> {
        lock(&self.inner)?.repositories.push(repo.clone());
        Ok(())
    }
    async fn update_repository(&self, repo: &Repository) -> StoreResult<()> {
        upsert(&mut lock(&self.inner)?.repositories, repo, |r| {
            r.id == repo.id
        });
        Ok(())
    }
    async fn delete_repository(&self, id: RepositoryId) -> StoreResult<()> {
        lock(&self.inner)?.repositories.retain(|r| r.id != id);
        Ok(())
    }
}

impl ProjectRepo for InMemoryStore {
    async fn get_project(&self, id: ProjectId) -> StoreResult<Option<Project>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.projects, |p| p.id == id))
    }
    async fn find_project_by_slug(
        &self,
        repository_id: RepositoryId,
        slug: &str,
    ) -> StoreResult<Option<Project>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.projects, |p| {
            p.repository_id == repository_id && p.slug == slug
        }))
    }
    async fn list_projects(&self) -> StoreResult<Vec<Project>> {
        Ok(lock(&self.inner)?.projects.clone())
    }
    async fn list_projects_by_repository(
        &self,
        repository_id: RepositoryId,
    ) -> StoreResult<Vec<Project>> {
        let inner = lock(&self.inner)?;
        Ok(inner
            .projects
            .iter()
            .filter(|p| p.repository_id == repository_id)
            .cloned()
            .collect())
    }
    async fn insert_project(&self, project: &Project) -> StoreResult<()> {
        lock(&self.inner)?.projects.push(project.clone());
        Ok(())
    }
    async fn update_project(&self, project: &Project) -> StoreResult<()> {
        upsert(&mut lock(&self.inner)?.projects, project, |p| {
            p.id == project.id
        });
        Ok(())
    }
    async fn delete_project(&self, id: ProjectId) -> StoreResult<()> {
        lock(&self.inner)?.projects.retain(|p| p.id != id);
        Ok(())
    }
}

impl AgentRepo for InMemoryStore {
    async fn get_agent(&self, id: AgentId) -> StoreResult<Option<Agent>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.agents, |a| a.id == id))
    }
    async fn find_agent_by_name(&self, name: &str) -> StoreResult<Option<Agent>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.agents, |a| a.name == name))
    }
    async fn list_agents(&self) -> StoreResult<Vec<Agent>> {
        Ok(lock(&self.inner)?.agents.clone())
    }
    async fn list_agents_by_project(&self, project_id: ProjectId) -> StoreResult<Vec<Agent>> {
        let inner = lock(&self.inner)?;
        Ok(inner
            .agents
            .iter()
            .filter(|a| a.project_id == Some(project_id))
            .cloned()
            .collect())
    }
    async fn insert_agent(&self, agent: &Agent) -> StoreResult<()> {
        lock(&self.inner)?.agents.push(agent.clone());
        Ok(())
    }
    async fn update_agent(&self, agent: &Agent) -> StoreResult<()> {
        upsert(&mut lock(&self.inner)?.agents, agent, |a| a.id == agent.id);
        Ok(())
    }
    async fn delete_agent(&self, id: AgentId) -> StoreResult<()> {
        lock(&self.inner)?.agents.retain(|a| a.id != id);
        Ok(())
    }
}

impl ModelProfileRepo for InMemoryStore {
    async fn get_model_profile(&self, id: ModelProfileId) -> StoreResult<Option<ModelProfile>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.model_profiles, |p| p.id == id))
    }
    async fn find_model_profile_by_name(&self, name: &str) -> StoreResult<Option<ModelProfile>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.model_profiles, |p| p.name == name))
    }
    async fn list_model_profiles(&self) -> StoreResult<Vec<ModelProfile>> {
        Ok(lock(&self.inner)?.model_profiles.clone())
    }
    async fn insert_model_profile(&self, profile: &ModelProfile) -> StoreResult<()> {
        lock(&self.inner)?.model_profiles.push(profile.clone());
        Ok(())
    }
    async fn update_model_profile(&self, profile: &ModelProfile) -> StoreResult<()> {
        upsert(&mut lock(&self.inner)?.model_profiles, profile, |p| {
            p.id == profile.id
        });
        Ok(())
    }
    async fn delete_model_profile(&self, id: ModelProfileId) -> StoreResult<()> {
        lock(&self.inner)?.model_profiles.retain(|p| p.id != id);
        Ok(())
    }
}

impl HarnessRepo for InMemoryStore {
    async fn get_harness(&self, id: HarnessId) -> StoreResult<Option<Harness>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.harnesses, |h| h.id == id))
    }
    async fn find_harness_by_name(&self, name: &str) -> StoreResult<Option<Harness>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.harnesses, |h| h.name == name))
    }
    async fn list_harnesses(&self) -> StoreResult<Vec<Harness>> {
        Ok(lock(&self.inner)?.harnesses.clone())
    }
    async fn insert_harness(&self, harness: &Harness) -> StoreResult<()> {
        lock(&self.inner)?.harnesses.push(harness.clone());
        Ok(())
    }
    async fn update_harness(&self, harness: &Harness) -> StoreResult<()> {
        upsert(&mut lock(&self.inner)?.harnesses, harness, |h| {
            h.id == harness.id
        });
        Ok(())
    }
    async fn delete_harness(&self, id: HarnessId) -> StoreResult<()> {
        lock(&self.inner)?.harnesses.retain(|h| h.id != id);
        Ok(())
    }
}

impl TaskRepo for InMemoryStore {
    async fn get_task(&self, id: TaskId) -> StoreResult<Option<Task>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.tasks, |t| t.id == id))
    }
    async fn list_tasks(&self) -> StoreResult<Vec<Task>> {
        Ok(lock(&self.inner)?.tasks.clone())
    }
    async fn list_tasks_by_project(&self, project_id: ProjectId) -> StoreResult<Vec<Task>> {
        let inner = lock(&self.inner)?;
        Ok(inner
            .tasks
            .iter()
            .filter(|t| t.project_id == project_id)
            .cloned()
            .collect())
    }
    async fn insert_task(&self, task: &Task) -> StoreResult<()> {
        lock(&self.inner)?.tasks.push(task.clone());
        Ok(())
    }
    async fn update_task(&self, task: &Task) -> StoreResult<()> {
        upsert(&mut lock(&self.inner)?.tasks, task, |t| t.id == task.id);
        Ok(())
    }
    async fn delete_task(&self, id: TaskId) -> StoreResult<()> {
        lock(&self.inner)?.tasks.retain(|t| t.id != id);
        Ok(())
    }
}

impl RunRepo for InMemoryStore {
    async fn get_run(&self, id: RunId) -> StoreResult<Option<Run>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.runs, |r| r.id == id))
    }
    async fn list_runs(&self) -> StoreResult<Vec<Run>> {
        Ok(lock(&self.inner)?.runs.clone())
    }
    async fn list_runs_by_task(&self, task_id: TaskId) -> StoreResult<Vec<Run>> {
        let inner = lock(&self.inner)?;
        Ok(inner
            .runs
            .iter()
            .filter(|r| r.task_id == task_id)
            .cloned()
            .collect())
    }
    async fn insert_run(&self, run: &Run) -> StoreResult<()> {
        lock(&self.inner)?.runs.push(run.clone());
        Ok(())
    }
    async fn update_run(&self, run: &Run) -> StoreResult<()> {
        upsert(&mut lock(&self.inner)?.runs, run, |r| r.id == run.id);
        Ok(())
    }
    async fn delete_run(&self, id: RunId) -> StoreResult<()> {
        lock(&self.inner)?.runs.retain(|r| r.id != id);
        Ok(())
    }
}

impl SignalRepo for InMemoryStore {
    async fn get_signal(&self, id: SignalId) -> StoreResult<Option<Signal>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.signals, |s| s.id == id))
    }
    async fn find_signal_by_dedupe_key(&self, dedupe_key: &str) -> StoreResult<Option<Signal>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.signals, |s| s.dedupe_key == dedupe_key))
    }
    async fn list_signals(&self) -> StoreResult<Vec<Signal>> {
        Ok(lock(&self.inner)?.signals.clone())
    }
    async fn insert_signal(&self, signal: &Signal) -> StoreResult<()> {
        lock(&self.inner)?.signals.push(signal.clone());
        Ok(())
    }
    async fn delete_signal(&self, id: SignalId) -> StoreResult<()> {
        lock(&self.inner)?.signals.retain(|s| s.id != id);
        Ok(())
    }
}

impl MemoryRepo for InMemoryStore {
    async fn get_memory_entry(&self, id: MemoryEntryId) -> StoreResult<Option<MemoryEntry>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.memory_entries, |e| e.id == id))
    }
    async fn list_memory_entries(&self) -> StoreResult<Vec<MemoryEntry>> {
        Ok(lock(&self.inner)?.memory_entries.clone())
    }
    async fn list_memory_by_scope(&self, scope: &MemoryScope) -> StoreResult<Vec<MemoryEntry>> {
        let inner = lock(&self.inner)?;
        Ok(inner
            .memory_entries
            .iter()
            .filter(|e| &e.scope == scope)
            .cloned()
            .collect())
    }
    async fn insert_memory_entry(&self, entry: &MemoryEntry) -> StoreResult<()> {
        lock(&self.inner)?.memory_entries.push(entry.clone());
        Ok(())
    }
    async fn update_memory_entry(&self, entry: &MemoryEntry) -> StoreResult<()> {
        upsert(&mut lock(&self.inner)?.memory_entries, entry, |e| {
            e.id == entry.id
        });
        Ok(())
    }
    async fn delete_memory_entry(&self, id: MemoryEntryId) -> StoreResult<()> {
        lock(&self.inner)?.memory_entries.retain(|e| e.id != id);
        Ok(())
    }
}

impl ConversationRepo for InMemoryStore {
    async fn get_conversation(&self, id: ConversationId) -> StoreResult<Option<Conversation>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.conversations, |c| c.id == id))
    }
    async fn list_conversations(&self) -> StoreResult<Vec<Conversation>> {
        Ok(lock(&self.inner)?.conversations.clone())
    }
    async fn insert_conversation(&self, conversation: &Conversation) -> StoreResult<()> {
        lock(&self.inner)?.conversations.push(conversation.clone());
        Ok(())
    }
    async fn update_conversation(&self, conversation: &Conversation) -> StoreResult<()> {
        upsert(&mut lock(&self.inner)?.conversations, conversation, |c| {
            c.id == conversation.id
        });
        Ok(())
    }
    async fn delete_conversation(&self, id: ConversationId) -> StoreResult<()> {
        lock(&self.inner)?.conversations.retain(|c| c.id != id);
        Ok(())
    }
}

impl MessageRepo for InMemoryStore {
    async fn get_message(&self, id: MessageId) -> StoreResult<Option<Message>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.messages, |m| m.id == id))
    }
    async fn list_messages_by_conversation(
        &self,
        conversation_id: ConversationId,
    ) -> StoreResult<Vec<Message>> {
        let inner = lock(&self.inner)?;
        Ok(inner
            .messages
            .iter()
            .filter(|m| m.conversation_id == conversation_id)
            .cloned()
            .collect())
    }
    async fn insert_message(&self, message: &Message) -> StoreResult<()> {
        lock(&self.inner)?.messages.push(message.clone());
        Ok(())
    }
    async fn update_message(&self, message: &Message) -> StoreResult<()> {
        upsert(&mut lock(&self.inner)?.messages, message, |m| {
            m.id == message.id
        });
        Ok(())
    }
    async fn delete_message(&self, id: MessageId) -> StoreResult<()> {
        lock(&self.inner)?.messages.retain(|m| m.id != id);
        Ok(())
    }
}

impl PermissionRequestRepo for InMemoryStore {
    async fn get_permission_request(
        &self,
        id: PermissionRequestId,
    ) -> StoreResult<Option<PermissionRequest>> {
        let inner = lock(&self.inner)?;
        Ok(get_by(&inner.permission_requests, |p| p.id == id))
    }
    async fn list_permission_requests(&self) -> StoreResult<Vec<PermissionRequest>> {
        Ok(lock(&self.inner)?.permission_requests.clone())
    }
    async fn list_pending_permission_requests(&self) -> StoreResult<Vec<PermissionRequest>> {
        let inner = lock(&self.inner)?;
        Ok(inner
            .permission_requests
            .iter()
            .filter(|p| p.status == PermissionRequestStatus::Pending)
            .cloned()
            .collect())
    }
    async fn insert_permission_request(&self, request: &PermissionRequest) -> StoreResult<()> {
        lock(&self.inner)?.permission_requests.push(request.clone());
        Ok(())
    }
    async fn update_permission_request(&self, request: &PermissionRequest) -> StoreResult<()> {
        upsert(&mut lock(&self.inner)?.permission_requests, request, |p| {
            p.id == request.id
        });
        Ok(())
    }
}
