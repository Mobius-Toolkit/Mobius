use axum::body::Body;
use axum::http::{Request, StatusCode};
use dioxus::server::axum::Extension;
use mobius_engine::{Engine, auth, github, workstreams};
use mobius_testkit::fake_github::FakeGitHub;
use mobius_testkit::{start, wait_for};
use serde_json::json;
use std::sync::Once;
use tempfile::TempDir;
use tower::ServiceExt;

const REPOSITORY: &str = "owner/shop";
static PUBLIC_PATH: Once = Once::new();

async fn connect(data_dir: &TempDir, github: &FakeGitHub) -> Engine {
    github.add_manifest_code("manifest-code");
    github.add_repository(REPOSITORY);
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    let engine = start(data_dir.path(), "correct horse", &github.url).await;
    github::convert_manifest(&engine, "manifest-code")
        .await
        .unwrap();
    wait_for(async || (!workstreams::list(&engine).await.unwrap().is_empty()).then_some(())).await;
    engine
}

// POSTs to the server function endpoint the same way as the web client of the task tab.
async fn task_list(engine: &Engine, token: &str, repository: &str, workstream: i64) -> String {
    // `router` serves the web bundle of a `dx` build from DIOXUS_PUBLIC_PATH; a test has none.
    // The variable is global to the process, so it points to one directory that no test deletes.
    PUBLIC_PATH.call_once(|| {
        let public = TempDir::new().unwrap().keep();
        unsafe { std::env::set_var("DIOXUS_PUBLIC_PATH", public) };
    });
    let router = dioxus::server::router(mobius_ui::App)
        .layer(Extension(engine.clone()))
        .layer(Extension(engine.store.clone()));
    let response = router
        .oneshot(
            Request::post("/api/tasks")
                .header("content-type", "application/json")
                .header("cookie", format!("mobius_session={token}"))
                .body(Body::from(
                    json!({ "repository": repository, "workstream": workstream }).to_string(),
                ))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .unwrap();
    String::from_utf8(body.to_vec()).unwrap()
}

#[tokio::test]
async fn the_task_tab_shows_the_sub_issues_of_the_workstream() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github).await;
    github.add_issue(REPOSITORY, 41, "Add plan model");
    github.add_sub_issue(REPOSITORY, 12, 41);
    github.add_issue(REPOSITORY, 50, "Store the price in cents");
    github.add_sub_issue(REPOSITORY, 41, 50);
    github.add_issue(REPOSITORY, 42, "Let customers change plans");
    github.add_sub_issue(REPOSITORY, 12, 42);
    let token = auth::login(&engine, "correct horse", "test")
        .await
        .unwrap()
        .unwrap();

    let body = task_list(&engine, &token, REPOSITORY, 12).await;

    // The nested task follows its parent and each level adds one to the depth.
    let tasks: serde_json::Value = serde_json::from_str(&body).unwrap();
    assert_eq!(
        tasks,
        json!([
            {
                "number": 41,
                "title": "Add plan model",
                "state": "open",
                "url": "https://github.com/owner/shop/issues/41",
                "depth": 0,
                "blocked_by": []
            },
            {
                "number": 50,
                "title": "Store the price in cents",
                "state": "open",
                "url": "https://github.com/owner/shop/issues/50",
                "depth": 1,
                "blocked_by": []
            },
            {
                "number": 42,
                "title": "Let customers change plans",
                "state": "open",
                "url": "https://github.com/owner/shop/issues/42",
                "depth": 0,
                "blocked_by": []
            }
        ]),
        "{body}"
    );
}

// A sub-issue that lives in another repository is a task of the Workstream, but
// its number names a different issue here, so this repository cannot give its
// blockers, its task state, or its children.
#[tokio::test]
async fn the_task_tab_shows_a_sub_issue_of_another_repository() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github).await;
    github.add_issue(REPOSITORY, 41, "Add plan model");
    github.add_sub_issue(REPOSITORY, 12, 41);
    github.add_issue("other/repo", 70, "A blocker in another repository");
    github.add_issue("other/repo", 77, "A task in another repository");
    github.add_blocker("other/repo", 77, 70);
    github.add_label("other/repo", 77, "mobius:working", "owner");
    github.add_foreign_sub_issue(REPOSITORY, 12, "other/repo", 77);
    // The different issue 77 of this repository has a sub-issue, a blocker,
    // and a queued task. A walk of the foreign 77 through this repository
    // would show them.
    github.add_issue(REPOSITORY, 77, "A different issue");
    github.add_issue(REPOSITORY, 90, "Task of the different issue");
    github.add_sub_issue(REPOSITORY, 77, 90);
    github.add_blocker(REPOSITORY, 77, 41);
    sqlx::query(
        "INSERT INTO tasks (repository, issue, workstream, state, dispatched_at)
         VALUES (?, 77, 12, 'queued', '2026-09-30T00:00:00Z')",
    )
    .bind(REPOSITORY)
    .execute(&engine.store.pool)
    .await
    .unwrap();
    github.add_issue(REPOSITORY, 42, "Let customers change plans");
    github.add_sub_issue(REPOSITORY, 12, 42);
    let token = auth::login(&engine, "correct horse", "test")
        .await
        .unwrap()
        .unwrap();

    let body = task_list(&engine, &token, REPOSITORY, 12).await;

    let tasks: serde_json::Value = serde_json::from_str(&body).unwrap();
    assert_eq!(
        tasks,
        json!([
            {
                "number": 41,
                "title": "Add plan model",
                "state": "open",
                "url": "https://github.com/owner/shop/issues/41",
                "depth": 0,
                "blocked_by": []
            },
            {
                "number": 77,
                "title": "A task in another repository",
                "state": "working",
                "url": "https://github.com/other/repo/issues/77",
                "depth": 0,
                "blocked_by": []
            },
            {
                "number": 42,
                "title": "Let customers change plans",
                "state": "open",
                "url": "https://github.com/owner/shop/issues/42",
                "depth": 0,
                "blocked_by": []
            }
        ]),
        "{body}"
    );
}

// A closed or untrusted task still has its own sub-issues. They keep the depth
// of the hidden parent, so a nested task does not move below an unrelated
// sibling.
#[tokio::test]
async fn the_task_tab_shows_the_sub_issues_of_a_closed_task_at_their_own_depth() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github).await;
    github.add_issue(REPOSITORY, 40, "First task");
    github.add_sub_issue(REPOSITORY, 12, 40);
    github.add_issue(REPOSITORY, 41, "Closed task");
    github.add_sub_issue(REPOSITORY, 12, 41);
    github.add_issue(REPOSITORY, 50, "Task of the closed task");
    github.add_sub_issue(REPOSITORY, 41, 50);
    github.close_issue(REPOSITORY, 41);
    let token = auth::login(&engine, "correct horse", "test")
        .await
        .unwrap()
        .unwrap();

    let body = task_list(&engine, &token, REPOSITORY, 12).await;

    // 50 stays at depth 0 under its hidden parent 41; it does not indent below 40.
    let tasks: serde_json::Value = serde_json::from_str(&body).unwrap();
    assert_eq!(
        tasks,
        json!([
            {
                "number": 40,
                "title": "First task",
                "state": "open",
                "url": "https://github.com/owner/shop/issues/40",
                "depth": 0,
                "blocked_by": []
            },
            {
                "number": 50,
                "title": "Task of the closed task",
                "state": "open",
                "url": "https://github.com/owner/shop/issues/50",
                "depth": 0,
                "blocked_by": []
            }
        ]),
        "{body}"
    );
}
