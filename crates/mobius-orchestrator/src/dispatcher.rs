//! Signal → Task routing and task execution. Runs are conversations now:
//! `start_task` opens a `Run` conversation in a fresh worktree and drives
//! `SessionManager::run_turn` in the background.

use crate::agents::AgentProvisioner;
use crate::error::OrchestratorError;
use crate::prompt::{MemoryGroup, RunContext, run_prompt};
use crate::research::message_text;
use crate::resolver::ProfileResolver;
use crate::session_manager::{OpenConversation, SessionManager};
use crate::worktree::WorktreeManager;
use mobius_core::*;
use std::path::PathBuf;
use std::sync::Arc;
use tracing::{info, warn};

pub type EmitFn = Arc<dyn Fn(DomainEvent) + Send + Sync>;

/// Routing: a `repository_id` on the signal narrows candidates to projects
/// whose `repository_ids` hint contains it (there is no "primary"
/// repository). A single surviving candidate wins outright; several are
/// disambiguated by scope keyword/label contains-match on title+body. When
/// the repo hint matches no project at all, content routing across all
/// active projects is the fallback.
pub fn route_signal<'a>(signal: &Signal, projects: &'a [Project]) -> Option<&'a Project> {
    // A signal already carrying a project routes directly.
    if let Some(pid) = signal.project_id {
        return projects.iter().find(|p| p.id == pid);
    }
    let haystack = format!("{}\n{}", signal.title, signal.body).to_lowercase();
    let keyword = |p: &Project| {
        p.scope
            .keywords
            .iter()
            .chain(p.scope.labels.iter())
            .any(|k| !k.is_empty() && haystack.contains(&k.to_lowercase()))
    };
    let by_repo: Vec<&Project> = projects
        .iter()
        .filter(|p| {
            signal
                .repository_id
                .is_none_or(|r| p.repository_ids.contains(&r))
        })
        .filter(|p| p.status == ProjectStatus::Active)
        .collect();
    if let [only] = by_repo.as_slice() {
        return Some(*only);
    }
    if let Some(p) = by_repo.iter().find(|p| keyword(p)) {
        return Some(*p);
    }
    // The repo hint matched nothing — no project claims the repository, so
    // fall back to content routing across all active projects.
    if signal.repository_id.is_some() && by_repo.is_empty() {
        return projects
            .iter()
            .filter(|p| p.status == ProjectStatus::Active)
            .find(|p| keyword(p));
    }
    None
}

/// Specification for [`Dispatcher::create_task`].
pub struct CreateTaskSpec {
    pub project_id: ProjectId,
    /// The repository the task executes against. Required to *run* — a task
    /// without one can be proposed but not started.
    pub repository_id: Option<RepositoryId>,
    pub title: String,
    pub description: String,
    pub kind: TaskKind,
    pub parent_task_id: Option<TaskId>,
    pub priority: Priority,
    /// Set when the task was created from a chat.
    pub origin_conversation_id: Option<ConversationId>,
}

pub struct Dispatcher<S: Store> {
    store: Arc<S>,
    emit: EmitFn,
    sessions: Arc<SessionManager<S>>,
    worktrees: WorktreeManager,
}

impl<S: Store + 'static> Dispatcher<S> {
    pub fn new(
        store: Arc<S>,
        emit: EmitFn,
        sessions: Arc<SessionManager<S>>,
        worktree_dir: PathBuf,
    ) -> Self {
        Self {
            store,
            emit,
            sessions,
            worktrees: WorktreeManager::new(worktree_dir),
        }
    }

    /// Validate and insert a `Proposed` task, emit `TaskCreated`.
    pub async fn create_task(&self, spec: CreateTaskSpec) -> Result<Task, OrchestratorError> {
        let project = self
            .store
            .get_project(spec.project_id)
            .await?
            .ok_or_else(|| OrchestratorError::NotFound(format!("project {}", spec.project_id)))?;
        if let Some(repo_id) = spec.repository_id {
            self.store
                .get_repository(repo_id)
                .await?
                .ok_or_else(|| OrchestratorError::NotFound(format!("repository {repo_id}")))?;
        }
        if let Some(parent_id) = spec.parent_task_id {
            let parent =
                self.store.get_task(parent_id).await?.ok_or_else(|| {
                    OrchestratorError::NotFound(format!("parent task {parent_id}"))
                })?;
            if parent.project_id != project.id {
                return Err(OrchestratorError::Config(
                    "parent task belongs to a different project".to_string(),
                ));
            }
        }
        if spec.kind == TaskKind::Triage {
            return Err(OrchestratorError::Config(
                "kind triage is reserved for signal-routed tasks".to_string(),
            ));
        }
        let agent = AgentProvisioner::ensure_project_agent(self.store.as_ref(), &project).await?;
        let now = chrono::Utc::now();
        let task = Task {
            id: TaskId::new(),
            project_id: project.id,
            agent_id: agent.id,
            parent_task_id: spec.parent_task_id,
            title: spec.title,
            description: spec.description,
            repository_id: spec.repository_id,
            kind: spec.kind,
            status: TaskStatus::Proposed,
            origin: TaskOrigin {
                conversation_id: spec.origin_conversation_id,
                ..TaskOrigin::default()
            },
            priority: spec.priority,
            created_at: now,
            updated_at: now,
        };
        self.store.insert_task(&task).await?;
        (self.emit)(DomainEvent::TaskCreated { task: task.clone() });
        Ok(task)
    }

    /// Route a signal to a project, create a `Proposed` task owned by the
    /// project's Project-role agent, emit `TaskCreated`.
    pub async fn handle_signal(&self, signal: &Signal) -> Result<Option<Task>, OrchestratorError> {
        let projects = self.store.list_projects().await?;
        let project = match signal.project_id {
            Some(id) => match self.store.get_project(id).await? {
                Some(p) => p,
                None => return Ok(None),
            },
            None => match route_signal(signal, &projects) {
                Some(p) => p.clone(),
                None => {
                    info!(dedupe = %signal.dedupe_key, "signal routed to no project");
                    return Ok(None);
                }
            },
        };
        let agent = AgentProvisioner::ensure_project_agent(self.store.as_ref(), &project).await?;
        let now = chrono::Utc::now();
        let task = Task {
            id: TaskId::new(),
            project_id: project.id,
            agent_id: agent.id,
            parent_task_id: None,
            title: signal.title.clone(),
            description: signal.body.clone(),
            repository_id: signal.repository_id,
            kind: TaskKind::Triage,
            status: TaskStatus::Proposed,
            origin: TaskOrigin {
                signal_id: Some(signal.id),
                github_issue: signal
                    .payload
                    .get("number")
                    .and_then(|v| v.as_u64())
                    .filter(|_| {
                        matches!(
                            signal.kind,
                            SignalKind::IssueOpened
                                | SignalKind::IssueCommented
                                | SignalKind::IssueLabeled
                        )
                    }),
                github_pr: signal
                    .payload
                    .get("number")
                    .and_then(|v| v.as_u64())
                    .filter(|_| {
                        matches!(
                            signal.kind,
                            SignalKind::PullRequestOpened
                                | SignalKind::PullRequestReviewRequested
                                | SignalKind::PullRequestCommented
                        )
                    }),
                conversation_id: None,
            },
            priority: Priority::Normal,
            created_at: now,
            updated_at: now,
        };
        self.store.insert_task(&task).await?;
        (self.emit)(DomainEvent::TaskCreated { task: task.clone() });
        Ok(Some(task))
    }

    fn transition(&self, task: &mut Task, to: TaskStatus) -> Result<(), OrchestratorError> {
        if !task.status.can_transition_to(&to) {
            return Err(OrchestratorError::InvalidTransition {
                from: task.status,
                to,
            });
        }
        let from = task.status;
        task.status = to;
        task.updated_at = chrono::Utc::now();
        (self.emit)(DomainEvent::TaskStatusChanged {
            task_id: task.id,
            from,
            to,
        });
        Ok(())
    }

    /// Start a task: transition to `Running`, create the worktree, open the
    /// `Run` conversation, and drive `run_turn` in the background. Returns
    /// the `Run` immediately.
    pub async fn start_task(&self, task_id: TaskId) -> Result<Run, OrchestratorError> {
        let mut task = self
            .store
            .get_task(task_id)
            .await?
            .ok_or_else(|| OrchestratorError::NotFound(format!("task {task_id}")))?;
        // Fast-path legal transitions to Running.
        for next in [
            TaskStatus::Approved,
            TaskStatus::Queued,
            TaskStatus::Running,
        ] {
            if task.status != TaskStatus::Running && task.status.can_transition_to(&next) {
                self.transition(&mut task, next)?;
                self.store.update_task(&task).await?;
            }
        }
        if task.status != TaskStatus::Running {
            return Err(OrchestratorError::InvalidTransition {
                from: task.status,
                to: TaskStatus::Running,
            });
        }

        // Exactly one repository to run against — required.
        let repo_id = task.repository_id.ok_or_else(|| {
            OrchestratorError::Config(format!(
                "task {task_id} has no repository; set one before starting"
            ))
        })?;
        let repo = self
            .store
            .get_repository(repo_id)
            .await?
            .ok_or_else(|| OrchestratorError::NotFound(format!("repository {repo_id}")))?;
        let local = repo.local_path.clone().ok_or_else(|| {
            OrchestratorError::Config(format!(
                "repository {}/{} has no local checkout",
                repo.owner, repo.name
            ))
        })?;
        let project = self.store.get_project(task.project_id).await?;
        let agent = self
            .store
            .get_agent(task.agent_id)
            .await?
            .ok_or_else(|| OrchestratorError::NotFound("agent".into()))?;
        let activity = Activity::from(task.kind);
        let (profile, harness) =
            ProfileResolver::resolve(self.store.as_ref(), &agent, activity).await?;

        let branch = format!("mobius/task-{}", &task.id.to_string()[..8]);
        // No checkout fallback: an unattended implementer must never run in
        // the user's real working copy. Worktree failure → task Failed
        // (`Failed → Queued` stays legal, so it can be retried).
        let cwd = match self.worktrees.create(&local, &branch).await {
            Ok(p) => p,
            Err(e) => {
                warn!(error = %e, "worktree create failed");
                task.status = TaskStatus::Failed;
                task.updated_at = chrono::Utc::now();
                self.store.update_task(&task).await?;
                (self.emit)(DomainEvent::EntityChanged {
                    kind: EntityKind::Task,
                    id: task.id.to_string(),
                });
                return Err(e);
            }
        };

        // Parent chain briefs, root first.
        let mut parents = Vec::new();
        let mut cursor = task.parent_task_id;
        while let Some(id) = cursor {
            match self.store.get_task(id).await? {
                Some(parent) => {
                    cursor = parent.parent_task_id;
                    parents.push(parent);
                }
                None => break,
            }
        }
        parents.reverse();

        // Memory groups: org → repo → project.
        let mut memory = Vec::new();
        if let Some(p) = &project {
            memory.push(MemoryGroup::new(
                "Organization",
                self.store
                    .list_memory_by_scope(&MemoryScope::Organization(p.organization_id))
                    .await?,
            ));
        }
        memory.push(MemoryGroup::new(
            format!("Repository {}/{}", repo.owner, repo.name),
            self.store
                .list_memory_by_scope(&MemoryScope::Repository(repo.id))
                .await?,
        ));
        memory.push(MemoryGroup::new(
            project
                .as_ref()
                .map(|p| format!("Project {}", p.slug))
                .unwrap_or_else(|| "Project".to_string()),
            self.store
                .list_memory_by_scope(&MemoryScope::Project(task.project_id))
                .await?,
        ));

        let org = match &project {
            Some(p) => self.store.get_organization(p.organization_id).await?,
            None => None,
        }
        .ok_or_else(|| OrchestratorError::Config("task project has no organization".into()))?;

        let run_id = RunId::new();
        // `conversations.run_id` FKs to `runs`, which doesn't exist yet —
        // link it after the run row is inserted.
        let mut conversation = self
            .sessions
            .open_conversation(OpenConversation {
                organization_id: Some(org.id),
                project_id: Some(task.project_id),
                title: Some(format!("run: {}", task.title)),
                model_profile_id: Some(profile.id),
                kind: ConversationKind::Run,
                activity,
                run_id: None,
                research_id: None,
                task_id: Some(task.id),
                repository_id: Some(repo.id),
                workdir: Some(cwd.clone()),
                // Project agents inherit the org coordinator's ReadOnly
                // policy — implementers run unattended and must be able to
                // write in their worktree, so Runs always override to Auto.
                permission_policy: Some(PermissionPolicy::Auto),
            })
            .await?;

        let prompt = run_prompt(&RunContext {
            org,
            project,
            repository: repo,
            worktree: cwd.clone(),
            branch: branch.clone(),
            parents,
            memory,
            task: task.clone(),
        });

        let run = Run {
            id: run_id,
            task_id: task.id,
            agent_id: agent.id,
            activity,
            model_profile_id: profile.id,
            harness_id: harness.id,
            repository_id: Some(repo_id),
            conversation_id: Some(conversation.id),
            worktree_path: Some(cwd),
            branch: Some(branch),
            acp_session_id: conversation.acp_session_id.clone(),
            status: RunStatus::Running,
            summary: None,
            started_at: chrono::Utc::now(),
            finished_at: None,
        };
        self.store.insert_run(&run).await?;
        conversation.run_id = Some(run.id);
        self.store.update_conversation(&conversation).await?;
        (self.emit)(DomainEvent::RunStarted { run: run.clone() });

        let sessions = self.sessions.clone();
        let store = self.store.clone();
        let emit = self.emit.clone();
        let conv_id = conversation.id;
        let run_id = run.id;
        tokio::spawn(async move {
            let outcome = sessions.run_turn(conv_id, &prompt).await;
            let mut run = match store.get_run(run_id).await {
                Ok(Some(r)) => r,
                _ => return,
            };
            let mut task = match store.get_task(run.task_id).await {
                Ok(Some(t)) => t,
                _ => return,
            };
            match outcome {
                Ok(msg) => {
                    run.status = RunStatus::Succeeded;
                    run.summary = extract_summary(&msg);
                    task.status = TaskStatus::NeedsReview;
                }
                Err(e) => {
                    run.status = RunStatus::Failed;
                    run.summary = Some(e.to_string());
                    task.status = TaskStatus::Failed;
                }
            }
            run.finished_at = Some(chrono::Utc::now());
            task.updated_at = chrono::Utc::now();
            if let Err(e) = store.update_run(&run).await {
                warn!(error = %e, "failed to update run");
            }
            if let Err(e) = store.update_task(&task).await {
                warn!(error = %e, "failed to update task");
            }
            emit(DomainEvent::RunFinished { run });
            emit(DomainEvent::EntityChanged {
                kind: EntityKind::Task,
                id: task.id.to_string(),
            });
        });
        Ok(run)
    }

    /// Cancel a task: stop the live run conversation (if any) and transition
    /// the task to `Cancelled` when legal.
    pub async fn cancel_task(&self, task_id: TaskId) -> Result<Task, OrchestratorError> {
        let mut task = self
            .store
            .get_task(task_id)
            .await?
            .ok_or_else(|| OrchestratorError::NotFound(format!("task {task_id}")))?;
        for run in self.store.list_runs_by_task(task.id).await? {
            if !matches!(
                run.status,
                RunStatus::Succeeded | RunStatus::Failed | RunStatus::Cancelled
            ) {
                if let Some(conv_id) = run.conversation_id {
                    let _ = self.sessions.cancel(conv_id).await;
                }
                let mut run = run;
                run.status = RunStatus::Cancelled;
                run.finished_at = Some(chrono::Utc::now());
                self.store.update_run(&run).await?;
                (self.emit)(DomainEvent::RunFinished { run });
            }
        }
        if task.status.can_transition_to(&TaskStatus::Cancelled) {
            self.transition(&mut task, TaskStatus::Cancelled)?;
            self.store.update_task(&task).await?;
        }
        Ok(task)
    }
}

/// `## Summary` section content, else the last 2000 chars of the reply.
fn extract_summary(msg: &Message) -> Option<String> {
    let text = message_text(msg);
    let text = text.trim();
    if text.is_empty() {
        return None;
    }
    if let Some(start) = text.find("## Summary") {
        let rest = &text[start + "## Summary".len()..];
        let end = rest.find("\n## ").unwrap_or(rest.len());
        return Some(rest[..end].trim().to_string());
    }
    let tail: String = text
        .chars()
        .collect::<Vec<_>>()
        .into_iter()
        .rev()
        .take(2000)
        .collect::<Vec<_>>()
        .into_iter()
        .rev()
        .collect();
    Some(tail)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn org() -> Organization {
        Organization {
            id: OrganizationId::new(),
            name: "Org".into(),
            slug: "org".into(),
            created_at: chrono::Utc::now(),
        }
    }

    fn project(org_id: OrganizationId, slug: &str, keywords: &[&str]) -> Project {
        project_with_repos(org_id, slug, keywords, vec![RepositoryId::new()])
    }

    fn project_with_repos(
        org_id: OrganizationId,
        slug: &str,
        keywords: &[&str],
        repository_ids: Vec<RepositoryId>,
    ) -> Project {
        Project {
            id: ProjectId::new(),
            organization_id: org_id,
            repository_ids,
            name: slug.into(),
            slug: slug.into(),
            description: String::new(),
            scope: ProjectScope {
                paths: vec![],
                labels: vec![],
                keywords: keywords.iter().map(|s| s.to_string()).collect(),
            },
            status: ProjectStatus::Active,
            created_at: chrono::Utc::now(),
        }
    }

    fn signal(title: &str, repository_id: Option<RepositoryId>) -> Signal {
        Signal {
            id: SignalId::new(),
            source: SourceKind::Manual,
            kind: SignalKind::HumanRequest,
            repository_id,
            project_id: None,
            dedupe_key: "x".into(),
            title: title.into(),
            body: String::new(),
            payload: serde_json::Value::Null,
            occurred_at: chrono::Utc::now(),
            ingested_at: chrono::Utc::now(),
        }
    }

    #[test]
    fn routes_by_repository_hint_contains() {
        let org = org();
        let repo_a = RepositoryId::new();
        let repo_b = RepositoryId::new();
        let repo_unhinted = RepositoryId::new();
        // `core` hints at repo_a (among others); `ui` at repo_b.
        let projects = vec![
            project_with_repos(
                org.id,
                "core",
                &["harness"],
                vec![repo_a, RepositoryId::new()],
            ),
            project_with_repos(org.id, "ui", &["css"], vec![repo_b]),
        ];
        // Repo_b narrows to exactly `ui` — the single candidate wins even
        // though the keyword matches only `core`.
        let s = signal("harness option broken", Some(repo_b));
        assert_eq!(
            route_signal(&s, &projects).map(|p| p.slug.as_str()),
            Some("ui")
        );
        // Repo_a narrows to `core`; keyword agrees.
        let s = signal("harness option broken", Some(repo_a));
        assert_eq!(
            route_signal(&s, &projects).map(|p| p.slug.as_str()),
            Some("core")
        );
        // A repo no project hints at → content routing fallback.
        let s = signal("harness option broken", Some(repo_unhinted));
        assert_eq!(
            route_signal(&s, &projects).map(|p| p.slug.as_str()),
            Some("core")
        );
        // No repo → keyword routing.
        let s = signal("harness option broken", None);
        assert_eq!(
            route_signal(&s, &projects).map(|p| p.slug.as_str()),
            Some("core")
        );
        // No keyword, ambiguous pool → nothing.
        let s = signal("unrelated", None);
        assert!(route_signal(&s, &projects).is_none());
    }

    async fn fixture() -> (
        Arc<InMemoryStore>,
        Organization,
        Project,
        Agent,
        Dispatcher<InMemoryStore>,
    ) {
        let store = Arc::new(InMemoryStore::new());
        let emit: EmitFn = Arc::new(|_| {});
        let sessions = Arc::new(SessionManager::new(
            store.clone(),
            emit.clone(),
            PathBuf::from("/tmp/data"),
        ));
        let dispatcher = Dispatcher::new(store.clone(), emit, sessions, PathBuf::from("/tmp/wt"));
        let org = org();
        store.insert_organization(&org).await.expect("org");
        let org_agent = Agent {
            id: AgentId::new(),
            name: "mobius".into(),
            role: AgentRole::Organization,
            organization_id: org.id,
            profiles: ActivityProfiles {
                default: ModelProfileId::new(),
                overrides: Default::default(),
            },
            project_id: None,
            repository_id: None,
            instructions: String::new(),
            permission_policy: PermissionPolicy::ReadOnly,
            status: AgentStatus::Idle,
            created_at: chrono::Utc::now(),
        };
        store.insert_agent(&org_agent).await.expect("org agent");
        let project = project(org.id, "core", &["harness"]);
        store.insert_project(&project).await.expect("p");
        let agent = Agent {
            id: AgentId::new(),
            name: "dev".into(),
            role: AgentRole::Project,
            organization_id: org.id,
            profiles: ActivityProfiles {
                default: ModelProfileId::new(),
                overrides: Default::default(),
            },
            project_id: Some(project.id),
            repository_id: None,
            instructions: String::new(),
            permission_policy: PermissionPolicy::Auto,
            status: AgentStatus::Idle,
            created_at: chrono::Utc::now(),
        };
        store.insert_agent(&agent).await.expect("a");
        (store, org, project, agent, dispatcher)
    }

    fn task_spec(project_id: ProjectId) -> CreateTaskSpec {
        CreateTaskSpec {
            project_id,
            repository_id: None,
            title: "do the thing".into(),
            description: "details".into(),
            kind: TaskKind::Feature,
            parent_task_id: None,
            priority: Priority::Normal,
            origin_conversation_id: None,
        }
    }

    #[tokio::test]
    async fn handle_signal_creates_task_with_repository() {
        let (store, _org, project, agent, dispatcher) = fixture().await;
        let repo_id = project.repository_ids[0];
        let task = dispatcher
            .handle_signal(&signal("harness fix", Some(repo_id)))
            .await
            .expect("ok")
            .expect("task");
        assert_eq!(task.status, TaskStatus::Proposed);
        assert_eq!(task.agent_id, agent.id);
        assert_eq!(task.repository_id, Some(repo_id));
        assert_eq!(task.kind, TaskKind::Triage);
        let _ = store;
    }

    #[tokio::test]
    async fn create_task_validates_and_links_parent() {
        let (store, _org, project, _agent, dispatcher) = fixture().await;
        // Missing project → NotFound.
        let err = dispatcher
            .create_task(task_spec(ProjectId::new()))
            .await
            .expect_err("missing project");
        assert!(matches!(err, OrchestratorError::NotFound(_)));

        // Triage is reserved for signal routing.
        let mut spec = task_spec(project.id);
        spec.kind = TaskKind::Triage;
        let err = dispatcher.create_task(spec).await.expect_err("triage");
        assert!(matches!(err, OrchestratorError::Config(_)));

        // Missing repository → NotFound.
        let mut spec = task_spec(project.id);
        spec.repository_id = Some(RepositoryId::new());
        let err = dispatcher
            .create_task(spec)
            .await
            .expect_err("missing repo");
        assert!(matches!(err, OrchestratorError::NotFound(_)));

        // Parent in the same project → ok; the child links it.
        let parent = dispatcher
            .create_task(task_spec(project.id))
            .await
            .expect("parent");
        let mut spec = task_spec(project.id);
        spec.parent_task_id = Some(parent.id);
        spec.origin_conversation_id = Some(ConversationId::new());
        let child = dispatcher.create_task(spec).await.expect("child");
        assert_eq!(child.parent_task_id, Some(parent.id));

        // Parent in a different project → Config error.
        let other_project = project_with_repos(project.organization_id, "other", &[], vec![]);
        store.insert_project(&other_project).await.expect("other");
        let foreign_parent = {
            let mut s = task_spec(other_project.id);
            s.title = "foreign parent".into();
            dispatcher.create_task(s).await.expect("foreign")
        };
        let mut spec = task_spec(project.id);
        spec.parent_task_id = Some(foreign_parent.id);
        let err = dispatcher
            .create_task(spec)
            .await
            .expect_err("cross-project parent");
        assert!(matches!(err, OrchestratorError::Config(_)), "got {err:?}");
    }

    #[tokio::test]
    async fn start_task_refuses_without_repository() {
        let (_store, _org, project, _agent, dispatcher) = fixture().await;
        let task = dispatcher
            .create_task(task_spec(project.id))
            .await
            .expect("task");
        let err = dispatcher.start_task(task.id).await.expect_err("no repo");
        assert!(matches!(err, OrchestratorError::Config(_)), "got {err:?}");
    }

    #[test]
    fn summary_extracts_section_or_tail() {
        let cid = ConversationId::new();
        let msg = |text: &str| Message {
            id: MessageId::new(),
            conversation_id: cid,
            author: MessageAuthor::Agent,
            blocks: vec![ContentBlock::Text { text: text.into() }],
            created_at: chrono::Utc::now(),
        };
        let m = msg("intro\n\n## Summary\n\nadded the file\n\n## Other\n\nx");
        assert_eq!(extract_summary(&m).as_deref(), Some("added the file"));
        let m = msg(&"y".repeat(5000));
        assert_eq!(extract_summary(&m).map(|s| s.len()), Some(2000));
    }
}
