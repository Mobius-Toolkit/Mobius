use clap::{Parser, Subcommand};
use mobius_core::DomainEvent;
use mobius_ingest::{SourceRegistry, manual_source};
use mobius_ingest_github::{CliGhRunner, GithubSources, gh_available};
use mobius_orchestrator::{Dispatcher, SessionManager};
use mobius_server::config::Config;
use mobius_server::events::EventBus;
use mobius_server::routes::router;
use mobius_server::state::{AppState, emit_for};
use mobius_store::SqliteStore;
use std::net::SocketAddr;
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

#[derive(Parser)]
#[command(name = "mobius-server", version, about = "Mobius agent-swarm server")]
struct Cli {
    /// Path to mobius.toml (infra config only).
    #[arg(long, global = true, default_value = "mobius.toml")]
    config: PathBuf,
    #[command(subcommand)]
    command: Option<Command>,
}

#[derive(Subcommand)]
enum Command {
    /// Run the HTTP server (default).
    Serve,
    /// Insert the dogfooding fixture (org/repo/project/profiles/agent).
    SeedDev {
        /// Path to the local git checkout to register as a repository.
        #[arg(long)]
        repo: PathBuf,
    },
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let cli = Cli::parse();
    let config = Config::load(&cli.config)?;

    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| config.server.log.clone().into()),
        )
        .init();

    std::fs::create_dir_all(&config.server.data_dir)?;
    let store = Arc::new(SqliteStore::open(config.db_path()).await?);
    mobius_store::ensure_default_harnesses(store.as_ref()).await?;

    if let Some(Command::SeedDev { repo }) = cli.command {
        {
            let seed = mobius_store::dev_fixture(store.as_ref(), &repo).await?;
            tracing::info!(
                org = %seed.organization.slug,
                repo = %format!("{}/{}", seed.repository.owner, seed.repository.name),
                project = %seed.project.slug,
                agent = %seed.agent.name,
                "seed-dev complete"
            );
            println!(
                "seeded: org={} repo={}/{} project={} agent={} profiles={}",
                seed.organization.slug,
                seed.repository.owner,
                seed.repository.name,
                seed.project.slug,
                seed.agent.name,
                seed.profiles.len()
            );
            return Ok(());
        }
    }

    let events = Arc::new(EventBus::new());
    let emit = emit_for(events.clone());
    let sessions = Arc::new(SessionManager::new(store.clone(), emit.clone()));
    let dispatcher = Arc::new(Dispatcher::new(
        store.clone(),
        emit.clone(),
        config.server.data_dir.join("worktrees"),
    ));
    let (manual_source, manual_handle) = manual_source();
    let (shutdown_tx, shutdown_rx) = tokio::sync::watch::channel(false);
    let state = AppState {
        store: store.clone(),
        events: events.clone(),
        sessions: sessions.clone(),
        dispatcher: dispatcher.clone(),
        manual: manual_handle,
        shutdown: shutdown_rx,
    };

    // Signal ingestion: manual source on a fast loop (UI input must dispatch
    // immediately); GitHub sources on the configured poll interval, one per
    // repo whose provider is GitHub and owner != "local" (only when `gh`
    // is installed).
    let mut manual_registry = SourceRegistry::new();
    manual_registry.add(Box::new(manual_source));
    {
        let store = store.clone();
        let emit = emit.clone();
        tokio::spawn(async move {
            manual_registry
                .run_polling_loop(Duration::from_millis(500), store, move |e| emit(e))
                .await;
        });
    }

    let gh_ok = gh_available(&config.github.gh_binary).await;
    if !gh_ok {
        tracing::warn!(
            binary = %config.github.gh_binary,
            "gh CLI not found; GitHub ingestion disabled"
        );
    }
    if gh_ok {
        // GithubSources re-reads eligible repositories (GitHub provider,
        // owner != "local") from the store on every tick, so repository CRUD
        // takes effect without a restart.
        let sources =
            GithubSources::new(Arc::new(CliGhRunner::new(config.github.gh_binary.clone())));
        let poll_interval = Duration::from_secs(config.github.poll_interval_secs.max(1));
        let store = store.clone();
        let emit = emit.clone();
        tokio::spawn(async move {
            sources.run(poll_interval, store, move |e| emit(e)).await;
        });
    }

    // Route ingested signals through the dispatcher.
    {
        let mut rx = events.subscribe();
        let dispatcher = dispatcher.clone();
        tokio::spawn(async move {
            while let Ok(envelope) = rx.recv().await {
                if let DomainEvent::SignalIngested { signal } = envelope.event
                    && let Err(e) = dispatcher.handle_signal(&signal).await
                {
                    tracing::warn!(error = %e, "signal dispatch failed");
                }
            }
        });
    }

    // Periodic memory markdown dump.
    {
        let store = store.clone();
        let data_dir = config.server.data_dir.clone();
        tokio::spawn(async move {
            loop {
                tokio::time::sleep(Duration::from_secs(300)).await;
                if let Err(e) = mobius_store::MemoryDumper::dump(store.as_ref(), &data_dir).await {
                    tracing::warn!(error = %e, "memory dump failed");
                }
            }
        });
    }

    // Conversations left `streaming`/`awaiting_permission` by a previous
    // shutdown are dead — the live ACP sessions were process-lifetime.
    sessions.reset_stale_statuses().await;

    if let Some(ui_dir) = &config.server.ui_dir
        && !ui_dir.join("index.html").exists()
    {
        tracing::warn!(
            ui_dir = %ui_dir.display(),
            "UI not built; run scripts/mobius.sh ui"
        );
    }

    let app = router(state, config.server.ui_dir.clone());
    let addr: SocketAddr = config.server.bind.parse()?;
    let listener = tokio::net::TcpListener::bind(addr).await?;
    tracing::info!(%addr, "mobius-server listening");
    axum::serve(listener, app)
        .with_graceful_shutdown(shutdown_signal(sessions, shutdown_tx))
        .await?;
    Ok(())
}

async fn shutdown_signal(
    sessions: Arc<SessionManager<SqliteStore>>,
    shutdown_tx: tokio::sync::watch::Sender<bool>,
) {
    let _ = tokio::signal::ctrl_c().await;
    tracing::info!("shutting down; closing sessions (Ctrl+C again to force)");
    // A second Ctrl+C exits immediately — the first replaced the default
    // SIGINT disposition, so without this the process would ignore it.
    tokio::spawn(async {
        let _ = tokio::signal::ctrl_c().await;
        std::process::exit(0);
    });
    // …and if in-flight requests still haven't drained after a grace
    // period, leave anyway rather than hanging forever.
    tokio::spawn(async {
        tokio::time::sleep(Duration::from_secs(5)).await;
        tracing::warn!("graceful shutdown timed out after 5s; forcing exit");
        std::process::exit(0);
    });
    sessions.close_all().await;
    // End SSE streams so axum's graceful shutdown can complete.
    let _ = shutdown_tx.send(true);
}
