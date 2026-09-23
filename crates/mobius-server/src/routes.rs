//! REST + SSE routes. CRUD for domain config with 422 field-level validation;
//! chat, permissions, manual signals, and the `EventBus` SSE stream.

use crate::state::AppState;
use axum::extract::{Path, Query, State};
use axum::http::{HeaderMap, StatusCode};
use axum::response::sse::{Event, KeepAlive, Sse};
use axum::routing::{get, post};
use axum::{Json, Router};
use futures::stream::{self, Stream};
use mobius_api::*;
use mobius_core::*;
use serde::Deserialize;
use std::collections::BTreeMap;
use std::convert::Infallible;
use std::path::PathBuf;
use tokio_stream::StreamExt;
use tokio_stream::wrappers::BroadcastStream;

type ApiResult<T> = Result<T, (StatusCode, Json<ApiError>)>;

fn err(status: StatusCode, message: impl Into<String>) -> (StatusCode, Json<ApiError>) {
    (
        status,
        Json(ApiError {
            message: message.into(),
            fields: BTreeMap::new(),
        }),
    )
}

fn err_fields(
    message: impl Into<String>,
    fields: BTreeMap<String, String>,
) -> (StatusCode, Json<ApiError>) {
    (
        StatusCode::UNPROCESSABLE_ENTITY,
        Json(ApiError {
            message: message.into(),
            fields,
        }),
    )
}

fn store_err(e: StoreError) -> (StatusCode, Json<ApiError>) {
    match e {
        StoreError::NotFound(m) => err(StatusCode::NOT_FOUND, m),
        StoreError::Conflict(m) => err(StatusCode::CONFLICT, m),
        other => err(StatusCode::INTERNAL_SERVER_ERROR, other.to_string()),
    }
}

fn orch_err(e: mobius_orchestrator::OrchestratorError) -> (StatusCode, Json<ApiError>) {
    use mobius_orchestrator::OrchestratorError as O;
    match e {
        O::NotFound(m) => err(StatusCode::NOT_FOUND, m),
        O::NotLive(m) => err(StatusCode::CONFLICT, m),
        O::Store(s) => store_err(s),
        other => err(StatusCode::INTERNAL_SERVER_ERROR, other.to_string()),
    }
}

fn not_found(what: impl Into<String>) -> (StatusCode, Json<ApiError>) {
    err(StatusCode::NOT_FOUND, what)
}

fn changed(state: &AppState, kind: EntityKind, id: impl ToString) {
    state.events.publish(DomainEvent::EntityChanged {
        kind,
        id: id.to_string(),
    });
}

pub fn router(state: AppState, ui_dir: Option<PathBuf>) -> Router {
    let api = Router::new()
        // Health + events
        .route("/health", get(health))
        .route("/api/v1/events", get(events_sse))
        // CRUD
        .route("/api/v1/organizations", get(list_orgs).post(create_org))
        .route(
            "/api/v1/organizations/{id}",
            get(get_org).put(update_org).delete(delete_org),
        )
        .route("/api/v1/repositories", get(list_repos).post(create_repo))
        .route(
            "/api/v1/repositories/{id}",
            get(get_repo).put(update_repo).delete(delete_repo),
        )
        .route("/api/v1/projects", get(list_projects).post(create_project))
        .route(
            "/api/v1/projects/{id}",
            get(get_project).put(update_project).delete(delete_project),
        )
        .route("/api/v1/agents", get(list_agents).post(create_agent))
        .route(
            "/api/v1/agents/{id}",
            get(get_agent).put(update_agent).delete(delete_agent),
        )
        .route(
            "/api/v1/model_profiles",
            get(list_profiles).post(create_profile),
        )
        .route(
            "/api/v1/model_profiles/{id}",
            get(get_profile).put(update_profile).delete(delete_profile),
        )
        .route(
            "/api/v1/harnesses",
            get(list_harnesses).post(create_harness),
        )
        .route(
            "/api/v1/harnesses/{id}",
            get(get_harness).put(update_harness).delete(delete_harness),
        )
        // Read-only collections
        .route("/api/v1/tasks", get(list_tasks))
        .route("/api/v1/tasks/{id}", get(get_task))
        .route("/api/v1/runs", get(list_runs))
        .route("/api/v1/runs/{id}", get(get_run))
        .route("/api/v1/signals", get(list_signals))
        .route("/api/v1/memory", get(list_memory))
        .route("/api/v1/signals/manual", post(post_manual_signal))
        // Chat
        .route(
            "/api/v1/conversations",
            get(list_conversations).post(create_conversation),
        )
        .route(
            "/api/v1/conversations/{id}",
            get(get_conversation).delete(delete_conversation),
        )
        .route("/api/v1/conversations/{id}/messages", post(post_message))
        .route(
            "/api/v1/conversations/{id}/cancel",
            post(cancel_conversation),
        )
        .route(
            "/api/v1/conversations/{id}/config",
            get(get_conversation_config).post(set_conversation_config),
        )
        // Permissions
        .route("/api/v1/permissions", get(list_permissions))
        .route("/api/v1/permissions/{id}", post(resolve_permission))
        .with_state(state);

    let app = Router::new()
        .merge(api)
        .layer(tower_http::cors::CorsLayer::permissive());

    match ui_dir.filter(|d| d.join("index.html").exists()) {
        Some(dir) => {
            let index = dir.join("index.html");
            app.fallback_service(
                tower_http::services::ServeDir::new(dir)
                    .fallback(tower_http::services::ServeFile::new(index)),
            )
        }
        None => app,
    }
}

async fn health() -> &'static str {
    "ok"
}

// ---------- SSE ----------

#[derive(Debug, Deserialize)]
struct EventsQuery {
    conversation_id: Option<ConversationId>,
}

fn event_conversation(e: &DomainEvent) -> Option<ConversationId> {
    match e {
        DomainEvent::ConversationCreated { conversation } => Some(conversation.id),
        DomainEvent::ConversationStatusChanged {
            conversation_id, ..
        } => Some(*conversation_id),
        DomainEvent::ConversationConfigChanged {
            conversation_id, ..
        } => Some(*conversation_id),
        DomainEvent::MessageAppended {
            conversation_id, ..
        }
        | DomainEvent::MessageUpdated {
            conversation_id, ..
        } => Some(*conversation_id),
        DomainEvent::MessageDelta {
            conversation_id, ..
        } => Some(*conversation_id),
        DomainEvent::PermissionRequested { request } => request.conversation_id,
        _ => None,
    }
}

async fn events_sse(
    State(state): State<AppState>,
    headers: HeaderMap,
    Query(q): Query<EventsQuery>,
) -> Sse<impl Stream<Item = Result<Event, Infallible>>> {
    let last_id: u64 = headers
        .get("last-event-id")
        .and_then(|v| v.to_str().ok())
        .and_then(|s| s.parse().ok())
        .unwrap_or(0);
    let replay = state.events.replay_after(last_id);
    let live = BroadcastStream::new(state.events.subscribe());
    let conv_filter = q.conversation_id;
    let mut shutdown = state.shutdown.clone();
    // Open SSE connections would otherwise keep graceful shutdown waiting
    // forever — `take_until` (futures::StreamExt; tokio-stream has none)
    // ends the stream when shutdown starts.
    let stream = futures::StreamExt::take_until(
        stream::iter(replay).chain(live.filter_map(std::result::Result::ok)),
        async move {
            let _ = shutdown.wait_for(|v| *v).await;
        },
    )
    .filter_map(move |envelope| {
        let keep = match conv_filter {
            Some(id) => event_conversation(&envelope.event) == Some(id),
            None => true,
        };
        if !keep {
            return None;
        }
        let data = serde_json::to_string(&envelope).unwrap_or_default();
        Some(Ok(Event::default().id(envelope.id.to_string()).data(data)))
    });
    Sse::new(stream).keep_alive(KeepAlive::default())
}

// ---------- CRUD macro-free helpers ----------

macro_rules! crud_list_get {
    ($list_fn:ident, $get_fn:ident, $entity:ty, $id:ty, $list:ident, $get:ident) => {
        async fn $list_fn(State(s): State<AppState>) -> ApiResult<Json<Vec<$entity>>> {
            s.store.$list().await.map(Json).map_err(store_err)
        }
        async fn $get_fn(
            State(s): State<AppState>,
            Path(id): Path<$id>,
        ) -> ApiResult<Json<$entity>> {
            s.store
                .$get(id)
                .await
                .map_err(store_err)?
                .map(Json)
                .ok_or_else(|| not_found(stringify!($entity)))
        }
    };
}

crud_list_get!(
    list_orgs,
    get_org,
    Organization,
    OrganizationId,
    list_organizations,
    get_organization
);
crud_list_get!(
    list_repos,
    get_repo,
    Repository,
    RepositoryId,
    list_repositories,
    get_repository
);
crud_list_get!(
    list_projects,
    get_project,
    Project,
    ProjectId,
    list_projects,
    get_project
);
crud_list_get!(
    list_agents,
    get_agent,
    Agent,
    AgentId,
    list_agents,
    get_agent
);
crud_list_get!(
    list_profiles,
    get_profile,
    ModelProfile,
    ModelProfileId,
    list_model_profiles,
    get_model_profile
);
crud_list_get!(
    list_harnesses,
    get_harness,
    Harness,
    HarnessId,
    list_harnesses,
    get_harness
);
crud_list_get!(list_tasks, get_task, Task, TaskId, list_tasks, get_task);
crud_list_get!(list_runs, get_run, Run, RunId, list_runs, get_run);

async fn list_signals(State(s): State<AppState>) -> ApiResult<Json<Vec<Signal>>> {
    s.store.list_signals().await.map(Json).map_err(store_err)
}

async fn list_memory(State(s): State<AppState>) -> ApiResult<Json<Vec<MemoryEntry>>> {
    s.store
        .list_memory_entries()
        .await
        .map(Json)
        .map_err(store_err)
}

// ---------- Organizations ----------

async fn create_org(
    State(s): State<AppState>,
    Json(body): Json<UpsertOrganization>,
) -> ApiResult<Json<Organization>> {
    if s.store
        .find_organization_by_slug(&body.slug)
        .await
        .map_err(store_err)?
        .is_some()
    {
        let mut f = BTreeMap::new();
        f.insert("slug".into(), "already exists".into());
        return Err(err_fields("validation failed", f));
    }
    let org = Organization {
        id: OrganizationId::new(),
        name: body.name,
        slug: body.slug,
        created_at: chrono::Utc::now(),
    };
    s.store.insert_organization(&org).await.map_err(store_err)?;
    changed(&s, EntityKind::Organization, org.id);
    Ok(Json(org))
}

async fn update_org(
    State(s): State<AppState>,
    Path(id): Path<OrganizationId>,
    Json(body): Json<UpsertOrganization>,
) -> ApiResult<Json<Organization>> {
    let mut org = s
        .store
        .get_organization(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("organization"))?;
    if let Some(other) = s
        .store
        .find_organization_by_slug(&body.slug)
        .await
        .map_err(store_err)?
        && other.id != id
    {
        let mut f = BTreeMap::new();
        f.insert("slug".into(), "already exists".into());
        return Err(err_fields("validation failed", f));
    }
    org.name = body.name;
    org.slug = body.slug;
    s.store.update_organization(&org).await.map_err(store_err)?;
    changed(&s, EntityKind::Organization, org.id);
    Ok(Json(org))
}

async fn delete_org(
    State(s): State<AppState>,
    Path(id): Path<OrganizationId>,
) -> ApiResult<StatusCode> {
    s.store
        .get_organization(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("organization"))?;
    s.store.delete_organization(id).await.map_err(store_err)?;
    changed(&s, EntityKind::Organization, id);
    Ok(StatusCode::NO_CONTENT)
}

// ---------- Repositories ----------

async fn create_repo(
    State(s): State<AppState>,
    Json(body): Json<CreateRepository>,
) -> ApiResult<Json<Repository>> {
    s.store
        .get_organization(body.organization_id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| {
            let mut f = BTreeMap::new();
            f.insert("organization_id".into(), "unknown organization".into());
            err_fields("validation failed", f)
        })?;
    if s.store
        .find_repository_by_owner_name(&body.owner, &body.name)
        .await
        .map_err(store_err)?
        .is_some()
    {
        let mut f = BTreeMap::new();
        f.insert("name".into(), "owner/name already exists".into());
        return Err(err_fields("validation failed", f));
    }
    let repo = Repository {
        id: RepositoryId::new(),
        organization_id: body.organization_id,
        owner: body.owner,
        name: body.name,
        provider: body.provider.unwrap_or(RepoProvider::GitHub),
        default_branch: body.default_branch.unwrap_or_else(|| "main".into()),
        local_path: body.local_path.map(PathBuf::from),
        created_at: chrono::Utc::now(),
    };
    s.store.insert_repository(&repo).await.map_err(store_err)?;
    changed(&s, EntityKind::Repository, repo.id);
    Ok(Json(repo))
}

async fn update_repo(
    State(s): State<AppState>,
    Path(id): Path<RepositoryId>,
    Json(body): Json<UpdateRepository>,
) -> ApiResult<Json<Repository>> {
    let mut repo = s
        .store
        .get_repository(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("repository"))?;
    if let Some(other) = s
        .store
        .find_repository_by_owner_name(&body.owner, &body.name)
        .await
        .map_err(store_err)?
        && other.id != id
    {
        let mut f = BTreeMap::new();
        f.insert("name".into(), "owner/name already exists".into());
        return Err(err_fields("validation failed", f));
    }
    repo.owner = body.owner;
    repo.name = body.name;
    repo.provider = body.provider;
    repo.default_branch = body.default_branch;
    repo.local_path = body.local_path.map(PathBuf::from);
    s.store.update_repository(&repo).await.map_err(store_err)?;
    changed(&s, EntityKind::Repository, id);
    Ok(Json(repo))
}

async fn delete_repo(
    State(s): State<AppState>,
    Path(id): Path<RepositoryId>,
) -> ApiResult<StatusCode> {
    s.store
        .get_repository(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("repository"))?;
    s.store.delete_repository(id).await.map_err(store_err)?;
    changed(&s, EntityKind::Repository, id);
    Ok(StatusCode::NO_CONTENT)
}

// ---------- Projects ----------

async fn create_project(
    State(s): State<AppState>,
    Json(body): Json<CreateProject>,
) -> ApiResult<Json<Project>> {
    s.store
        .get_repository(body.repository_id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| {
            let mut f = BTreeMap::new();
            f.insert("repository_id".into(), "unknown repository".into());
            err_fields("validation failed", f)
        })?;
    if s.store
        .find_project_by_slug(body.repository_id, &body.slug)
        .await
        .map_err(store_err)?
        .is_some()
    {
        let mut f = BTreeMap::new();
        f.insert("slug".into(), "already exists in repository".into());
        return Err(err_fields("validation failed", f));
    }
    let project = Project {
        id: ProjectId::new(),
        repository_id: body.repository_id,
        name: body.name,
        slug: body.slug,
        description: body.description,
        scope: body.scope,
        status: body.status.unwrap_or(ProjectStatus::Active),
        created_at: chrono::Utc::now(),
    };
    s.store.insert_project(&project).await.map_err(store_err)?;
    changed(&s, EntityKind::Project, project.id);
    Ok(Json(project))
}

async fn update_project(
    State(s): State<AppState>,
    Path(id): Path<ProjectId>,
    Json(body): Json<UpdateProject>,
) -> ApiResult<Json<Project>> {
    let mut project = s
        .store
        .get_project(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("project"))?;
    if let Some(other) = s
        .store
        .find_project_by_slug(project.repository_id, &body.slug)
        .await
        .map_err(store_err)?
        && other.id != id
    {
        let mut f = BTreeMap::new();
        f.insert("slug".into(), "already exists in repository".into());
        return Err(err_fields("validation failed", f));
    }
    project.name = body.name;
    project.slug = body.slug;
    project.description = body.description;
    project.scope = body.scope;
    project.status = body.status;
    s.store.update_project(&project).await.map_err(store_err)?;
    changed(&s, EntityKind::Project, id);
    Ok(Json(project))
}

async fn delete_project(
    State(s): State<AppState>,
    Path(id): Path<ProjectId>,
) -> ApiResult<StatusCode> {
    s.store
        .get_project(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("project"))?;
    s.store.delete_project(id).await.map_err(store_err)?;
    changed(&s, EntityKind::Project, id);
    Ok(StatusCode::NO_CONTENT)
}

// ---------- Model profiles ----------

async fn validate_profile_refs(
    s: &AppState,
    harness_id: HarnessId,
) -> Result<(), (StatusCode, Json<ApiError>)> {
    match s.store.get_harness(harness_id).await.map_err(store_err)? {
        None => {
            let mut f = BTreeMap::new();
            f.insert("harness_id".into(), "unknown harness".into());
            Err(err_fields("validation failed", f))
        }
        Some(h) if !h.enabled => {
            let mut f = BTreeMap::new();
            f.insert("harness_id".into(), "harness is disabled".into());
            Err(err_fields("validation failed", f))
        }
        Some(_) => Ok(()),
    }
}

async fn create_profile(
    State(s): State<AppState>,
    Json(body): Json<CreateModelProfile>,
) -> ApiResult<Json<ModelProfile>> {
    validate_profile_refs(&s, body.harness_id).await?;
    if s.store
        .find_model_profile_by_name(&body.name)
        .await
        .map_err(store_err)?
        .is_some()
    {
        let mut f = BTreeMap::new();
        f.insert("name".into(), "already exists".into());
        return Err(err_fields("validation failed", f));
    }
    let profile = ModelProfile {
        id: ModelProfileId::new(),
        name: body.name,
        harness_id: body.harness_id,
        model: body.model,
        effort: body.effort,
        config: body.config,
        created_at: chrono::Utc::now(),
    };
    s.store
        .insert_model_profile(&profile)
        .await
        .map_err(store_err)?;
    changed(&s, EntityKind::ModelProfile, profile.id);
    Ok(Json(profile))
}

async fn update_profile(
    State(s): State<AppState>,
    Path(id): Path<ModelProfileId>,
    Json(body): Json<UpdateModelProfile>,
) -> ApiResult<Json<ModelProfile>> {
    let mut profile = s
        .store
        .get_model_profile(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("model profile"))?;
    validate_profile_refs(&s, body.harness_id).await?;
    if let Some(other) = s
        .store
        .find_model_profile_by_name(&body.name)
        .await
        .map_err(store_err)?
        && other.id != id
    {
        let mut f = BTreeMap::new();
        f.insert("name".into(), "already exists".into());
        return Err(err_fields("validation failed", f));
    }
    profile.name = body.name;
    profile.harness_id = body.harness_id;
    profile.model = body.model;
    profile.effort = body.effort;
    profile.config = body.config;
    s.store
        .update_model_profile(&profile)
        .await
        .map_err(store_err)?;
    changed(&s, EntityKind::ModelProfile, id);
    Ok(Json(profile))
}

async fn delete_profile(
    State(s): State<AppState>,
    Path(id): Path<ModelProfileId>,
) -> ApiResult<StatusCode> {
    s.store
        .get_model_profile(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("model profile"))?;
    s.store.delete_model_profile(id).await.map_err(store_err)?;
    changed(&s, EntityKind::ModelProfile, id);
    Ok(StatusCode::NO_CONTENT)
}

// ---------- Harnesses ----------

async fn create_harness(
    State(s): State<AppState>,
    Json(body): Json<CreateHarness>,
) -> ApiResult<Json<Harness>> {
    if s.store
        .find_harness_by_name(&body.name)
        .await
        .map_err(store_err)?
        .is_some()
    {
        let mut f = BTreeMap::new();
        f.insert("name".into(), "already exists".into());
        return Err(err_fields("validation failed", f));
    }
    let harness = Harness {
        id: HarnessId::new(),
        name: body.name,
        command: body.command,
        args: body.args,
        env: body.env,
        default_permission_policy: body
            .default_permission_policy
            .unwrap_or(PermissionPolicy::AskHuman),
        model_arg_template: body.model_arg_template,
        enabled: body.enabled.unwrap_or(true),
        created_at: chrono::Utc::now(),
    };
    s.store.insert_harness(&harness).await.map_err(store_err)?;
    changed(&s, EntityKind::Harness, harness.id);
    Ok(Json(harness))
}

async fn update_harness(
    State(s): State<AppState>,
    Path(id): Path<HarnessId>,
    Json(body): Json<UpdateHarness>,
) -> ApiResult<Json<Harness>> {
    let mut harness = s
        .store
        .get_harness(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("harness"))?;
    if let Some(other) = s
        .store
        .find_harness_by_name(&body.name)
        .await
        .map_err(store_err)?
        && other.id != id
    {
        let mut f = BTreeMap::new();
        f.insert("name".into(), "already exists".into());
        return Err(err_fields("validation failed", f));
    }
    harness.name = body.name;
    harness.command = body.command;
    harness.args = body.args;
    harness.env = body.env;
    harness.default_permission_policy = body.default_permission_policy;
    harness.model_arg_template = body.model_arg_template;
    harness.enabled = body.enabled;
    s.store.update_harness(&harness).await.map_err(store_err)?;
    changed(&s, EntityKind::Harness, id);
    Ok(Json(harness))
}

async fn delete_harness(
    State(s): State<AppState>,
    Path(id): Path<HarnessId>,
) -> ApiResult<StatusCode> {
    s.store
        .get_harness(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("harness"))?;
    s.store.delete_harness(id).await.map_err(store_err)?;
    changed(&s, EntityKind::Harness, id);
    Ok(StatusCode::NO_CONTENT)
}

// ---------- Agents ----------

async fn validate_agent_refs(
    s: &AppState,
    default_profile: ModelProfileId,
    overrides: &BTreeMap<Activity, ModelProfileId>,
) -> Result<(), (StatusCode, Json<ApiError>)> {
    let mut fields = BTreeMap::new();
    if s.store
        .get_model_profile(default_profile)
        .await
        .map_err(store_err)?
        .is_none()
    {
        fields.insert("default_profile".into(), "unknown model profile".into());
    }
    for (activity, pid) in overrides {
        if s.store
            .get_model_profile(*pid)
            .await
            .map_err(store_err)?
            .is_none()
        {
            fields.insert(
                format!("profile_overrides.{activity}"),
                "unknown model profile".into(),
            );
        }
    }
    if fields.is_empty() {
        Ok(())
    } else {
        Err(err_fields("validation failed", fields))
    }
}

async fn create_agent(
    State(s): State<AppState>,
    Json(body): Json<CreateAgent>,
) -> ApiResult<Json<Agent>> {
    validate_agent_refs(&s, body.default_profile, &body.profile_overrides).await?;
    if s.store
        .find_agent_by_name(&body.name)
        .await
        .map_err(store_err)?
        .is_some()
    {
        let mut f = BTreeMap::new();
        f.insert("name".into(), "already exists".into());
        return Err(err_fields("validation failed", f));
    }
    let agent = Agent {
        id: AgentId::new(),
        name: body.name,
        role: body.role,
        profiles: ActivityProfiles {
            default: body.default_profile,
            overrides: body.profile_overrides,
        },
        project_id: body.project_id,
        repository_id: body.repository_id,
        instructions: body.instructions,
        permission_policy: body.permission_policy.unwrap_or(PermissionPolicy::AskHuman),
        status: body.status.unwrap_or(AgentStatus::Idle),
        created_at: chrono::Utc::now(),
    };
    s.store.insert_agent(&agent).await.map_err(store_err)?;
    changed(&s, EntityKind::Agent, agent.id);
    Ok(Json(agent))
}

async fn update_agent(
    State(s): State<AppState>,
    Path(id): Path<AgentId>,
    Json(body): Json<UpdateAgent>,
) -> ApiResult<Json<Agent>> {
    let mut agent = s
        .store
        .get_agent(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("agent"))?;
    validate_agent_refs(&s, body.default_profile, &body.profile_overrides).await?;
    if let Some(other) = s
        .store
        .find_agent_by_name(&body.name)
        .await
        .map_err(store_err)?
        && other.id != id
    {
        let mut f = BTreeMap::new();
        f.insert("name".into(), "already exists".into());
        return Err(err_fields("validation failed", f));
    }
    agent.name = body.name;
    agent.role = body.role;
    agent.profiles = ActivityProfiles {
        default: body.default_profile,
        overrides: body.profile_overrides,
    };
    agent.project_id = body.project_id;
    agent.repository_id = body.repository_id;
    agent.instructions = body.instructions;
    agent.permission_policy = body.permission_policy;
    agent.status = body.status;
    s.store.update_agent(&agent).await.map_err(store_err)?;
    changed(&s, EntityKind::Agent, id);
    Ok(Json(agent))
}

async fn delete_agent(State(s): State<AppState>, Path(id): Path<AgentId>) -> ApiResult<StatusCode> {
    s.store
        .get_agent(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("agent"))?;
    s.store.delete_agent(id).await.map_err(store_err)?;
    changed(&s, EntityKind::Agent, id);
    Ok(StatusCode::NO_CONTENT)
}

// ---------- Signals ----------

async fn post_manual_signal(
    State(s): State<AppState>,
    Json(body): Json<ManualSignal>,
) -> ApiResult<StatusCode> {
    s.manual
        .push(mobius_ingest::ManualSignal {
            title: body.title,
            body: body.body,
            repository_id: body.repository_id,
        })
        .await
        .map_err(|e| err(StatusCode::INTERNAL_SERVER_ERROR, e.to_string()))?;
    Ok(StatusCode::ACCEPTED)
}

// ---------- Conversations ----------

async fn list_conversations(State(s): State<AppState>) -> ApiResult<Json<Vec<Conversation>>> {
    s.store
        .list_conversations()
        .await
        .map(Json)
        .map_err(store_err)
}

async fn create_conversation(
    State(s): State<AppState>,
    Json(body): Json<CreateConversation>,
) -> ApiResult<Json<Conversation>> {
    // Default workdir: the agent's repository local checkout.
    let workdir = if body.workdir.trim().is_empty() {
        let agent = s
            .store
            .get_agent(body.agent_id)
            .await
            .map_err(store_err)?
            .ok_or_else(|| not_found("agent"))?;
        match agent.repository_id {
            Some(rid) => s
                .store
                .get_repository(rid)
                .await
                .map_err(store_err)?
                .and_then(|r| r.local_path)
                .unwrap_or_else(std::env::temp_dir),
            None => std::env::temp_dir(),
        }
    } else {
        PathBuf::from(body.workdir)
    };
    s.sessions
        .open_conversation(
            body.agent_id,
            workdir,
            body.title.unwrap_or_else(|| "chat".into()),
        )
        .await
        .map(Json)
        .map_err(orch_err)
}

async fn get_conversation(
    State(s): State<AppState>,
    Path(id): Path<ConversationId>,
) -> ApiResult<Json<ConversationView>> {
    let conversation = s
        .store
        .get_conversation(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("conversation"))?;
    let messages = s
        .store
        .list_messages_by_conversation(id)
        .await
        .map_err(store_err)?;
    Ok(Json(ConversationView {
        conversation,
        messages,
    }))
}

async fn delete_conversation(
    State(s): State<AppState>,
    Path(id): Path<ConversationId>,
) -> ApiResult<StatusCode> {
    s.sessions.close(id).await.map_err(orch_err)?;
    s.store.delete_conversation(id).await.map_err(store_err)?;
    changed(&s, EntityKind::Conversation, id);
    Ok(StatusCode::NO_CONTENT)
}

async fn post_message(
    State(s): State<AppState>,
    Path(id): Path<ConversationId>,
    Json(body): Json<PostMessage>,
) -> ApiResult<Json<Message>> {
    s.sessions
        .send_message(id, &body.text)
        .await
        .map(Json)
        .map_err(orch_err)
}

async fn cancel_conversation(
    State(s): State<AppState>,
    Path(id): Path<ConversationId>,
) -> ApiResult<StatusCode> {
    s.sessions.cancel(id).await.map_err(orch_err)?;
    Ok(StatusCode::OK)
}

async fn get_conversation_config(
    State(s): State<AppState>,
    Path(id): Path<ConversationId>,
) -> ApiResult<Json<Vec<SessionConfigOption>>> {
    // Prefer the live session's options; fall back to the persisted row.
    if let Some(session) = s.sessions.live_session(id).await {
        let opts = session.config_options();
        if !opts.is_empty() {
            return Ok(Json(opts));
        }
    }
    let conv = s
        .store
        .get_conversation(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("conversation"))?;
    Ok(Json(conv.config_options))
}

async fn set_conversation_config(
    State(s): State<AppState>,
    Path(id): Path<ConversationId>,
    Json(body): Json<SetConversationConfig>,
) -> ApiResult<Json<Vec<SessionConfigOption>>> {
    s.sessions
        .set_conversation_config(id, &body.config_id, &body.value)
        .await
        .map(Json)
        .map_err(orch_err)
}

// ---------- Permissions ----------

#[derive(Debug, Deserialize)]
struct PermissionsQuery {
    status: Option<String>,
}

async fn list_permissions(
    State(s): State<AppState>,
    Query(q): Query<PermissionsQuery>,
) -> ApiResult<Json<Vec<PermissionRequest>>> {
    let all = match q.status.as_deref() {
        Some("pending") => s.store.list_pending_permission_requests().await,
        _ => s.store.list_permission_requests().await,
    }
    .map_err(store_err)?;
    Ok(Json(all))
}

async fn resolve_permission(
    State(s): State<AppState>,
    Path(id): Path<PermissionRequestId>,
    Json(body): Json<ResolvePermission>,
) -> ApiResult<StatusCode> {
    let req = s
        .store
        .get_permission_request(id)
        .await
        .map_err(store_err)?
        .ok_or_else(|| not_found("permission request"))?;

    if req.conversation_id.is_some() {
        s.sessions
            .resolve_permission(id, &body.option_id)
            .await
            .map_err(orch_err)?;
        return Ok(StatusCode::OK);
    }

    // Run-scoped permission: resolve directly against the run session.
    if let Some(run_id) = req.run_id {
        let session = s.dispatcher.run_session(run_id).await.ok_or_else(|| {
            err(
                StatusCode::CONFLICT,
                format!("run {run_id} has no live session"),
            )
        })?;
        if !session.respond_permission(&id.to_string(), &body.option_id) {
            return Err(err(
                StatusCode::CONFLICT,
                "no parked permission on run session",
            ));
        }
        let mut req = req;
        req.status = PermissionRequestStatus::Resolved {
            option_id: body.option_id.clone(),
        };
        req.resolved_at = Some(chrono::Utc::now());
        s.store
            .update_permission_request(&req)
            .await
            .map_err(store_err)?;
        s.events.publish(DomainEvent::PermissionResolved {
            permission_request_id: id,
            option_id: body.option_id,
        });
        return Ok(StatusCode::OK);
    }

    Err(err(
        StatusCode::CONFLICT,
        "permission request is not attached to a live session",
    ))
}
