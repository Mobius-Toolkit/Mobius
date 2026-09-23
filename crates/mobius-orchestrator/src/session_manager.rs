//! Human-chat sessions: long-lived `HarnessSession`s keyed by `Conversation`,
//! streaming updates persisted as `Message` blocks and forwarded as
//! `DomainEvent`s.

use crate::dispatcher::EmitFn;
use crate::error::OrchestratorError;
use crate::resolver::ProfileResolver;
use mobius_core::*;
use mobius_harness::{HarnessSession, SessionUpdate, SpawnSpec};
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::Arc;
use tokio::sync::Mutex;
use tracing::warn;

pub struct SessionManager<S: Store> {
    store: Arc<S>,
    emit: EmitFn,
    sessions: Mutex<HashMap<ConversationId, Arc<HarnessSession>>>,
}

impl<S: Store + 'static> SessionManager<S> {
    pub fn new(store: Arc<S>, emit: EmitFn) -> Self {
        Self {
            store,
            emit,
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
        let session = Arc::new(
            HarnessSession::spawn(SpawnSpec {
                harness,
                cwd: conversation.workdir.clone(),
                policy: agent.permission_policy,
                profile: Some(profile),
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

    /// Resolve the agent's Chat profile, spawn the harness session, persist
    /// the conversation with the advertised config options.
    pub async fn open_conversation(
        &self,
        agent_id: AgentId,
        workdir: PathBuf,
        title: String,
    ) -> Result<Conversation, OrchestratorError> {
        let agent = self
            .store
            .get_agent(agent_id)
            .await?
            .ok_or_else(|| OrchestratorError::NotFound(format!("agent {agent_id}")))?;
        let (profile, harness) =
            ProfileResolver::resolve(self.store.as_ref(), &agent, Activity::Chat).await?;
        let session = Arc::new(
            HarnessSession::spawn(SpawnSpec {
                harness: harness.clone(),
                cwd: workdir.clone(),
                policy: agent.permission_policy,
                profile: Some(profile.clone()),
            })
            .await?,
        );
        let now = chrono::Utc::now();
        let conversation = Conversation {
            id: ConversationId::new(),
            agent_id,
            activity: Activity::Chat,
            model_profile_id: profile.id,
            repository_id: agent.repository_id,
            workdir,
            title,
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

    /// Persist the human message, then drive the turn in the background:
    /// stream `MessageDelta`s, persist agent blocks as they complete, park
    /// permissions, finalize on `TurnFinished`.
    pub async fn send_message(
        &self,
        conversation_id: ConversationId,
        text: &str,
    ) -> Result<Message, OrchestratorError> {
        let (session, reattached) = self.session_or_reattach(conversation_id).await?;
        // On re-attach the new ACP session has no memory of the prior
        // turns — prepend a transcript preamble to the first prompt only.
        let prompt_text = if reattached {
            let prior = self
                .store
                .list_messages_by_conversation(conversation_id)
                .await?;
            format!("{}\n\n{text}", build_resume_preamble(&prior))
        } else {
            text.to_string()
        };
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
            )
            .await;
        });
        Ok(human)
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
                    update,
                )
                .await
                {
                    finished = true;
                    break;
                }
            }
            if !finished {
                finalize(&store, &emit, conversation_id, &mut msg, &mut pending).await;
            }
            return;
        }
        tokio::select! {
            update = rx.recv() => {
                match update {
                    Ok(u) => {
                        if apply_update(&store, &emit, conversation_id, &mut msg, &mut pending, u).await {
                            return;
                        }
                    }
                    Err(tokio::sync::broadcast::error::RecvError::Closed) => return,
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

/// Flush pending text, emit `MessageAppended`, and set the conversation idle.
async fn finalize<S: Store>(
    store: &Arc<S>,
    emit: &EmitFn,
    conversation_id: ConversationId,
    msg: &mut Message,
    pending: &mut Option<PendingChunk>,
) {
    flush_and_persist(store, emit, conversation_id, msg, pending).await;
    emit(DomainEvent::MessageAppended {
        conversation_id,
        message: msg.clone(),
    });
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
            finalize(store, emit, conversation_id, msg, pending).await;
            return true;
        }
        SessionUpdate::Other { .. } => {}
        SessionUpdate::Error(e) => {
            warn!(%e, "session update error");
        }
        SessionUpdate::Exited => {
            set_status_now(store, emit, conversation_id, ConversationStatus::Closed).await;
            return true;
        }
    }
    false
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

        drive_turn(store.clone(), emit.clone(), cid, m1, rx1, ok_prompt()).await;

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

        drive_turn(store.clone(), emit.clone(), cid, m2, rx2, ok_prompt()).await;

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
        let mgr = SessionManager::new(store.clone(), emit);

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
