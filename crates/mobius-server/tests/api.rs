//! In-process axum integration tests over a temp-file `SqliteStore`.

use axum::Router;
use axum::body::Body;
use axum::http::{Request, StatusCode};
use http_body_util::BodyExt;
use mobius_core::*;
use mobius_server::events::EventBus;
use mobius_server::routes::router;
use mobius_server::state::{AppState, emit_for};
use mobius_store::SqliteStore;
use serde_json::{Value, json};
use std::sync::Arc;
use tempfile::TempDir;
use tower::ServiceExt;

struct TestApp {
    app: Router,
    store: Arc<SqliteStore>,
    _dir: TempDir,
    seed: mobius_store::DevSeed,
}

async fn test_app() -> TestApp {
    let dir = tempfile::tempdir().expect("tempdir");
    let data_dir = dir.path().join("data");
    std::fs::create_dir_all(&data_dir).expect("data dir");
    let store = Arc::new(
        SqliteStore::open(data_dir.join("test.db"))
            .await
            .expect("store"),
    );
    mobius_store::ensure_default_harnesses(store.as_ref())
        .await
        .expect("harnesses");
    let repo_dir = dir.path().join("checkout");
    std::fs::create_dir_all(&repo_dir).expect("repo dir");
    let seed = mobius_store::dev_fixture(store.as_ref(), &repo_dir)
        .await
        .expect("fixture");

    let events = Arc::new(EventBus::new());
    let emit = emit_for(events.clone());
    let sessions = Arc::new(mobius_orchestrator::SessionManager::new(
        store.clone(),
        emit.clone(),
        data_dir.clone(),
    ));
    let dispatcher = Arc::new(mobius_orchestrator::Dispatcher::new(
        store.clone(),
        emit.clone(),
        sessions.clone(),
        data_dir.join("worktrees"),
    ));
    let research = Arc::new(mobius_orchestrator::ResearchService::new(
        store.clone(),
        emit.clone(),
        sessions.clone(),
        data_dir.clone(),
    ));
    let (_source, manual) = mobius_ingest::manual_source();
    let (_tx, shutdown) = tokio::sync::watch::channel(false);
    let state = AppState {
        store: store.clone(),
        events,
        sessions,
        dispatcher,
        research,
        manual,
        shutdown,
    };
    TestApp {
        app: router(state, None),
        store,
        _dir: dir,
        seed,
    }
}

async fn call(app: &Router, req: Request<Body>) -> (StatusCode, Value) {
    let res = app.clone().oneshot(req).await.expect("response");
    let status = res.status();
    let bytes = res.into_body().collect().await.expect("body").to_bytes();
    let body = if bytes.is_empty() {
        Value::Null
    } else {
        serde_json::from_slice(&bytes).expect("json body")
    };
    (status, body)
}

fn get(uri: &str) -> Request<Body> {
    Request::get(uri).body(Body::empty()).expect("request")
}

fn post(uri: &str, body: Value) -> Request<Body> {
    Request::post(uri)
        .header("content-type", "application/json")
        .body(Body::from(body.to_string()))
        .expect("request")
}

#[tokio::test]
async fn task_create_list_and_view_with_child() {
    let t = test_app().await;
    let project = &t.seed.projects[0];

    let (status, task) = call(
        &t.app,
        post(
            "/api/v1/tasks",
            json!({
                "project_id": project.id,
                "repository_id": t.seed.repository.id,
                "title": "parent",
                "description": "do the thing",
                "kind": "feature",
            }),
        ),
    )
    .await;
    assert_eq!(status, StatusCode::CREATED, "{task}");
    let task_id = task["id"].as_str().expect("task id").to_string();

    let (status, child) = call(
        &t.app,
        post(
            "/api/v1/tasks",
            json!({
                "project_id": project.id,
                "title": "child",
                "parent_task_id": task_id,
            }),
        ),
    )
    .await;
    assert_eq!(status, StatusCode::CREATED, "{child}");

    let (status, tasks) = call(
        &t.app,
        get(&format!("/api/v1/tasks?project_id={}", project.id)),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(tasks.as_array().expect("array").len(), 2);

    let (status, view) = call(&t.app, get(&format!("/api/v1/tasks/{task_id}"))).await;
    assert_eq!(status, StatusCode::OK, "{view}");
    assert_eq!(view["title"], "parent");
    assert_eq!(view["children"].as_array().expect("children").len(), 1);
    assert_eq!(view["runs"].as_array().expect("runs").len(), 0);
}

#[tokio::test]
async fn run_task_without_repository_is_422() {
    let t = test_app().await;
    let project = &t.seed.projects[0];

    let (status, task) = call(
        &t.app,
        post(
            "/api/v1/tasks",
            json!({
                "project_id": project.id,
                "title": "no repo",
            }),
        ),
    )
    .await;
    assert_eq!(status, StatusCode::CREATED, "{task}");
    let task_id = task["id"].as_str().expect("task id").to_string();

    let (status, body) = call(
        &t.app,
        Request::post(format!("/api/v1/tasks/{task_id}/run"))
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY, "{body}");
}

#[tokio::test]
async fn memory_add_list_delete() {
    let t = test_app().await;
    let org_id = t.seed.organization.id;

    let (status, entry) = call(
        &t.app,
        post(
            "/api/v1/memory",
            json!({
                "scope": { "level": "organization", "id": org_id },
                "kind": "fact",
                "content": "sky is blue",
            }),
        ),
    )
    .await;
    assert_eq!(status, StatusCode::CREATED, "{entry}");
    let entry_id = entry["id"].as_str().expect("entry id").to_string();

    let (status, entries) = call(
        &t.app,
        get(&format!("/api/v1/memory?scope=organization:{org_id}")),
    )
    .await;
    assert_eq!(status, StatusCode::OK, "{entries}");
    assert_eq!(entries.as_array().expect("array").len(), 1);

    let (status, _) = call(
        &t.app,
        Request::delete(format!("/api/v1/memory/{entry_id}"))
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(status, StatusCode::NO_CONTENT);

    let (status, _) = call(
        &t.app,
        Request::delete(format!("/api/v1/memory/{entry_id}"))
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(status, StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn research_start_requires_local_checkout() {
    let t = test_app().await;
    // A repository row with no local_path fails validation before any
    // harness is spawned.
    let repo = Repository {
        id: RepositoryId::new(),
        organization_id: t.seed.organization.id,
        owner: "acme".into(),
        name: "remote-only".into(),
        provider: RepoProvider::GitHub,
        default_branch: "main".into(),
        local_path: None,
        created_at: chrono::Utc::now(),
    };
    t.store.insert_repository(&repo).await.expect("insert repo");

    let (status, body) = call(
        &t.app,
        post(
            "/api/v1/research",
            json!({
                "repositories": [repo.id],
                "question": "where is the config?",
            }),
        ),
    )
    .await;
    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY, "{body}");
}
