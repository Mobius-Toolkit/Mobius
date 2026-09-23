//! Clap parsing, table rendering, and an in-process round-trip against a
//! real `mobius-server` router bound to an ephemeral port.

use clap::Parser;
use mobius_cli::{Cli, Commands, Scope, run};
use std::collections::HashMap;
use std::sync::Arc;

fn parse(args: &[&str]) -> Cli {
    Cli::try_parse_from(args).expect("parse")
}

#[test]
fn parses_every_subcommand() {
    assert!(matches!(
        parse(&["mobius", "project", "list"]).command,
        Commands::Project { .. }
    ));
    assert!(matches!(
        parse(&["mobius", "repo", "list"]).command,
        Commands::Repo { .. }
    ));
    assert!(matches!(
        parse(&[
            "mobius",
            "research",
            "start",
            "--question",
            "q",
            "--repo",
            "a/b",
            "--repo",
            "c/d",
            "--project",
            "chat",
            "--wait",
            "--timeout",
            "5",
        ])
        .command,
        Commands::Research { .. }
    ));
    assert!(matches!(
        parse(&["mobius", "research", "show", "x"]).command,
        Commands::Research { .. }
    ));
    assert!(matches!(
        parse(&["mobius", "research", "list", "--mine"]).command,
        Commands::Research { .. }
    ));
    assert!(matches!(
        parse(&["mobius", "research", "cancel", "x"]).command,
        Commands::Research { .. }
    ));
    assert!(matches!(
        parse(&[
            "mobius",
            "task",
            "create",
            "--title",
            "t",
            "--brief",
            "b",
            "--repo",
            "a/b",
            "--project",
            "chat",
            "--parent",
            "id",
            "--kind",
            "fix",
            "--priority",
            "high",
        ])
        .command,
        Commands::Task { .. }
    ));
    assert!(matches!(
        parse(&[
            "mobius",
            "task",
            "create",
            "--title",
            "t",
            "--brief-file",
            "f",
            "--repo",
            "a/b",
        ])
        .command,
        Commands::Task { .. }
    ));
    assert!(matches!(
        parse(&["mobius", "task", "run", "id"]).command,
        Commands::Task { .. }
    ));
    assert!(matches!(
        parse(&["mobius", "task", "show", "id"]).command,
        Commands::Task { .. }
    ));
    assert!(matches!(
        parse(&[
            "mobius",
            "task",
            "list",
            "--project",
            "chat",
            "--status",
            "running",
            "--mine",
        ])
        .command,
        Commands::Task { .. }
    ));
    assert!(matches!(
        parse(&["mobius", "task", "cancel", "id"]).command,
        Commands::Task { .. }
    ));
    assert!(matches!(
        parse(&["mobius", "run", "show", "id"]).command,
        Commands::Run { .. }
    ));
    assert!(matches!(
        parse(&["mobius", "memory", "add", "--kind", "fact", "hello"]).command,
        Commands::Memory { .. }
    ));
    assert!(matches!(
        parse(&[
            "mobius", "memory", "add", "--kind", "fact", "--scope", "repo:a/b", "hi"
        ])
        .command,
        Commands::Memory { .. }
    ));
    assert!(matches!(
        parse(&["mobius", "memory", "list", "--scope", "org"]).command,
        Commands::Memory { .. }
    ));
}

#[test]
fn brief_and_brief_file_conflict() {
    assert!(
        Cli::try_parse_from([
            "mobius",
            "task",
            "create",
            "--title",
            "t",
            "--brief",
            "b",
            "--brief-file",
            "f",
            "--repo",
            "a/b",
        ])
        .is_err()
    );
}

#[test]
fn scope_reads_mobius_env() {
    let env: HashMap<&str, &str> = HashMap::from([
        ("MOBIUS_URL", "http://example:1"),
        ("MOBIUS_ORG", "acme"),
        ("MOBIUS_PROJECT", "chat"),
        (
            "MOBIUS_CONVERSATION",
            "123e4567-e89b-12d3-a456-426614174000",
        ),
    ]);
    let scope = Scope::from(|k| env.get(k).map(|v| v.to_string()));
    assert_eq!(scope.url.as_deref(), Some("http://example:1"));
    assert_eq!(scope.organization.as_deref(), Some("acme"));
    assert_eq!(scope.project.as_deref(), Some("chat"));
    assert!(scope.conversation.is_some());
    // Bad conversation id is ignored, not fatal.
    let scope = Scope::from(|k| (k == "MOBIUS_CONVERSATION").then(|| "nope".to_string()));
    assert!(scope.conversation.is_none());
}

#[test]
fn table_pads_columns() {
    let out = mobius_cli::table(
        &["ID", "NAME"],
        &[
            vec!["1".into(), "short".into()],
            vec!["longer".into(), "n".into()],
        ],
    );
    let lines: Vec<&str> = out.lines().collect();
    assert_eq!(lines.len(), 3);
    assert_eq!(lines[0], "ID      NAME");
    assert_eq!(lines[1], "1       short");
    assert_eq!(lines[2], "longer  n");
}

// ---------------------------------------------------------------- round-trip

struct Server {
    url: String,
    _dir: tempfile::TempDir,
    seed: mobius_store::DevSeed,
}

async fn spawn_server() -> Server {
    let dir = tempfile::tempdir().expect("tempdir");
    let data_dir = dir.path().join("data");
    std::fs::create_dir_all(&data_dir).expect("data dir");
    let store = Arc::new(
        mobius_store::SqliteStore::open(data_dir.join("t.db"))
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

    let events = Arc::new(mobius_server::events::EventBus::new());
    let emit = mobius_server::state::emit_for(events.clone());
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
        emit,
        sessions.clone(),
        data_dir,
    ));
    let (_source, manual) = mobius_ingest::manual_source();
    let (_tx, shutdown) = tokio::sync::watch::channel(false);
    let app = mobius_server::routes::router(
        mobius_server::state::AppState {
            store,
            events,
            sessions,
            dispatcher,
            research,
            manual,
            shutdown,
        },
        None,
    );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind");
    let addr = listener.local_addr().expect("addr");
    tokio::spawn(async move {
        let _ = axum::serve(listener, app).await;
    });
    Server {
        url: format!("http://{addr}"),
        _dir: dir,
        seed,
    }
}

#[tokio::test]
async fn cli_round_trip() {
    let server = spawn_server().await;
    let scope = Scope::default();
    let url = server.url.as_str();

    let cli = parse(&["mobius", "--url", url, "project", "list"]);
    let out = run(&cli, &scope).await.expect("project list");
    assert!(out.contains("chat"), "{out}");
    assert!(out.contains("ingestion"), "{out}");

    let owner_name = format!(
        "{}/{}",
        server.seed.repository.owner, server.seed.repository.name
    );
    let cli = parse(&["mobius", "--url", url, "repo", "list"]);
    let out = run(&cli, &scope).await.expect("repo list");
    assert!(out.contains(&owner_name), "{out}");

    let cli = parse(&[
        "mobius",
        "--url",
        url,
        "task",
        "create",
        "--title",
        "smoke",
        "--brief",
        "x",
        "--repo",
        &owner_name,
        "--project",
        "chat",
    ]);
    let out = run(&cli, &scope).await.expect("task create");
    let task_id = out.split_whitespace().next().expect("task id").to_string();

    let cli = parse(&["mobius", "--url", url, "task", "list", "--project", "chat"]);
    let out = run(&cli, &scope).await.expect("task list");
    assert!(out.contains(&task_id), "{out}");

    let cli = parse(&["mobius", "--url", url, "task", "show", &task_id]);
    let out = run(&cli, &scope).await.expect("task show");
    assert!(out.contains("smoke"), "{out}");

    let cli = parse(&[
        "mobius",
        "--url",
        url,
        "memory",
        "add",
        "--kind",
        "fact",
        "cli smoke",
        "--scope",
        "org",
    ]);
    let out = run(&cli, &scope).await.expect("memory add");
    assert!(!out.is_empty());

    let cli = parse(&["mobius", "--url", url, "memory", "list", "--scope", "org"]);
    let out = run(&cli, &scope).await.expect("memory list");
    assert!(out.contains("cli smoke"), "{out}");
}
