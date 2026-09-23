//! Long-lived ACP sessions over `agent-client-protocol`.
//!
//! A [`HarnessSession`] owns one spawned agent process, one ACP connection and
//! one ACP session. Permission requests are routed by the agent's
//! [`PermissionPolicy`]: `Auto`/`WorkspaceEdits` pick the first "allow"
//! option, `ReadOnly` allows only `read`-kind tool calls, and `AskHuman` parks
//! the ACP request on a oneshot until [`HarnessSession::respond_permission`]
//! (or `cancel`/`close`) resolves it — human approval UI is a server concern.

use crate::config_options;
use mobius_core::{
    Harness, ModelProfile, PermissionOptionInfo, PermissionPolicy, PermissionRequestId,
    SessionConfigOption, ToolCallInfo,
};
use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use thiserror::Error;
use tokio::sync::{broadcast, oneshot};

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::schema::v1::{
    self as acp, CancelNotification, ContentBlock, ContentChunk, InitializeRequest,
    NewSessionRequest, PromptRequest, RequestPermissionOutcome, RequestPermissionRequest,
    RequestPermissionResponse, SelectedPermissionOutcome, SessionNotification,
    SetSessionConfigOptionRequest, TextContent,
};
use agent_client_protocol::{AcpAgent, AcpAgentConfig, Agent, ConnectionTo};

#[derive(Debug, Error)]
pub enum HarnessError {
    #[error("protocol error: {0}")]
    Protocol(String),
    #[error("agent exited or connection closed: {0}")]
    Disconnected(String),
    #[error("spawn timed out waiting for session/new")]
    SpawnTimeout,
    #[error("session is closed")]
    Closed,
}

impl From<agent_client_protocol::Error> for HarnessError {
    fn from(e: agent_client_protocol::Error) -> Self {
        HarnessError::Protocol(e.to_string())
    }
}

/// Everything needed to spawn one harness session.
#[derive(Debug, Clone)]
pub struct SpawnSpec {
    pub harness: Harness,
    pub cwd: PathBuf,
    pub policy: PermissionPolicy,
    pub profile: Option<ModelProfile>,
}

/// Stream of updates produced by a session. Delivered over a `broadcast`
/// channel from [`HarnessSession::updates`].
#[derive(Debug, Clone)]
pub enum SessionUpdate {
    AgentText(String),
    Thought(String),
    ToolCall {
        id: String,
        title: String,
        kind: Option<String>,
        status: Option<String>,
        raw: serde_json::Value,
    },
    ToolCallUpdate {
        id: String,
        title: Option<String>,
        kind: Option<String>,
        status: Option<String>,
        raw: serde_json::Value,
    },
    Plan(serde_json::Value),
    /// An ACP `session/request_permission` request parked for a human.
    /// `request_id` is the key for [`HarnessSession::respond_permission`].
    PermissionRequested {
        request_id: String,
        tool_call: ToolCallInfo,
        options: Vec<PermissionOptionInfo>,
    },
    ConfigOptionsChanged(Vec<SessionConfigOption>),
    TurnFinished {
        stop_reason: String,
    },
    /// An ACP update we don't map semantically (e.g. `usage_update`,
    /// `available_commands_update`, `session_info_update`). Log at debug.
    Other {
        kind: String,
        raw: serde_json::Value,
    },
    Error(String),
    /// The agent process exited or the connection ended.
    Exited,
}

type PendingPermissions = HashMap<String, oneshot::Sender<Option<String>>>;

struct Shared {
    updates: broadcast::Sender<SessionUpdate>,
    pending_permissions: Mutex<PendingPermissions>,
    config_options: Mutex<Vec<SessionConfigOption>>,
}

impl Shared {
    fn emit(&self, update: SessionUpdate) {
        // No subscribers is fine.
        let _ = self.updates.send(update);
    }
}

fn tool_call_info(tc: &acp::ToolCallUpdate) -> ToolCallInfo {
    ToolCallInfo {
        tool_call_id: tc.tool_call_id.to_string(),
        title: tc.fields.title.clone(),
        kind: tc
            .fields
            .kind
            .and_then(|k| serde_json::to_value(k).ok())
            .and_then(|v| v.as_str().map(String::from)),
        raw: serde_json::to_value(tc).unwrap_or_default(),
    }
}

fn map_update(update: &acp::SessionUpdate) -> Option<SessionUpdate> {
    match update {
        acp::SessionUpdate::AgentMessageChunk(ContentChunk {
            content: ContentBlock::Text(t),
            ..
        }) => Some(SessionUpdate::AgentText(t.text.clone())),
        acp::SessionUpdate::AgentThoughtChunk(ContentChunk {
            content: ContentBlock::Text(t),
            ..
        }) => Some(SessionUpdate::Thought(t.text.clone())),
        acp::SessionUpdate::ToolCall(tc) => Some(SessionUpdate::ToolCall {
            id: tc.tool_call_id.to_string(),
            title: tc.title.clone(),
            kind: serde_json::to_value(tc.kind)
                .ok()
                .and_then(|v| v.as_str().map(String::from)),
            status: serde_json::to_value(tc.status)
                .ok()
                .and_then(|v| v.as_str().map(String::from)),
            raw: serde_json::to_value(tc).unwrap_or_default(),
        }),
        acp::SessionUpdate::ToolCallUpdate(tcu) => Some(SessionUpdate::ToolCallUpdate {
            id: tcu.tool_call_id.to_string(),
            title: tcu.fields.title.clone(),
            kind: tcu
                .fields
                .kind
                .and_then(|k| serde_json::to_value(k).ok())
                .and_then(|v| v.as_str().map(String::from)),
            status: tcu
                .fields
                .status
                .and_then(|s| serde_json::to_value(s).ok())
                .and_then(|v| v.as_str().map(String::from)),
            raw: serde_json::to_value(tcu).unwrap_or_default(),
        }),
        acp::SessionUpdate::Plan(plan) => Some(SessionUpdate::Plan(
            serde_json::to_value(plan).unwrap_or_default(),
        )),
        acp::SessionUpdate::ConfigOptionUpdate(update) => {
            Some(SessionUpdate::ConfigOptionsChanged(
                update
                    .config_options
                    .iter()
                    .map(config_options::to_core)
                    .collect(),
            ))
        }
        other => {
            let raw = serde_json::to_value(other).unwrap_or_default();
            let kind = raw
                .get("sessionUpdate")
                .and_then(|t| t.as_str())
                .unwrap_or("unknown")
                .to_string();
            Some(SessionUpdate::Other { kind, raw })
        }
    }
}

fn permission_options(req: &RequestPermissionRequest) -> Vec<PermissionOptionInfo> {
    req.options
        .iter()
        .map(|o| PermissionOptionInfo {
            option_id: o.option_id.to_string(),
            name: o.name.clone(),
            kind: serde_json::to_value(o.kind)
                .ok()
                .and_then(|v| v.as_str().map(String::from))
                .unwrap_or_default(),
        })
        .collect()
}

fn first_option_matching(
    req: &RequestPermissionRequest,
    allow: bool,
) -> Option<acp::PermissionOptionId> {
    use acp::PermissionOptionKind as K;
    req.options
        .iter()
        .find(|o| {
            if allow {
                matches!(o.kind, K::AllowOnce | K::AllowAlways)
            } else {
                matches!(o.kind, K::RejectOnce | K::RejectAlways)
            }
        })
        .map(|o| o.option_id.clone())
}

fn outcome_for(option_id: Option<acp::PermissionOptionId>) -> RequestPermissionOutcome {
    match option_id {
        Some(id) => RequestPermissionOutcome::Selected(SelectedPermissionOutcome::new(id)),
        None => RequestPermissionOutcome::Cancelled,
    }
}

/// A live ACP session. Dropping/closing tears the agent process down.
pub struct HarnessSession {
    connection: ConnectionTo<Agent>,
    session_id: acp::SessionId,
    shared: Arc<Shared>,
    close_tx: Mutex<Option<oneshot::Sender<()>>>,
    _driver: tokio::task::JoinHandle<Result<(), agent_client_protocol::Error>>,
}

struct SpawnedCore {
    connection: ConnectionTo<Agent>,
    session_id: acp::SessionId,
}

impl HarnessSession {
    /// Spawn the harness subprocess, run `initialize` + `session/new`, then
    /// apply the profile (model/effort via config options, raw `config`
    /// entries verbatim). Unmatched options are logged and skipped.
    pub async fn spawn(spec: SpawnSpec) -> Result<Self, HarnessError> {
        let mut config = AcpAgentConfig::new(&spec.harness.command)
            .args(spec.harness.args.iter().cloned())
            .envs(spec.harness.env.iter().map(|(k, v)| (k.clone(), v.clone())));

        // Launch-arg model override: e.g. `devin acp --model swe`.
        if let Some(model) = spec.profile.as_ref().and_then(|p| p.model.clone())
            && !spec.harness.model_arg_template.is_empty()
        {
            let args: Vec<String> = spec
                .harness
                .model_arg_template
                .iter()
                .map(|a| a.replace("{model}", &model))
                .collect();
            tracing::debug!(args = ?args, "applying model_arg_template");
            config = config.args(args);
        }

        let agent = AcpAgent::new(config);
        let shared = Arc::new(Shared {
            updates: broadcast::channel(512).0,
            pending_permissions: Mutex::new(HashMap::new()),
            config_options: Mutex::new(Vec::new()),
        });
        let policy = spec.policy;

        let (ready_tx, ready_rx) = oneshot::channel::<Result<SpawnedCore, HarnessError>>();
        let (close_tx, close_rx) = oneshot::channel::<()>();

        let shared_notifications = shared.clone();
        let shared_requests = shared.clone();
        let shared_session = shared.clone();
        let profile = spec.profile.clone();
        let cwd = spec.cwd.clone();

        let driver = tokio::spawn(
            agent_client_protocol::Client
                .builder()
                .on_receive_notification(
                    async move |notification: SessionNotification, _cx| {
                        // Agent-initiated mode switches arrive as
                        // `current_mode_update`; fold the value into the
                        // advertised `mode` option and re-emit the list.
                        if let acp::SessionUpdate::CurrentModeUpdate(mu) = &notification.update {
                            let mut changed = false;
                            if let Ok(mut guard) = shared_notifications.config_options.lock()
                                && let Some(mode) = guard.iter_mut().find(|o| o.id == "mode")
                            {
                                mode.current_value = Some(mu.current_mode_id.to_string());
                                changed = true;
                            }
                            if changed {
                                let updated = shared_notifications
                                    .config_options
                                    .lock()
                                    .map(|g| g.clone())
                                    .unwrap_or_default();
                                shared_notifications
                                    .emit(SessionUpdate::ConfigOptionsChanged(updated));
                            }
                            return Ok(());
                        }
                        if let Some(update) = map_update(&notification.update) {
                            if let SessionUpdate::ConfigOptionsChanged(opts) = &update
                                && let Ok(mut guard) = shared_notifications.config_options.lock()
                            {
                                *guard = opts.clone();
                            }
                            if let SessionUpdate::Other { kind, .. } = &update {
                                tracing::debug!(%kind, "unmapped session update");
                            }
                            shared_notifications.emit(update);
                        }
                        Ok(())
                    },
                    agent_client_protocol::on_receive_notification!(),
                )
                .on_receive_request(
                    async move |request: RequestPermissionRequest, responder, _connection| {
                        handle_permission(&shared_requests, policy, request, responder).await
                    },
                    agent_client_protocol::on_receive_request!(),
                )
                .connect_with(agent, move |connection: ConnectionTo<Agent>| {
                    let shared = shared_session.clone();
                    let profile = profile.clone();
                    let cwd = cwd.clone();
                    async move {
                        let result = setup_session(&connection, &cwd, &shared, &profile).await;
                        match result {
                            Ok(core) => {
                                let _ = ready_tx.send(Ok(core));
                            }
                            Err(e) => {
                                let _ = ready_tx.send(Err(e));
                            }
                        }
                        // Keep the connection alive until close() is called.
                        let _ = close_rx.await;
                        shared.emit(SessionUpdate::Exited);
                        Ok(())
                    }
                }),
        );

        let core = match tokio::time::timeout(std::time::Duration::from_secs(90), ready_rx).await {
            Ok(Ok(Ok(core))) => core,
            Ok(Ok(Err(e))) => return Err(e),
            Ok(Err(_)) => {
                return Err(HarnessError::Disconnected(
                    "connection driver ended before session was ready".into(),
                ));
            }
            Err(_) => return Err(HarnessError::SpawnTimeout),
        };

        Ok(Self {
            connection: core.connection,
            session_id: core.session_id,
            shared,
            close_tx: Mutex::new(Some(close_tx)),
            _driver: driver,
        })
    }

    pub fn session_id(&self) -> String {
        self.session_id.to_string()
    }

    /// Stream of session updates.
    pub fn updates(&self) -> broadcast::Receiver<SessionUpdate> {
        self.shared.updates.subscribe()
    }

    /// Send a prompt; resolves with the agent's stop reason when the turn
    /// finishes. Session updates are observed via [`updates`](Self::updates).
    pub async fn prompt(&self, text: &str) -> Result<String, HarnessError> {
        let response = self
            .connection
            .send_request(PromptRequest::new(
                self.session_id.clone(),
                vec![ContentBlock::Text(TextContent::new(text.to_string()))],
            ))
            .block_task()
            .await?;
        let stop_reason = serde_json::to_value(response.stop_reason)
            .ok()
            .and_then(|v| v.as_str().map(String::from))
            .unwrap_or_else(|| format!("{:?}", response.stop_reason));
        self.shared.emit(SessionUpdate::TurnFinished {
            stop_reason: stop_reason.clone(),
        });
        Ok(stop_reason)
    }

    /// Send `session/cancel` to abort the in-flight turn.
    pub async fn cancel(&self) -> Result<(), HarnessError> {
        self.connection
            .send_notification(CancelNotification::new(self.session_id.clone()))?;
        Ok(())
    }

    /// Resolve a parked `AskHuman` permission request with the chosen
    /// `option_id`. Returns `false` if the request id is unknown.
    pub fn respond_permission(&self, request_id: &str, option_id: &str) -> bool {
        let sender = self
            .shared
            .pending_permissions
            .lock()
            .ok()
            .and_then(|mut pending| pending.remove(request_id));
        match sender {
            Some(tx) => tx.send(Some(option_id.to_string())).is_ok(),
            None => false,
        }
    }

    /// `session/set_config_option`; returns the full updated option list.
    pub async fn set_config_option(
        &self,
        config_id: &str,
        value: &str,
    ) -> Result<Vec<SessionConfigOption>, HarnessError> {
        let response = self
            .connection
            .send_request(SetSessionConfigOptionRequest::new(
                self.session_id.clone(),
                acp::SessionConfigId::new(config_id),
                acp::SessionConfigOptionValue::from(value),
            ))
            .block_task()
            .await?;
        let options: Vec<SessionConfigOption> = response
            .config_options
            .iter()
            .map(config_options::to_core)
            .collect();
        if let Ok(mut guard) = self.shared.config_options.lock() {
            *guard = options.clone();
        }
        self.shared
            .emit(SessionUpdate::ConfigOptionsChanged(options.clone()));
        Ok(options)
    }

    /// Snapshot of the session's current config options.
    pub fn config_options(&self) -> Vec<SessionConfigOption> {
        self.shared
            .config_options
            .lock()
            .map(|g| g.clone())
            .unwrap_or_default()
    }

    /// Close the session and let the driver task end (the agent process is
    /// killed when its transport drops).
    pub async fn close(&self) {
        if let Ok(mut guard) = self.close_tx.lock()
            && let Some(tx) = guard.take()
        {
            let _ = tx.send(());
        }
        // Any still-parked permission requests resolve as cancelled.
        if let Ok(mut pending) = self.shared.pending_permissions.lock() {
            for (_, tx) in pending.drain() {
                let _ = tx.send(None);
            }
        }
    }
}

async fn setup_session(
    connection: &ConnectionTo<Agent>,
    cwd: &std::path::Path,
    shared: &Arc<Shared>,
    profile: &Option<ModelProfile>,
) -> Result<SpawnedCore, HarnessError> {
    let init = connection
        .send_request(InitializeRequest::new(ProtocolVersion::V1))
        .block_task()
        .await?;
    tracing::info!(
        load_session = init.agent_capabilities.load_session,
        "agent capabilities"
    );

    let new_session = connection
        .send_request(NewSessionRequest::new(cwd.to_path_buf()))
        .block_task()
        .await?;

    let initial: Vec<SessionConfigOption> = new_session
        .config_options
        .as_deref()
        .unwrap_or(&[])
        .iter()
        .map(config_options::to_core)
        .collect();
    if let Ok(mut guard) = shared.config_options.lock() {
        *guard = initial.clone();
    }

    apply_profile(connection, &new_session.session_id, shared, profile).await;

    Ok(SpawnedCore {
        connection: connection.clone(),
        session_id: new_session.session_id,
    })
}

/// Apply a model profile onto a live session: `model` category → fuzzy model
/// match, `thought_level` category → effort heuristic, then raw `config`
/// entries verbatim. Every miss is a `warn!`, never an error.
async fn apply_profile(
    connection: &ConnectionTo<Agent>,
    session_id: &acp::SessionId,
    shared: &Arc<Shared>,
    profile: &Option<ModelProfile>,
) {
    let Some(profile) = profile else {
        return;
    };

    let advertised = shared
        .config_options
        .lock()
        .map(|g| g.clone())
        .unwrap_or_default();

    if let Some(model) = &profile.model {
        match config_options::find_by_category(&advertised, "model") {
            Some(opt) => match config_options::match_model_option(opt, model) {
                Some(value) => {
                    set_config_option_inner(connection, session_id, shared, &opt.id, &value).await
                }
                None => tracing::warn!(
                    option = %opt.id,
                    %model,
                    "no advertised model option matched profile model"
                ),
            },
            None => tracing::warn!(
                %model,
                "harness advertises no 'model' config option; \
                 model left at launch-arg/harness default"
            ),
        }
    }

    if let Some(effort) = profile.effort {
        match config_options::find_effort_option(&advertised) {
            Some(opt) => match config_options::match_effort_option(opt, effort) {
                Some(value) => {
                    set_config_option_inner(connection, session_id, shared, &opt.id, &value).await
                }
                None => tracing::warn!(
                    option = %opt.id,
                    ?effort,
                    "no advertised thought_level option matched effort"
                ),
            },
            None => tracing::warn!(
                ?effort,
                "harness advertises no 'thought_level'/'effort' config option"
            ),
        }
    }

    for (config_id, value) in &profile.config {
        set_config_option_inner(connection, session_id, shared, config_id, value).await;
    }
}

async fn set_config_option_inner(
    connection: &ConnectionTo<Agent>,
    session_id: &acp::SessionId,
    shared: &Arc<Shared>,
    config_id: &str,
    value: &str,
) {
    let result = connection
        .send_request(SetSessionConfigOptionRequest::new(
            session_id.clone(),
            acp::SessionConfigId::new(config_id),
            acp::SessionConfigOptionValue::from(value),
        ))
        .block_task()
        .await;
    match result {
        Ok(response) => {
            let options: Vec<SessionConfigOption> = response
                .config_options
                .iter()
                .map(config_options::to_core)
                .collect();
            if let Ok(mut guard) = shared.config_options.lock() {
                *guard = options.clone();
            }
            shared.emit(SessionUpdate::ConfigOptionsChanged(options));
        }
        Err(e) => {
            tracing::warn!(%config_id, %value, error = %e,
                "session/set_config_option failed");
        }
    }
}

async fn handle_permission(
    shared: &Arc<Shared>,
    policy: PermissionPolicy,
    request: RequestPermissionRequest,
    responder: agent_client_protocol::Responder<RequestPermissionResponse>,
) -> Result<(), agent_client_protocol::Error> {
    match policy {
        PermissionPolicy::Auto | PermissionPolicy::WorkspaceEdits => responder.respond(
            RequestPermissionResponse::new(outcome_for(first_option_matching(&request, true))),
        ),
        PermissionPolicy::ReadOnly => {
            let allow = config_options::is_read_tool_kind(request.tool_call.fields.kind);
            responder.respond(RequestPermissionResponse::new(outcome_for(
                first_option_matching(&request, allow),
            )))
        }
        PermissionPolicy::AskHuman => {
            // Ids are PermissionRequestIds so the orchestrator can persist the
            // row and resolve it back by the same key.
            let request_id = PermissionRequestId::new().to_string();
            let (tx, rx) = oneshot::channel::<Option<String>>();
            if let Ok(mut pending) = shared.pending_permissions.lock() {
                pending.insert(request_id.clone(), tx);
            }
            shared.emit(SessionUpdate::PermissionRequested {
                request_id,
                tool_call: tool_call_info(&request.tool_call),
                options: permission_options(&request),
            });
            let chosen = rx.await.ok().flatten();
            responder.respond(RequestPermissionResponse::new(match chosen {
                Some(option_id) => outcome_for(Some(acp::PermissionOptionId::new(option_id))),
                None => RequestPermissionOutcome::Cancelled,
            }))
        }
    }
}
