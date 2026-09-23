//! Conversation sessions: long-lived `HarnessSession`s keyed by
//! `Conversation`, streaming updates persisted as `Message` blocks and
//! forwarded as `DomainEvent`s. Covers human-facing chats (`kind=chat`,
//! always run in the org memory dir) and run/research transcript
//! conversations.

use crate::agents::AgentProvisioner;
use crate::dispatcher::EmitFn;
use crate::error::OrchestratorError;
use crate::prompt::{ChatContext, MemoryGroup, chat_preamble, updates_block};
use crate::resolver::ProfileResolver;
use mobius_core::*;
use mobius_harness::{HarnessSession, SessionUpdate, SpawnSpec};
use std::collections::{BTreeMap, HashMap};
use std::path::PathBuf;
use std::sync::Arc;
use tokio::sync::Mutex;
use tracing::warn;

/// Specification for [`SessionManager::open_conversation`].
pub struct OpenConversation {
    /// Explicit org; otherwise derived from `project_id`, else the single
    /// organization, else a `Config` error.
    pub organization_id: Option<OrganizationId>,
    pub project_id: Option<ProjectId>,
    pub title: Option<String>,
    /// Profile override; defaults to the agent's profile for `activity`.
    pub model_profile_id: Option<ModelProfileId>,
    pub kind: ConversationKind,
    pub activity: Activity,
    /// Linkage for run/research conversations (+ `MOBIUS_RUN` /
    /// `MOBIUS_RESEARCH` env).
    pub run_id: Option<RunId>,
    pub research_id: Option<ResearchId>,
    /// `MOBIUS_TASK` env (task runs).
    pub task_id: Option<TaskId>,
    /// Repository the conversation is bound to (run/research transcripts).
    pub repository_id: Option<RepositoryId>,
    /// Working directory — required for Run/Research; ignored for Chat
    /// (chats always run in the org memory dir).
    pub workdir: Option<PathBuf>,
    /// Policy override (research runs ReadOnly); defaults to the agent's.
    pub permission_policy: Option<PermissionPolicy>,
}

impl OpenConversation {
    /// A human-facing chat, optionally scoped to a project.
    pub fn chat(project_id: Option<ProjectId>) -> Self {
        Self {
            organization_id: None,
            project_id,
            title: None,
            model_profile_id: None,
            kind: ConversationKind::Chat,
            activity: Activity::Chat,
            run_id: None,
            research_id: None,
            task_id: None,
            repository_id: None,
            workdir: None,
            permission_policy: None,
        }
    }
}

pub struct SessionManager<S: Store> {
    store: Arc<S>,
    emit: EmitFn,
    data_dir: PathBuf,
    /// Directory containing the `mobius` CLI binary, prepended to agent
    /// `PATH`. Defaults to the directory of `current_exe()`.
    pub cli_bin_dir: Option<PathBuf>,
    /// Base URL agents use to reach this server (`MOBIUS_URL`).
    pub mobius_url: String,
    sessions: Mutex<HashMap<ConversationId, Arc<HarnessSession>>>,
}

fn default_cli_bin_dir() -> Option<PathBuf> {
    std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|d| d.to_path_buf()))
}

impl<S: Store + 'static> SessionManager<S> {
    pub fn new(store: Arc<S>, emit: EmitFn, data_dir: PathBuf) -> Self {
        Self {
            store,
            emit,
            data_dir,
            cli_bin_dir: default_cli_bin_dir(),
            mobius_url: "http://127.0.0.1:8787".to_string(),
            sessions: Mutex::new(HashMap::new()),
        }
    }

    /// Live session for a conversation, if one exists (process-lifetime only).
    pub async fn live_session(&self, id: ConversationId) -> Option<Arc<HarnessSession>> {
        self.sessions.lock().await.get(&id).cloned()
    }

    async fn session(&self, id: ConversationId) -> Result<Arc<HarnessSession>, OrchestratorError> {
        self.sessions.lock().await.get(&id).cloned().ok_or_else(|| {
            OrchestratorError::NotLive(format!(
                "conversation {id} has no live session (server restart?)"
            ))
        })
    }

    /// `MOBIUS_*` environment for an agent process.
    fn mobius_env(
        &self,
        org: &Organization,
        project: Option<&Project>,
        conversation_id: ConversationId,
    ) -> BTreeMap<String, String> {
        let mut env = BTreeMap::new();
        env.insert("MOBIUS_URL".into(), self.mobius_url.clone());
        env.insert("MOBIUS_ORG".into(), org.slug.clone());
        if let Some(p) = project {
            env.insert("MOBIUS_PROJECT".into(), p.slug.clone());
        }
        env.insert("MOBIUS_CONVERSATION".into(), conversation_id.to_string());
        env
    }

    /// Env for an existing conversation row (re-attach): `MOBIUS_TASK` is
    /// recovered through `run_id` → task when present.
    async fn conversation_env(
        &self,
        conversation: &Conversation,
    ) -> Result<BTreeMap<String, String>, OrchestratorError> {
        let org = self
            .store
            .get_organization(conversation.organization_id)
            .await?
            .ok_or_else(|| {
                OrchestratorError::Config(format!(
                    "conversation {} references missing organization",
                    conversation.id
                ))
            })?;
        let project = match conversation.project_id {
            Some(id) => self.store.get_project(id).await?,
            None => None,
        };
        let mut env = self.mobius_env(&org, project.as_ref(), conversation.id);
        if let Some(run_id) = conversation.run_id {
            env.insert("MOBIUS_RUN".into(), run_id.to_string());
            if let Some(run) = self.store.get_run(run_id).await? {
                env.insert("MOBIUS_TASK".into(), run.task_id.to_string());
            }
        }
        if let Some(research_id) = conversation.research_id {
            env.insert("MOBIUS_RESEARCH".into(), research_id.to_string());
        }
        Ok(env)
    }

    /// Live session if one exists; otherwise spawn a fresh ACP session for
    /// the persisted conversation (sessions are process-lifetime, so every
    /// restart leaves them orphaned) and update the row. Returns
    /// `(session, reattached)` — callers use the flag to prepend a resume
    /// preamble to the first prompt of the new session.
    async fn session_or_reattach(
        &self,
        id: ConversationId,
    ) -> Result<(Arc<HarnessSession>, bool), OrchestratorError> {
        if let Some(session) = self.live_session(id).await {
            return Ok((session, false));
        }
        let conversation = self
            .store
            .get_conversation(id)
            .await?
            .ok_or_else(|| OrchestratorError::NotFound(format!("conversation {id}")))?;
        if conversation.status == ConversationStatus::Closed {
            return Err(OrchestratorError::NotLive(format!(
                "conversation {id} is closed"
            )));
        }
        let agent = self
            .store
            .get_agent(conversation.agent_id)
            .await?
            .ok_or_else(|| {
                OrchestratorError::NotFound(format!("agent {}", conversation.agent_id))
            })?;
        let (profile, harness) =
            ProfileResolver::resolve(self.store.as_ref(), &agent, conversation.activity).await?;
        let extra_env = self.conversation_env(&conversation).await?;
        let session = Arc::new(
            HarnessSession::spawn(SpawnSpec {
                harness,
                cwd: conversation.workdir.clone(),
                policy: agent.permission_policy,
                profile: Some(profile),
                extra_env,
                cli_bin_dir: self.cli_bin_dir.clone(),
            })
            .await?,
        );
        self.sessions.lock().await.insert(id, session.clone());
        let mut c = conversation;
        c.acp_session_id = Some(session.session_id());
        c.config_options = session.config_options();
        c.updated_at = chrono::Utc::now();
        self.store.update_conversation(&c).await?;
        (self.emit)(DomainEvent::ConversationConfigChanged {
            conversation_id: id,
            config_options: c.config_options.clone(),
        });
        tracing::info!(%id, "re-attached conversation with a fresh ACP session");
        Ok((session, true))
    }

    /// Reset conversations stuck in `Streaming`/`AwaitingPermission` by a
    /// previous process lifetime to `Idle`. Runs once at server startup.
    pub async fn reset_stale_statuses(&self) {
        let Ok(conversations) = self.store.list_conversations().await else {
            return;
        };
        for c in conversations {
            if matches!(
                c.status,
                ConversationStatus::Streaming | ConversationStatus::AwaitingPermission
            ) {
                let mut c = c;
                c.status = ConversationStatus::Idle;
                c.updated_at = chrono::Utc::now();
                if self.store.update_conversation(&c).await.is_ok() {
                    (self.emit)(DomainEvent::ConversationStatusChanged {
                        conversation_id: c.id,
                        status: ConversationStatus::Idle,
                    });
                }
            }
        }
    }

    /// Organization resolution: explicitly supplied → from project → the
    /// single organization → `Config` error.
    async fn resolve_organization(
        &self,
        organization_id: Option<OrganizationId>,
        project: Option<&Project>,
    ) -> Result<Organization, OrchestratorError> {
        if let Some(id) = organization_id {
            return self
                .store
                .get_organization(id)
                .await?
                .ok_or_else(|| OrchestratorError::NotFound(format!("organization {id}")));
        }
        if let Some(p) = project {
            return self
                .store
                .get_organization(p.organization_id)
                .await?
                .ok_or_else(|| {
                    OrchestratorError::Config(format!(
                        "project {:?} references missing organization",
                        p.slug
                    ))
                });
        }
        let orgs = self.store.list_organizations().await?;
        match orgs.as_slice() {
            [single] => Ok(single.clone()),
            _ => Err(OrchestratorError::Config(format!(
                "cannot resolve organization ({} registered); specify one",
                orgs.len()
            ))),
        }
    }

    /// Open a conversation: resolve org + coordinator agent (via the
    /// provisioner), spawn the harness session with `MOBIUS_*` env and the
    /// CLI bin dir on PATH, persist the conversation.
    pub async fn open_conversation(
        &self,
        spec: OpenConversation,
    ) -> Result<Conversation, OrchestratorError> {
        let project = match spec.project_id {
            Some(id) => Some(
                self.store
                    .get_project(id)
                    .await?
                    .ok_or_else(|| OrchestratorError::NotFound(format!("project {id}")))?,
            ),
            None => None,
        };
        let org = self
            .resolve_organization(spec.organization_id, project.as_ref())
            .await?;
        if let Some(p) = &project
            && p.organization_id != org.id
        {
            return Err(OrchestratorError::Config(format!(
                "project {:?} does not belong to organization {:?}",
                p.slug, org.slug
            )));
        }
        let agent = match &project {
            Some(p) => AgentProvisioner::ensure_project_agent(self.store.as_ref(), p).await?,
            None => AgentProvisioner::ensure_org_agent(self.store.as_ref(), &org).await?,
        };
        let activity = spec.activity;
        let (profile, harness) = match spec.model_profile_id {
            Some(id) => {
                let profile =
                    self.store.get_model_profile(id).await?.ok_or_else(|| {
                        OrchestratorError::NotFound(format!("model profile {id}"))
                    })?;
                let harness = self
                    .store
                    .get_harness(profile.harness_id)
                    .await?
                    .ok_or_else(|| {
                        OrchestratorError::NotFound(format!("harness {}", profile.harness_id))
                    })?;
                if !harness.enabled {
                    return Err(OrchestratorError::Config(format!(
                        "profile {:?} references disabled harness {:?}",
                        profile.name, harness.name
                    )));
                }
                (profile, harness)
            }
            None => ProfileResolver::resolve(self.store.as_ref(), &agent, activity).await?,
        };

        let conversation_id = ConversationId::new();
        let cwd = match spec.kind {
            ConversationKind::Chat => {
                let dir = memory_paths::org_dir(&self.data_dir, &org.slug);
                std::fs::create_dir_all(&dir)?;
                dir
            }
            _ => spec.workdir.ok_or_else(|| {
                OrchestratorError::Config(
                    "run/research conversations require a workdir".to_string(),
                )
            })?,
        };

        let mut extra_env = self.mobius_env(&org, project.as_ref(), conversation_id);
        if let Some(t) = spec.task_id {
            extra_env.insert("MOBIUS_TASK".into(), t.to_string());
        }
        if let Some(r) = spec.run_id {
            extra_env.insert("MOBIUS_RUN".into(), r.to_string());
        }
        if let Some(r) = spec.research_id {
            extra_env.insert("MOBIUS_RESEARCH".into(), r.to_string());
        }

        let session = Arc::new(
            HarnessSession::spawn(SpawnSpec {
                harness,
                cwd: cwd.clone(),
                policy: spec.permission_policy.unwrap_or(agent.permission_policy),
                profile: Some(profile.clone()),
                extra_env,
                cli_bin_dir: self.cli_bin_dir.clone(),
            })
            .await?,
        );
        let now = chrono::Utc::now();
        let conversation = Conversation {
            id: conversation_id,
            agent_id: agent.id,
            activity,
            model_profile_id: profile.id,
            organization_id: org.id,
            project_id: project.map(|p| p.id),
            kind: spec.kind,
            run_id: spec.run_id,
            research_id: spec.research_id,
            repository_id: spec.repository_id,
            workdir: cwd,
            title: spec.title.unwrap_or_else(|| "New chat".to_string()),
            acp_session_id: Some(session.session_id()),
            status: ConversationStatus::Idle,
            config_options: session.config_options(),
            created_at: now,
            updated_at: now,
        };
        self.store.insert_conversation(&conversation).await?;
        self.sessions.lock().await.insert(conversation.id, session);
        (self.emit)(DomainEvent::ConversationCreated {
            conversation: conversation.clone(),
        });
        Ok(conversation)
    }

    /// Assemble the `ChatContext` a coordinator preamble renders. No local
    /// repository paths ever reach the context's rendered output.
    async fn chat_context(
        &self,
        conversation: &Conversation,
    ) -> Result<ChatContext, OrchestratorError> {
        let org = self
            .store
            .get_organization(conversation.organization_id)
            .await?
            .ok_or_else(|| OrchestratorError::Config("missing organization".into()))?;
        let agent = self
            .store
            .get_agent(conversation.agent_id)
            .await?
            .ok_or_else(|| OrchestratorError::NotFound("agent".into()))?;
        let project = match conversation.project_id {
            Some(id) => self.store.get_project(id).await?,
            None => None,
        };
        let projects = self.store.list_projects_by_organization(org.id).await?;
        let org_repos: Vec<Repository> = self
            .store
            .list_repositories()
            .await?
            .into_iter()
            .filter(|r| r.organization_id == org.id)
            .collect();
        let repositories: Vec<Repository> = match &project {
            Some(p) => org_repos
                .iter()
                .filter(|r| p.repository_ids.contains(&r.id))
                .cloned()
                .collect(),
            None => org_repos,
        };

        // Memory groups: org → repository (owner/name only) → project.
        let mut memory = vec![MemoryGroup::new(
            "Organization",
            self.store
                .list_memory_by_scope(&MemoryScope::Organization(org.id))
                .await?,
        )];
        for r in &repositories {
            memory.push(MemoryGroup::new(
                format!("Repository {}/{}", r.owner, r.name),
                self.store
                    .list_memory_by_scope(&MemoryScope::Repository(r.id))
                    .await?,
            ));
        }
        if let Some(p) = &project {
            memory.push(MemoryGroup::new(
                format!("Project {}", p.slug),
                self.store
                    .list_memory_by_scope(&MemoryScope::Project(p.id))
                    .await?,
            ));
        }

        let memory_files = memory_files_in(&memory_paths::org_dir(&self.data_dir, &org.slug));

        let open_research: Vec<Research> = match &project {
            Some(p) => self
                .store
                .list_research_by_project(p.id)
                .await?
                .into_iter()
                .filter(|r| matches!(r.status, ResearchStatus::Pending | ResearchStatus::Running))
                .collect(),
            None => self
                .store
                .list_research()
                .await?
                .into_iter()
                .filter(|r| {
                    r.project_id.is_none()
                        && matches!(r.status, ResearchStatus::Pending | ResearchStatus::Running)
                })
                .collect(),
        };
        let open_tasks: Vec<Task> = match &project {
            Some(p) => self
                .store
                .list_tasks_by_project(p.id)
                .await?
                .into_iter()
                .filter(|t| {
                    !matches!(
                        t.status,
                        TaskStatus::Done | TaskStatus::Cancelled | TaskStatus::Failed
                    )
                })
                .collect(),
            None => vec![],
        };

        Ok(ChatContext {
            org,
            agent,
            project,
            projects,
            repositories,
            memory,
            memory_files,
            open_research,
            open_tasks,
        })
    }

    /// The "since your last message" block: research/tasks/runs originating
    /// from this conversation that changed after the last human message.
    async fn updates_block(
        &self,
        conversation: &Conversation,
        prior: &[Message],
    ) -> Result<Option<String>, OrchestratorError> {
        let since = prior
            .iter()
            .rev()
            .find(|m| m.author == MessageAuthor::Human)
            .map(|m| m.created_at);
        let changed_since = |a: chrono::DateTime<chrono::Utc>,
                             b: Option<chrono::DateTime<chrono::Utc>>| {
            match since {
                Some(s) => a > s || b.is_some_and(|b| b > s),
                None => false,
            }
        };
        let research: Vec<Research> = self
            .store
            .list_research_by_origin_conversation(conversation.id)
            .await?
            .into_iter()
            .filter(|r| changed_since(r.created_at, r.finished_at))
            .collect();
        let tasks: Vec<Task> = self
            .store
            .list_tasks_by_origin_conversation(conversation.id)
            .await?
            .into_iter()
            .filter(|t| changed_since(t.updated_at, None))
            .collect();
        let mut runs = Vec::new();
        for t in &tasks {
            runs.extend(
                self.store
                    .list_runs_by_task(t.id)
                    .await?
                    .into_iter()
                    .filter(|r| changed_since(r.started_at, r.finished_at)),
            );
        }
        Ok(updates_block(&research, &tasks, &runs))
    }

    /// Persist the human message, then drive the turn in the background:
    /// stream `MessageDelta`s, persist agent blocks as they complete, park
    /// permissions, finalize on `TurnFinished`.
    pub async fn send_message(
        &self,
        conversation_id: ConversationId,
        text: &str,
    ) -> Result<Message, OrchestratorError> {
        let (session, reattached) = self.session_or_reattach(conversation_id).await?;
        let conversation = self
            .store
            .get_conversation(conversation_id)
            .await?
            .ok_or_else(|| {
                OrchestratorError::NotFound(format!("conversation {conversation_id}"))
            })?;
        let prior = self
            .store
            .list_messages_by_conversation(conversation_id)
            .await?;

        // Prompt composition (plan §3): chat preamble on the first turn of a
        // fresh session, resume preamble on re-attach, the updates block, and
        // finally the raw text.
        let mut parts: Vec<String> = Vec::new();
        let is_chat = conversation.kind == ConversationKind::Chat;
        let first_turn = !prior.iter().any(|m| m.author == MessageAuthor::Human);
        if is_chat && (reattached || first_turn) {
            let ctx = self.chat_context(&conversation).await?;
            parts.push(chat_preamble(&ctx));
        }
        if reattached {
            parts.push(build_resume_preamble(&prior));
        }
        if is_chat && let Some(updates) = self.updates_block(&conversation, &prior).await? {
            parts.push(updates);
        }
        parts.push(text.to_string());
        let prompt_text = parts.join("\n\n");

        // Auto-title the conversation from the first human message.
        if matches!(conversation.title.trim(), "" | "New chat") {
            let title: String = text.trim().chars().take(60).collect();
            if !title.is_empty() {
                let mut c = conversation.clone();
                c.title = title;
                c.updated_at = chrono::Utc::now();
                if self.store.update_conversation(&c).await.is_ok() {
                    (self.emit)(DomainEvent::EntityChanged {
                        kind: EntityKind::Conversation,
                        id: c.id.to_string(),
                    });
                }
            }
        }

        let human = Message {
            id: MessageId::new(),
            conversation_id,
            author: MessageAuthor::Human,
            blocks: vec![ContentBlock::Text {
                text: text.to_string(),
            }],
            created_at: chrono::Utc::now(),
        };
        self.store.insert_message(&human).await?;
        (self.emit)(DomainEvent::EntityChanged {
            kind: EntityKind::Message,
            id: human.id.to_string(),
        });
        set_status_now(
            &self.store,
            &self.emit,
            conversation_id,
            ConversationStatus::Streaming,
        )
        .await;

        // Agent message persisted up-front so MessageDelta has a stable id.
        let agent_msg = Message {
            id: MessageId::new(),
            conversation_id,
            author: MessageAuthor::Agent,
            blocks: vec![],
            created_at: chrono::Utc::now(),
        };
        self.store.insert_message(&agent_msg).await?;

        let store = self.store.clone();
        let emit = self.emit.clone();
        let rx = session.updates();
        let session_loop = session.clone();
        tokio::spawn(async move {
            drive_turn(
                store,
                emit,
                conversation_id,
                agent_msg,
                rx,
                session_loop.prompt(&prompt_text),
                TurnCtx {
                    human_message_id: Some(human.id),
                    done: None,
                },
            )
            .await;
        });
        Ok(human)
    }

    /// Send `prompt` to the conversation's session and wait for the turn to
    /// finish, returning the completed agent message. The prompt is persisted
    /// as a `System` message so the transcript stays readable. Used by
    /// research and task runs (the dispatcher/research service drive these).
    pub async fn run_turn(
        &self,
        conversation_id: ConversationId,
        prompt: &str,
    ) -> Result<Message, OrchestratorError> {
        let (session, reattached) = self.session_or_reattach(conversation_id).await?;
        let prompt_text = if reattached {
            let prior = self
                .store
                .list_messages_by_conversation(conversation_id)
                .await?;
            format!("{}\n\n{prompt}", build_resume_preamble(&prior))
        } else {
            prompt.to_string()
        };

        let sys = Message {
            id: MessageId::new(),
            conversation_id,
            author: MessageAuthor::System,
            blocks: vec![ContentBlock::Text {
                text: prompt.to_string(),
            }],
            created_at: chrono::Utc::now(),
        };
        self.store.insert_message(&sys).await?;
        (self.emit)(DomainEvent::EntityChanged {
            kind: EntityKind::Message,
            id: sys.id.to_string(),
        });
        set_status_now(
            &self.store,
            &self.emit,
            conversation_id,
            ConversationStatus::Streaming,
        )
        .await;

        let agent_msg = Message {
            id: MessageId::new(),
            conversation_id,
            author: MessageAuthor::Agent,
            blocks: vec![],
            created_at: chrono::Utc::now(),
        };
        self.store.insert_message(&agent_msg).await?;

        let (done_tx, done_rx) = tokio::sync::oneshot::channel();
        let store = self.store.clone();
        let emit = self.emit.clone();
        let rx = session.updates();
        let session_loop = session.clone();
        tokio::spawn(async move {
            drive_turn(
                store,
                emit,
                conversation_id,
                agent_msg,
                rx,
                session_loop.prompt(&prompt_text),
                TurnCtx {
                    human_message_id: None,
                    done: Some(done_tx),
                },
            )
            .await;
        });
        done_rx
            .await
            .map_err(|_| {
                OrchestratorError::Turn("turn driver ended without reporting a result".to_string())
            })?
            .map_err(OrchestratorError::Turn)
    }

    pub async fn resolve_permission(
        &self,
        permission_id: PermissionRequestId,
        option_id: &str,
    ) -> Result<(), OrchestratorError> {
        let mut req = self
            .store
            .get_permission_request(permission_id)
            .await?
            .ok_or_else(|| OrchestratorError::NotFound(format!("permission {permission_id}")))?;
        if let Some(conv_id) = req.conversation_id {
            let session = self.session(conv_id).await?;
            if !session.respond_permission(&permission_id.to_string(), option_id) {
                return Err(OrchestratorError::NotLive(format!(
                    "no parked permission {permission_id} on session"
                )));
            }
        }
        req.status = PermissionRequestStatus::Resolved {
            option_id: option_id.to_string(),
        };
        req.resolved_at = Some(chrono::Utc::now());
        self.store.update_permission_request(&req).await?;
        (self.emit)(DomainEvent::PermissionResolved {
            permission_request_id: permission_id,
            option_id: option_id.to_string(),
        });
        if let Some(conv_id) = req.conversation_id {
            set_status_now(
                &self.store,
                &self.emit,
                conv_id,
                ConversationStatus::Streaming,
            )
            .await;
        }
        Ok(())
    }

    pub async fn set_conversation_config(
        &self,
        conversation_id: ConversationId,
        config_id: &str,
        value: &str,
    ) -> Result<Vec<SessionConfigOption>, OrchestratorError> {
        let (session, _) = self.session_or_reattach(conversation_id).await?;
        let opts = session.set_config_option(config_id, value).await?;
        if let Some(mut c) = self.store.get_conversation(conversation_id).await? {
            c.config_options = opts.clone();
            c.updated_at = chrono::Utc::now();
            self.store.update_conversation(&c).await?;
        }
        (self.emit)(DomainEvent::ConversationConfigChanged {
            conversation_id,
            config_options: opts.clone(),
        });
        Ok(opts)
    }

    pub async fn cancel(&self, conversation_id: ConversationId) -> Result<(), OrchestratorError> {
        // No live session (e.g. after a restart): nothing to cancel — just
        // settle the status. Deliberately does NOT spawn a new session.
        if let Some(session) = self.live_session(conversation_id).await {
            session.cancel().await?;
        }
        set_status_now(
            &self.store,
            &self.emit,
            conversation_id,
            ConversationStatus::Idle,
        )
        .await;
        Ok(())
    }

    pub async fn close(&self, conversation_id: ConversationId) -> Result<(), OrchestratorError> {
        if let Some(session) = self.sessions.lock().await.remove(&conversation_id) {
            session.close().await;
        }
        set_status_now(
            &self.store,
            &self.emit,
            conversation_id,
            ConversationStatus::Closed,
        )
        .await;
        Ok(())
    }

    /// Close every live session (server shutdown).
    pub async fn close_all(&self) {
        let mut sessions = self.sessions.lock().await;
        for (_, session) in sessions.drain() {
            session.close().await;
        }
    }
}

/// Per-turn bookkeeping threaded through `drive_turn`/`apply_update`.
pub(crate) struct TurnCtx {
    /// The human message that opened the turn (`send_message`), for
    /// `DomainEvent::TurnFinished`. `None` for `run_turn` system prompts.
    pub human_message_id: Option<MessageId>,
    /// `run_turn` waiter, resolved exactly once when the turn ends.
    pub done: Option<tokio::sync::oneshot::Sender<Result<Message, String>>>,
}

impl TurnCtx {
    fn succeed(&mut self, msg: &Message) {
        if let Some(done) = self.done.take() {
            let _ = done.send(Ok(msg.clone()));
        }
    }

    fn fail(&mut self, error: String) {
        if let Some(done) = self.done.take() {
            let _ = done.send(Err(error));
        }
    }
}

/// One prompt turn: stream `SessionUpdate`s into `msg`, persist on every
/// completed block, emit `MessageUpdated`/`MessageDelta`/`MessageAppended`,
/// then return so the *next* turn's loop owns the broadcast stream. Returns
/// after `TurnFinished`, prompt error, `Exited`, or channel close.
pub(crate) async fn drive_turn<S: Store>(
    store: Arc<S>,
    emit: EmitFn,
    conversation_id: ConversationId,
    mut msg: Message,
    mut rx: tokio::sync::broadcast::Receiver<SessionUpdate>,
    prompt: impl std::future::Future<Output = Result<String, mobius_harness::HarnessError>>,
    mut ctx: TurnCtx,
) {
    tokio::pin!(prompt);
    let mut pending: Option<PendingChunk> = None;
    let mut prompt_done = false;

    loop {
        if prompt_done {
            // The prompt response is in; drain buffered updates up to
            // TurnFinished (finalize), then finalize anyway if it never came.
            let mut finished = false;
            while let Ok(update) = rx.try_recv() {
                if apply_update(
                    &store,
                    &emit,
                    conversation_id,
                    &mut msg,
                    &mut pending,
                    &mut ctx,
                    update,
                )
                .await
                {
                    finished = true;
                    break;
                }
            }
            if !finished {
                finalize(
                    &store,
                    &emit,
                    conversation_id,
                    &mut msg,
                    &mut pending,
                    &mut ctx,
                )
                .await;
            }
            return;
        }
        tokio::select! {
            update = rx.recv() => {
                match update {
                    Ok(u) => {
                        if apply_update(&store, &emit, conversation_id, &mut msg, &mut pending, &mut ctx, u).await {
                            return;
                        }
                    }
                    Err(tokio::sync::broadcast::error::RecvError::Closed) => {
                        ctx.fail("session update channel closed".to_string());
                        return;
                    }
                    Err(tokio::sync::broadcast::error::RecvError::Lagged(_)) => continue,
                }
            }
            outcome = &mut prompt => {
                prompt_done = true;
                if let Err(e) = outcome {
                    warn!("prompt failed: {e}");
                    flush_and_persist(&store, &emit, conversation_id, &mut msg, &mut pending).await;
                    msg.blocks.push(ContentBlock::Text {
                        text: format!("(error: {e})"),
                    });
                    let _ = store.update_message(&msg).await;
                    emit(DomainEvent::MessageAppended {
                        conversation_id,
                        message: msg.clone(),
                    });
                    set_status_now(&store, &emit, conversation_id, ConversationStatus::Idle).await;
                    ctx.fail(format!("prompt failed: {e}"));
                    return;
                }
            }
        }
    }
}

/// Transcript prefix sent to a freshly spawned ACP session so it can pick
/// up where a previous process lifetime left off. Only `Text` blocks are
/// rendered (thoughts/tool calls/plans are omitted); the last 40 messages
/// at most, capped at 16k chars by dropping the oldest messages first.
pub(crate) fn build_resume_preamble(messages: &[Message]) -> String {
    const MAX_MESSAGES: usize = 40;
    const MAX_CHARS: usize = 16_000;
    const HEADER: &str = "This conversation is being resumed after a restart; \
         the previous transcript follows. Continue from it without re-doing \
         completed work.";
    let mut lines: Vec<String> = messages[messages.len().saturating_sub(MAX_MESSAGES)..]
        .iter()
        .filter_map(|m| {
            let who = match m.author {
                MessageAuthor::Human => "Human",
                MessageAuthor::Agent => "Agent",
                MessageAuthor::System => "System",
            };
            let text = m
                .blocks
                .iter()
                .filter_map(|b| match b {
                    ContentBlock::Text { text } => Some(text.as_str()),
                    _ => None,
                })
                .collect::<Vec<_>>()
                .join("\n");
            (!text.trim().is_empty()).then(|| format!("{who}: {text}"))
        })
        .collect();
    while !lines.is_empty()
        && HEADER.len() + lines.iter().map(|l| l.len() + 1).sum::<usize>() > MAX_CHARS
    {
        lines.remove(0);
    }
    format!("{HEADER}\n\n{}", lines.join("\n"))
}

/// Streamed `AgentText`/`Thought` updates arrive as deltas; consecutive
/// chunks of the same kind merge into one block and a kind switch (or any
/// other update) flushes the pending chunk.
enum PendingChunk {
    Text(String),
    Thought(String),
}

/// Flush the in-flight text/thought chunk into a block and persist, emitting
/// `MessageUpdated` (which carries `conversation_id`, unlike `EntityChanged`).
/// No-op when nothing is pending, so callers can flush unconditionally.
async fn flush_and_persist<S: Store>(
    store: &Arc<S>,
    emit: &EmitFn,
    conversation_id: ConversationId,
    msg: &mut Message,
    pending: &mut Option<PendingChunk>,
) {
    match pending.take() {
        Some(PendingChunk::Text(text)) => msg.blocks.push(ContentBlock::Text { text }),
        Some(PendingChunk::Thought(text)) => msg.blocks.push(ContentBlock::Thought { text }),
        None => return,
    }
    if store.update_message(msg).await.is_ok() {
        emit(DomainEvent::MessageUpdated {
            conversation_id,
            message: msg.clone(),
        });
    }
}

/// Flush pending text, emit `MessageAppended` + `TurnFinished`, resolve the
/// `run_turn` waiter, and set the conversation idle.
async fn finalize<S: Store>(
    store: &Arc<S>,
    emit: &EmitFn,
    conversation_id: ConversationId,
    msg: &mut Message,
    pending: &mut Option<PendingChunk>,
    ctx: &mut TurnCtx,
) {
    flush_and_persist(store, emit, conversation_id, msg, pending).await;
    emit(DomainEvent::MessageAppended {
        conversation_id,
        message: msg.clone(),
    });
    emit(DomainEvent::TurnFinished {
        conversation_id,
        human_message_id: ctx.human_message_id,
        agent_message_id: msg.id,
    });
    ctx.succeed(msg);
    set_status_now(store, emit, conversation_id, ConversationStatus::Idle).await;
}

/// Apply one session update to the in-flight message. Returns `true` when the
/// turn is over and the driver loop must terminate.
async fn apply_update<S: Store>(
    store: &Arc<S>,
    emit: &EmitFn,
    conversation_id: ConversationId,
    msg: &mut Message,
    pending: &mut Option<PendingChunk>,
    ctx: &mut TurnCtx,
    update: SessionUpdate,
) -> bool {
    match update {
        SessionUpdate::AgentText(delta) => {
            // A kind switch flushes the pending thought so block order
            // matches the wire order.
            if matches!(pending.as_ref(), Some(PendingChunk::Thought(_))) {
                flush_and_persist(store, emit, conversation_id, msg, pending).await;
            }
            match pending {
                Some(PendingChunk::Text(t)) => t.push_str(&delta),
                _ => *pending = Some(PendingChunk::Text(delta.clone())),
            }
            emit(DomainEvent::MessageDelta {
                conversation_id,
                message_id: msg.id,
                block_index: msg.blocks.len(),
                text: delta,
            });
        }
        SessionUpdate::Thought(chunk) => {
            if matches!(pending.as_ref(), Some(PendingChunk::Text(_))) {
                flush_and_persist(store, emit, conversation_id, msg, pending).await;
            }
            match pending {
                Some(PendingChunk::Thought(t)) => t.push_str(&chunk),
                _ => *pending = Some(PendingChunk::Thought(chunk)),
            }
        }
        SessionUpdate::ToolCall {
            id,
            title,
            kind,
            status,
            raw,
        } => {
            flush_and_persist(store, emit, conversation_id, msg, pending).await;
            msg.blocks.push(ContentBlock::ToolCall {
                id,
                title,
                tool_kind: kind,
                status,
                raw,
            });
            if store.update_message(msg).await.is_ok() {
                emit(DomainEvent::MessageUpdated {
                    conversation_id,
                    message: msg.clone(),
                });
            }
        }
        SessionUpdate::ToolCallUpdate {
            id,
            title,
            kind,
            status,
            raw,
        } => {
            let existing = msg.blocks.iter_mut().rev().find_map(|b| match b {
                ContentBlock::ToolCall { id: bid, .. } if *bid == id => Some(b),
                _ => None,
            });
            match existing {
                Some(ContentBlock::ToolCall {
                    title: t,
                    tool_kind: k,
                    status: s,
                    raw: r,
                    ..
                }) => {
                    if let Some(t2) = title {
                        *t = t2;
                    }
                    if kind.is_some() {
                        *k = kind;
                    }
                    if status.is_some() {
                        *s = status;
                    }
                    *r = raw;
                }
                _ => msg.blocks.push(ContentBlock::ToolCall {
                    id,
                    title: title.unwrap_or_default(),
                    tool_kind: kind,
                    status,
                    raw,
                }),
            }
            if store.update_message(msg).await.is_ok() {
                emit(DomainEvent::MessageUpdated {
                    conversation_id,
                    message: msg.clone(),
                });
            }
        }
        SessionUpdate::Plan(raw) => {
            flush_and_persist(store, emit, conversation_id, msg, pending).await;
            msg.blocks.push(ContentBlock::Plan { raw });
            if store.update_message(msg).await.is_ok() {
                emit(DomainEvent::MessageUpdated {
                    conversation_id,
                    message: msg.clone(),
                });
            }
        }
        SessionUpdate::PermissionRequested {
            request_id,
            tool_call,
            options,
        } => {
            let Ok(id) = request_id.parse::<PermissionRequestId>() else {
                warn!(%request_id, "permission request id is not a uuid; skipping");
                return false;
            };
            let req = PermissionRequest {
                id,
                conversation_id: Some(conversation_id),
                run_id: None,
                tool_call,
                options,
                status: PermissionRequestStatus::Pending,
                created_at: chrono::Utc::now(),
                resolved_at: None,
            };
            if store.insert_permission_request(&req).await.is_ok() {
                emit(DomainEvent::PermissionRequested { request: req });
            }
            set_status_now(
                store,
                emit,
                conversation_id,
                ConversationStatus::AwaitingPermission,
            )
            .await;
        }
        SessionUpdate::ConfigOptionsChanged(opts) => {
            if let Ok(Some(mut c)) = store.get_conversation(conversation_id).await {
                c.config_options = opts.clone();
                c.updated_at = chrono::Utc::now();
                if store.update_conversation(&c).await.is_ok() {
                    emit(DomainEvent::ConversationConfigChanged {
                        conversation_id,
                        config_options: opts,
                    });
                }
            }
        }
        SessionUpdate::TurnFinished { .. } => {
            finalize(store, emit, conversation_id, msg, pending, ctx).await;
            return true;
        }
        SessionUpdate::Other { .. } => {}
        SessionUpdate::Error(e) => {
            warn!(%e, "session update error");
        }
        SessionUpdate::Exited => {
            set_status_now(store, emit, conversation_id, ConversationStatus::Closed).await;
            ctx.fail("session exited".to_string());
            return true;
        }
    }
    false
}

/// Markdown memory files under an org dir (organization.md, projects.md,
/// repos/*.md, projects/*.md) — the coordinator's on-disk memory.
fn memory_files_in(org_dir: &std::path::Path) -> Vec<PathBuf> {
    let mut files = Vec::new();
    for name in ["organization.md", "projects.md"] {
        let p = org_dir.join(name);
        if p.exists() {
            files.push(p);
        }
    }
    for sub in ["repos", "projects"] {
        if let Ok(entries) = std::fs::read_dir(org_dir.join(sub)) {
            let mut group: Vec<PathBuf> = entries
                .flatten()
                .map(|e| e.path())
                .filter(|p| p.extension().is_some_and(|e| e == "md"))
                .collect();
            group.sort();
            files.extend(group);
        }
    }
    files
}

async fn set_status_now<S: Store>(
    store: &Arc<S>,
    emit: &EmitFn,
    id: ConversationId,
    status: ConversationStatus,
) {
    let Ok(Some(mut c)) = store.get_conversation(id).await else {
        return;
    };
    if c.status == status {
        return;
    }
    c.status = status;
    c.updated_at = chrono::Utc::now();
    if store.update_conversation(&c).await.is_ok() {
        emit(DomainEvent::ConversationStatusChanged {
            conversation_id: id,
            status,
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;
    use tokio::sync::broadcast;

    fn conv(id: ConversationId) -> Conversation {
        let now = chrono::Utc::now();
        Conversation {
            id,
            agent_id: AgentId::new(),
            activity: Activity::Chat,
            model_profile_id: ModelProfileId::new(),
            organization_id: OrganizationId::new(),
            project_id: None,
            kind: ConversationKind::Chat,
            run_id: None,
            research_id: None,
            repository_id: None,
            workdir: PathBuf::from("/tmp"),
            title: "t".into(),
            acp_session_id: None,
            status: ConversationStatus::Idle,
            config_options: vec![],
            created_at: now,
            updated_at: now,
        }
    }

    fn agent_msg(cid: ConversationId) -> Message {
        Message {
            id: MessageId::new(),
            conversation_id: cid,
            author: MessageAuthor::Agent,
            blocks: vec![],
            created_at: chrono::Utc::now(),
        }
    }

    async fn ok_prompt() -> Result<String, mobius_harness::HarnessError> {
        Ok("end_turn".to_string())
    }

    /// Two back-to-back turns on one broadcast channel: each `drive_turn` must
    /// terminate after `TurnFinished` so turn-2 updates cannot leak into
    /// turn-1's message.
    #[tokio::test]
    async fn drive_turn_isolates_messages_across_turns() {
        let store = Arc::new(InMemoryStore::new());
        let events = Arc::new(Mutex::new(Vec::<DomainEvent>::new()));
        let ev = events.clone();
        let emit: EmitFn = Arc::new(move |e| {
            if let Ok(mut g) = ev.lock() {
                g.push(e);
            }
        });
        let cid = ConversationId::new();
        store
            .insert_conversation(&conv(cid))
            .await
            .expect("insert conv");
        let (tx, _) = broadcast::channel::<SessionUpdate>(64);

        // ---- turn 1 ----
        let m1 = agent_msg(cid);
        let m1_id = m1.id;
        store.insert_message(&m1).await.expect("insert m1");
        let rx1 = tx.subscribe();
        tx.send(SessionUpdate::Thought("thinking".into()))
            .expect("send");
        tx.send(SessionUpdate::AgentText("he".into()))
            .expect("send");
        tx.send(SessionUpdate::AgentText("llo".into()))
            .expect("send");
        tx.send(SessionUpdate::ToolCall {
            id: "tc1".into(),
            title: "Write file".into(),
            kind: Some("edit".into()),
            status: Some("running".into()),
            raw: serde_json::json!({"toolCallId":"tc1"}),
        })
        .expect("send");
        tx.send(SessionUpdate::ToolCallUpdate {
            id: "tc1".into(),
            title: Some("Wrote file".into()),
            kind: None,
            status: Some("completed".into()),
            raw: serde_json::json!({"toolCallId":"tc1","status":"completed"}),
        })
        .expect("send");
        tx.send(SessionUpdate::TurnFinished {
            stop_reason: "end_turn".into(),
        })
        .expect("send");

        drive_turn(
            store.clone(),
            emit.clone(),
            cid,
            m1,
            rx1,
            ok_prompt(),
            TurnCtx {
                human_message_id: None,
                done: None,
            },
        )
        .await;

        let m1_final = store.get_message(m1_id).await.expect("get").expect("msg1");
        let kinds: Vec<&str> = m1_final
            .blocks
            .iter()
            .map(|b| match b {
                ContentBlock::Thought { .. } => "thought",
                ContentBlock::Text { .. } => "text",
                ContentBlock::ToolCall { .. } => "tool_call",
                ContentBlock::Plan { .. } => "plan",
            })
            .collect();
        assert_eq!(kinds, ["thought", "text", "tool_call"]);
        assert!(
            matches!(&m1_final.blocks[1], ContentBlock::Text { text } if text == "hello"),
            "joined text: {:?}",
            m1_final.blocks[1]
        );
        assert!(
            matches!(&m1_final.blocks[2], ContentBlock::ToolCall { title, status, .. }
                if title == "Wrote file" && status.as_deref() == Some("completed")),
            "tool_call updated in place: {:?}",
            m1_final.blocks[2]
        );
        let m1_json = serde_json::to_string(&m1_final).expect("ser");

        // ---- turn 2: same broadcast channel, new receiver ----
        let m2 = agent_msg(cid);
        let m2_id = m2.id;
        store.insert_message(&m2).await.expect("insert m2");
        let rx2 = tx.subscribe();
        tx.send(SessionUpdate::AgentText("turn two".into()))
            .expect("send");
        tx.send(SessionUpdate::TurnFinished {
            stop_reason: "end_turn".into(),
        })
        .expect("send");

        drive_turn(
            store.clone(),
            emit.clone(),
            cid,
            m2,
            rx2,
            ok_prompt(),
            TurnCtx {
                human_message_id: None,
                done: None,
            },
        )
        .await;

        let m2_final = store.get_message(m2_id).await.expect("get").expect("msg2");
        assert!(
            matches!(m2_final.blocks.as_slice(), [ContentBlock::Text { text }] if text == "turn two"),
            "msg2 blocks: {:?}",
            m2_final.blocks
        );

        // Turn-1 message is byte-identical after turn 2.
        let m1_after = store
            .get_message(m1_id)
            .await
            .expect("get")
            .expect("msg1 again");
        assert_eq!(serde_json::to_string(&m1_after).expect("ser"), m1_json);

        // MessageUpdated carried conversation_id and the tool_call block.
        let updated_with_tool = events.lock().expect("events").iter().any(|e| {
            matches!(
                e,
                DomainEvent::MessageUpdated { conversation_id, message }
                    if *conversation_id == cid
                        && message.blocks.iter().any(|b| matches!(b, ContentBlock::ToolCall { .. }))
            )
        });
        assert!(updated_with_tool, "expected MessageUpdated with tool_call");
    }

    fn msg(cid: ConversationId, author: MessageAuthor, blocks: Vec<ContentBlock>) -> Message {
        Message {
            id: MessageId::new(),
            conversation_id: cid,
            author,
            blocks,
            created_at: chrono::Utc::now(),
        }
    }

    fn text_msg(cid: ConversationId, author: MessageAuthor, text: &str) -> Message {
        msg(
            cid,
            author,
            vec![ContentBlock::Text {
                text: text.to_string(),
            }],
        )
    }

    #[test]
    fn preamble_skips_non_text_blocks_and_empty_messages() {
        let cid = ConversationId::new();
        let messages = vec![
            text_msg(cid, MessageAuthor::Human, "build the thing"),
            msg(
                cid,
                MessageAuthor::Agent,
                vec![
                    ContentBlock::Thought { text: "hmm".into() },
                    ContentBlock::ToolCall {
                        id: "t1".into(),
                        title: "Write".into(),
                        tool_kind: None,
                        status: None,
                        raw: serde_json::json!({}),
                    },
                    ContentBlock::Text {
                        text: "done".into(),
                    },
                ],
            ),
            msg(cid, MessageAuthor::Agent, vec![]), // empty → skipped
            msg(
                cid,
                MessageAuthor::Agent,
                vec![ContentBlock::Thought { text: "x".into() }],
            ), // no text → skipped
        ];
        let p = build_resume_preamble(&messages);
        assert!(p.starts_with("This conversation is being resumed"));
        assert!(p.contains("Human: build the thing"));
        assert!(p.contains("Agent: done"));
        assert!(!p.contains("hmm"));
        assert!(!p.contains("Write"));
        // 2 rendered lines + header.
        assert_eq!(p.lines().count(), 1 + 1 + 2);
    }

    #[test]
    fn preamble_truncates_to_last_40_and_16k_chars() {
        let cid = ConversationId::new();
        // 50 messages > 40 cap.
        let messages: Vec<Message> = (0..50)
            .map(|i| text_msg(cid, MessageAuthor::Human, &format!("m{i}")))
            .collect();
        let p = build_resume_preamble(&messages);
        assert!(!p.contains("m9\n"), "oldest dropped by 40-message cap");
        assert!(p.contains("Human: m49"));

        // 40 messages each ~700 chars > 16k → oldest dropped again.
        let big: Vec<Message> = (0..40)
            .map(|i| {
                text_msg(
                    cid,
                    MessageAuthor::Human,
                    &format!("{i}:{}", "x".repeat(700)),
                )
            })
            .collect();
        let p = build_resume_preamble(&big);
        assert!(p.len() <= 16_000 + 200, "len {}", p.len());
        assert!(p.contains("Human: 39:"));
        assert!(!p.contains("Human: 0:"), "oldest dropped by char cap");
    }

    #[tokio::test]
    async fn reset_stale_statuses_idles_dead_conversations() {
        let store = Arc::new(InMemoryStore::new());
        let emit: EmitFn = Arc::new(|_| {});
        let mgr = SessionManager::new(store.clone(), emit, PathBuf::from("/tmp/data"));

        let mut streaming = conv(ConversationId::new());
        streaming.status = ConversationStatus::Streaming;
        let mut awaiting = conv(ConversationId::new());
        awaiting.status = ConversationStatus::AwaitingPermission;
        let mut closed = conv(ConversationId::new());
        closed.status = ConversationStatus::Closed;
        let idle = conv(ConversationId::new());
        store.insert_conversation(&streaming).await.expect("i");
        store.insert_conversation(&awaiting).await.expect("i");
        store.insert_conversation(&closed).await.expect("i");
        store.insert_conversation(&idle).await.expect("i");

        mgr.reset_stale_statuses().await;

        let status = |id: ConversationId| {
            let store = store.clone();
            async move {
                store
                    .get_conversation(id)
                    .await
                    .expect("get")
                    .expect("conv")
                    .status
            }
        };
        assert_eq!(status(streaming.id).await, ConversationStatus::Idle);
        assert_eq!(status(awaiting.id).await, ConversationStatus::Idle);
        assert_eq!(status(closed.id).await, ConversationStatus::Closed);
        assert_eq!(status(idle.id).await, ConversationStatus::Idle);
    }
}
