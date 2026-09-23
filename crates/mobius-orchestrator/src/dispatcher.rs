//! Signal → Task routing and the thin `run_task` driver.

use crate::error::OrchestratorError;
use crate::prompt::PromptBuilder;
use crate::resolver::ProfileResolver;
use crate::worktree::WorktreeManager;
use mobius_core::*;
use mobius_harness::{HarnessSession, SessionUpdate, SpawnSpec};
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::Arc;
use tokio::sync::Mutex;
use tracing::{info, warn};

pub type EmitFn = Arc<dyn Fn(DomainEvent) + Send + Sync>;

/// Simple contains-match routing: a project wins when one of its scope
/// keywords or labels appears (case-insensitive) in the signal title/body.
/// Signals already carrying `project_id` short-circuit; a `repository_id`
/// narrows candidates first.
pub fn route_signal<'a>(signal: &Signal, projects: &'a [Project]) -> Option<&'a Project> {
    let haystack = format!("{}\n{}", signal.title, signal.body).to_lowercase();
    let by_repo: Vec<&Project> = projects
        .iter()
        .filter(|p| signal.repository_id.is_none_or(|r| r == p.repository_id))
        .filter(|p| p.status == ProjectStatus::Active)
        .collect();
    let pool: &[&Project] = if by_repo.is_empty() { &[] } else { &by_repo };
    pool.iter()
        .find(|p| {
            p.scope
                .keywords
                .iter()
                .chain(p.scope.labels.iter())
                .any(|k| !k.is_empty() && haystack.contains(&k.to_lowercase()))
        })
        .copied()
        // Fall back to an explicit project match on title/body even when the
        // repo filter narrowed nothing.
        .or_else(|| {
            projects
                .iter()
                .filter(|p| p.status == ProjectStatus::Active)
                .find(|p| {
                    p.scope
                        .keywords
                        .iter()
                        .chain(p.scope.labels.iter())
                        .any(|k| !k.is_empty() && haystack.contains(&k.to_lowercase()))
                })
        })
}

pub struct Dispatcher<S: Store> {
    store: Arc<S>,
    emit: EmitFn,
    worktrees: WorktreeManager,
    /// Live run sessions so `POST /permissions/{id}` can resolve run-time
    /// permission requests (AskHuman runs park until a human answers).
    run_sessions: Mutex<HashMap<RunId, Arc<HarnessSession>>>,
}

impl<S: Store + 'static> Dispatcher<S> {
    pub fn new(store: Arc<S>, emit: EmitFn, worktree_dir: PathBuf) -> Self {
        Self {
            store,
            emit,
            worktrees: WorktreeManager::new(worktree_dir),
            run_sessions: Mutex::new(HashMap::new()),
        }
    }

    pub async fn run_session(&self, run_id: RunId) -> Option<Arc<HarnessSession>> {
        self.run_sessions.lock().await.get(&run_id).cloned()
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
        let agents = self.store.list_agents_by_project(project.id).await?;
        let Some(agent) = agents.iter().find(|a| a.role == AgentRole::Project) else {
            warn!(project = %project.slug, "project has no Project agent");
            return Ok(None);
        };
        let now = chrono::Utc::now();
        let task = Task {
            id: TaskId::new(),
            project_id: project.id,
            agent_id: agent.id,
            parent_task_id: None,
            title: signal.title.clone(),
            description: signal.body.clone(),
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

    /// Drive a Proposed/Approved/Queued task through a single harness run.
    pub async fn run_task(&self, task_id: TaskId) -> Result<Run, OrchestratorError> {
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
        let agent = self
            .store
            .get_agent(task.agent_id)
            .await?
            .ok_or_else(|| OrchestratorError::NotFound("agent".into()))?;
        let activity = Activity::from(task.kind);
        let (profile, harness) =
            ProfileResolver::resolve(self.store.as_ref(), &agent, activity).await?;
        let project = self.store.get_project(task.project_id).await?;
        let repo = match &project {
            Some(p) => self.store.get_repository(p.repository_id).await?,
            None => None,
        };

        let branch = format!("mobius/task-{}", &task.id.to_string()[..8]);
        let cwd = match repo.as_ref().and_then(|r| r.local_path.clone()) {
            Some(local) => match self.worktrees.create(&local, &branch).await {
                Ok(p) => p,
                Err(e) => {
                    warn!("worktree create failed ({e}); running in repo checkout");
                    local
                }
            },
            None => std::env::temp_dir(),
        };

        // Memory for the prompt: project scope, then repo, then org.
        let mut memory = self
            .store
            .list_memory_by_scope(&MemoryScope::Project(task.project_id))
            .await?;
        if let Some(p) = &project {
            memory.extend(
                self.store
                    .list_memory_by_scope(&MemoryScope::Repository(p.repository_id))
                    .await?,
            );
        }
        let prompt = PromptBuilder::build(&agent.instructions, &memory, &task);

        let run = Run {
            id: RunId::new(),
            task_id: task.id,
            agent_id: agent.id,
            activity,
            model_profile_id: profile.id,
            harness_id: harness.id,
            worktree_path: Some(cwd.clone()),
            branch: Some(branch),
            acp_session_id: None,
            status: RunStatus::Starting,
            summary: None,
            started_at: chrono::Utc::now(),
            finished_at: None,
        };
        self.store.insert_run(&run).await?;
        (self.emit)(DomainEvent::RunStarted { run: run.clone() });

        let result = self
            .execute_run(&run, &harness, &profile, &agent, cwd, prompt)
            .await;
        match result {
            Ok(summary) => {
                self.finish_run(&run.id, RunStatus::Succeeded, Some(summary))
                    .await?;
                self.transition(&mut task, TaskStatus::NeedsReview)?;
                self.store.update_task(&task).await?;
            }
            Err(e) => {
                self.finish_run(&run.id, RunStatus::Failed, Some(e.to_string()))
                    .await?;
                let _ = self.transition(&mut task, TaskStatus::Failed);
                let _ = self.store.update_task(&task).await;
                return Err(e);
            }
        }
        self.store
            .get_run(run.id)
            .await?
            .ok_or_else(|| OrchestratorError::NotFound("run".into()))
    }

    async fn execute_run(
        &self,
        run: &Run,
        harness: &Harness,
        profile: &ModelProfile,
        agent: &Agent,
        cwd: PathBuf,
        prompt: String,
    ) -> Result<String, OrchestratorError> {
        let session = Arc::new(
            HarnessSession::spawn(SpawnSpec {
                harness: harness.clone(),
                cwd,
                policy: agent.permission_policy,
                profile: Some(profile.clone()),
            })
            .await?,
        );
        self.run_sessions
            .lock()
            .await
            .insert(run.id, session.clone());
        let mut rx = session.updates();
        let store = self.store.clone();
        let emit = self.emit.clone();
        let run_id = run.id;
        tokio::spawn(async move {
            while let Ok(update) = rx.recv().await {
                if let SessionUpdate::PermissionRequested {
                    request_id,
                    tool_call,
                    options,
                } = update
                {
                    let req = PermissionRequest {
                        id: request_id
                            .parse()
                            .unwrap_or_else(|_| PermissionRequestId::new()),
                        conversation_id: None,
                        run_id: Some(run_id),
                        tool_call,
                        options,
                        status: PermissionRequestStatus::Pending,
                        created_at: chrono::Utc::now(),
                        resolved_at: None,
                    };
                    if let Err(e) = store.insert_permission_request(&req).await {
                        warn!("persist permission request: {e}");
                    }
                    emit(DomainEvent::PermissionRequested { request: req });
                    emit(DomainEvent::RunUpdated {
                        run_id,
                        status: RunStatus::AwaitingPermission,
                        message: None,
                    });
                }
            }
        });

        let outcome = session.prompt(&prompt).await;
        self.run_sessions.lock().await.remove(&run.id);
        session.close().await;
        outcome.map_err(OrchestratorError::Harness)
    }

    async fn finish_run(
        &self,
        run_id: &RunId,
        status: RunStatus,
        summary: Option<String>,
    ) -> Result<(), OrchestratorError> {
        let Some(mut run) = self.store.get_run(*run_id).await? else {
            return Ok(());
        };
        run.status = status;
        run.summary = summary;
        run.finished_at = Some(chrono::Utc::now());
        self.store.update_run(&run).await?;
        (self.emit)(DomainEvent::RunFinished { run });
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex as StdMutex;

    #[test]
    fn routes_by_keyword() {
        let repo_id = RepositoryId::new();
        let mk = |slug: &str, keywords: &[&str]| Project {
            id: ProjectId::new(),
            repository_id: repo_id,
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
        };
        let projects = vec![mk("core", &["harness", "acp"]), mk("ui", &["css", "page"])];
        let signal = Signal {
            id: SignalId::new(),
            source: SourceKind::Manual,
            kind: SignalKind::HumanRequest,
            repository_id: None,
            project_id: None,
            dedupe_key: "x".into(),
            title: "harness option matching broken".into(),
            body: String::new(),
            payload: serde_json::Value::Null,
            occurred_at: chrono::Utc::now(),
            ingested_at: chrono::Utc::now(),
        };
        let hit = route_signal(&signal, &projects).expect("route");
        assert_eq!(hit.slug, "core");
        let miss = Signal {
            title: "nothing matches".into(),
            ..signal
        };
        assert!(route_signal(&miss, &projects).is_none());
    }

    #[tokio::test]
    async fn handle_signal_creates_task() {
        let store = Arc::new(InMemoryStore::new());
        let events = Arc::new(StdMutex::new(Vec::new()));
        let ev = events.clone();
        let emit: EmitFn = Arc::new(move |e| {
            if let Ok(mut v) = ev.lock() {
                v.push(e);
            }
        });
        let repo_id = RepositoryId::new();
        let project = Project {
            id: ProjectId::new(),
            repository_id: repo_id,
            name: "core".into(),
            slug: "core".into(),
            description: String::new(),
            scope: ProjectScope {
                paths: vec![],
                labels: vec![],
                keywords: vec!["harness".into()],
            },
            status: ProjectStatus::Active,
            created_at: chrono::Utc::now(),
        };
        store.insert_project(&project).await.expect("p");
        let agent = Agent {
            id: AgentId::new(),
            name: "dev".into(),
            role: AgentRole::Project,
            profiles: ActivityProfiles {
                default: ModelProfileId::new(),
                overrides: Default::default(),
            },
            project_id: Some(project.id),
            repository_id: Some(repo_id),
            instructions: String::new(),
            permission_policy: PermissionPolicy::Auto,
            status: AgentStatus::Idle,
            created_at: chrono::Utc::now(),
        };
        store.insert_agent(&agent).await.expect("a");
        let dispatcher = Dispatcher::new(store.clone(), emit, PathBuf::from("/tmp/wt"));
        let signal = Signal {
            id: SignalId::new(),
            source: SourceKind::Manual,
            kind: SignalKind::HumanRequest,
            repository_id: Some(repo_id),
            project_id: None,
            dedupe_key: "k".into(),
            title: "harness fix".into(),
            body: "please".into(),
            payload: serde_json::Value::Null,
            occurred_at: chrono::Utc::now(),
            ingested_at: chrono::Utc::now(),
        };
        let task = dispatcher
            .handle_signal(&signal)
            .await
            .expect("ok")
            .expect("task");
        assert_eq!(task.status, TaskStatus::Proposed);
        assert_eq!(task.agent_id, agent.id);
        let created = events.lock().expect("l");
        assert!(
            created
                .iter()
                .any(|e| matches!(e, DomainEvent::TaskCreated { .. }))
        );
    }
}
