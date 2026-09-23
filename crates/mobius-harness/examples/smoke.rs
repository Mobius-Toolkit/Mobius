//! ACP smoke test against a real harness.
//!
//! ```bash
//! cargo run -p mobius-harness --example smoke -- devin --model swe --effort max \
//!     "Reply with exactly the word PONG"
//! ```
//!
//! Prints the advertised configOptions as JSON, applies the profile, then runs
//! TWO prompt turns in one session to prove the session is long-lived.

use clap::Parser;
use mobius_core::{Effort, Harness, HarnessId, ModelProfile, ModelProfileId, PermissionPolicy};
use mobius_harness::{HarnessSession, SessionUpdate, SpawnSpec};
use std::path::PathBuf;

#[derive(Parser)]
struct Cli {
    /// Harness command, e.g. `devin acp` → `--command "devin" --args "acp"`,
    /// or use a built-in name (devin, agy, opencode, claude, codex).
    harness: String,
    #[arg(long)]
    model: Option<String>,
    #[arg(long)]
    effort: Option<String>,
    /// Print unmapped `Other` session updates.
    #[arg(short, long)]
    verbose: bool,
    prompt: String,
}

fn builtin(name: &str) -> Option<Harness> {
    let (command, args, template): (&str, Vec<&str>, Vec<&str>) = match name {
        "devin" => ("devin", vec!["acp"], vec!["--model", "{model}"]),
        "agy" => ("npx", vec!["-y", "agy-acp"], vec![]),
        "opencode" => ("opencode", vec!["acp"], vec![]),
        "claude" => ("npx", vec!["-y", "@zed-industries/claude-code-acp"], vec![]),
        "codex" => ("codex-acp", vec![], vec![]),
        _ => return None,
    };
    Some(Harness {
        id: HarnessId::new(),
        name: name.to_string(),
        command: command.to_string(),
        args: args.iter().map(|s| s.to_string()).collect(),
        env: Default::default(),
        default_permission_policy: PermissionPolicy::Auto,
        model_arg_template: template.iter().map(|s| s.to_string()).collect(),
        enabled: true,
        created_at: chrono::Utc::now(),
    })
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("warn")),
        )
        .init();

    let cli = Cli::parse();
    let harness = builtin(&cli.harness).ok_or_else(|| {
        format!(
            "unknown harness {:?} (expected devin|agy|opencode|claude|codex)",
            cli.harness
        )
    })?;

    let effort = cli
        .effort
        .as_deref()
        .map(|e| e.parse::<Effort>())
        .transpose()?;

    let profile = if cli.model.is_some() || effort.is_some() {
        Some(ModelProfile {
            id: ModelProfileId::new(),
            name: "smoke".to_string(),
            harness_id: harness.id,
            model: cli.model.clone(),
            effort,
            config: Default::default(),
            created_at: chrono::Utc::now(),
        })
    } else {
        None
    };

    let t0 = std::time::Instant::now();
    macro_rules! ts {
        ($($arg:tt)*) => {
            eprintln!("[{:>7}ms] {}", t0.elapsed().as_millis(), format!($($arg)*))
        };
    }

    let cwd: PathBuf = std::env::temp_dir().join(format!("mobius-smoke-{}", std::process::id()));
    std::fs::create_dir_all(&cwd)?;
    ts!("cwd = {}", cwd.display());

    let session = HarnessSession::spawn(SpawnSpec {
        harness,
        cwd,
        policy: PermissionPolicy::Auto,
        profile,
        extra_env: Default::default(),
        cli_bin_dir: None,
    })
    .await?;

    ts!("acp session id: {}", session.session_id());
    let advertised = session.config_options();
    ts!("advertised configOptions:");
    println!(
        "{}",
        serde_json::to_string_pretty(&advertised).unwrap_or_else(|_| "[]".to_string())
    );

    // Forward updates to stderr while the turns run. `last_agent_at` records
    // when the most recent [agent] chunk arrived so we can measure the lag
    // between the final streamed text and `prompt()` resolving.
    let last_agent_at = std::sync::Arc::new(std::sync::Mutex::new(t0));
    let last_agent_printer = last_agent_at.clone();
    let mut updates = session.updates();
    let verbose = cli.verbose;
    let printer = tokio::spawn(async move {
        while let Ok(update) = updates.recv().await {
            let tag = format!("[{:>7}ms]", t0.elapsed().as_millis());
            match update {
                SessionUpdate::AgentText(t) => {
                    if let Ok(mut g) = last_agent_printer.lock() {
                        *g = std::time::Instant::now();
                    }
                    eprintln!("{tag} [agent] {t}");
                }
                SessionUpdate::Thought(t) => eprintln!("{tag} [thought] {t}"),
                SessionUpdate::ToolCall { title, kind, .. } => {
                    eprintln!("{tag} [tool] {kind:?} {title}")
                }
                SessionUpdate::ToolCallUpdate { id, status, .. } => {
                    eprintln!("{tag} [tool-update] {id} {status:?}")
                }
                SessionUpdate::PermissionRequested { request_id, .. } => {
                    eprintln!("{tag} [permission] parked {request_id}")
                }
                SessionUpdate::ConfigOptionsChanged(o) => {
                    eprintln!("{tag} [config] {} options", o.len())
                }
                SessionUpdate::TurnFinished { stop_reason } => {
                    eprintln!("{tag} [turn] finished: {stop_reason}")
                }
                SessionUpdate::Other { kind, .. } => {
                    if verbose {
                        eprintln!("{tag} [other] {kind}")
                    }
                }
                SessionUpdate::Error(e) => eprintln!("{tag} [error] {e}"),
                SessionUpdate::Plan(_) | SessionUpdate::Exited => {}
            }
        }
    });

    ts!("turn 1 sent: {}", cli.prompt);
    let stop1 = session.prompt(&cli.prompt).await?;
    let prompt_returned = std::time::Instant::now();
    ts!("turn 1 returned, stop_reason: {stop1}");
    if let Ok(last) = last_agent_at.lock() {
        let gap = prompt_returned.saturating_duration_since(*last);
        ts!(
            "turn 1 lag: last [agent] chunk at {:>7}ms, prompt() returned at {:>7}ms, gap {gap:?}",
            last.duration_since(t0).as_millis(),
            prompt_returned.duration_since(t0).as_millis(),
        );
    }

    ts!("turn 2 sent: What did I ask you in my previous message?");
    let stop2 = session
        .prompt("What did I ask you in my previous message?")
        .await?;
    ts!("turn 2 returned, stop_reason: {stop2}");

    session.close().await;
    printer.abort();
    ts!("done");
    Ok(())
}
