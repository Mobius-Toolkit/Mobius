pub mod activity;
pub mod agents;
pub mod auth;
pub mod chat;
pub mod config;
mod dispatch;
pub mod gh;
pub mod github;
mod issues;
mod lead;
mod lead_events;
pub mod mcp;
mod poll;
pub mod tasks;
pub mod transcript;
mod trust;
pub mod workstreams;

use std::collections::HashMap;
use std::error::Error;
use std::ffi::{OsStr, OsString};
use std::sync::{Arc, Mutex, RwLock};

use chat::ChatHandle;
use config::Config;
use mobius_domain::Live;
use mobius_github::{GitHub, Repository};
use mobius_store::Store;
use time::format_description::BorrowedFormatItem;
use time::macros::format_description;
use tokio::sync::broadcast;

const WORKSTREAM_LABEL: &str = "mobius:workstream";
const READY_LABEL: &str = "mobius:ready";
const WORKING_LABEL: &str = "mobius:working";
const TIME_FORMAT: &[BorrowedFormatItem] =
    format_description!("[year]-[month]-[day] [hour]:[minute] UTC");

#[derive(Clone)]
pub struct Engine {
    pub config: Arc<Config>,
    pub store: Store,
    pub github: GitHub,
    harness_path: Arc<OsString>,
    port: u16,
    repositories: Arc<RwLock<Vec<Repository>>>,
    chats: Arc<Mutex<HashMap<(String, i64), ChatHandle>>>,
    event_sessions: Arc<Mutex<HashMap<(String, i64), lead_events::Wakes>>>,
    callers: Arc<Mutex<HashMap<String, mcp::Caller>>>,
    live: broadcast::Sender<Live>,
}

impl Engine {
    fn repository(&self, name: &str) -> Result<Repository, String> {
        self.repositories
            .read()
            .unwrap()
            .iter()
            .find(|repository| repository.full_name == name)
            .cloned()
            .ok_or_else(|| format!("The Mobius App has no access to {name}."))
    }

    fn broadcast(&self, live: Live) {
        // With no open feed, the channel has no receiver and the send fails.
        let _ = self.live.send(live);
    }
}

pub async fn start(
    config: Config,
    store: Store,
    github_api_url: &str,
    github_web_url: &str,
    harness_path: OsString,
    port: u16,
) -> Result<Engine, Box<dyn Error + Send + Sync>> {
    let gh = mobius_runner::find("gh", &harness_path).ok_or("`gh` is not on PATH")?;
    mobius_runner::prepare(&config.data_dir, &gh)?;
    let engine = Engine {
        config: Arc::new(config),
        store,
        github: GitHub::new(github_api_url, github_web_url)?,
        harness_path: Arc::new(harness_path),
        port,
        repositories: Arc::default(),
        chats: Arc::default(),
        event_sessions: Arc::default(),
        callers: Arc::default(),
        live: broadcast::channel(256).0,
    };
    auth::start(&engine).await?;
    poll::spawn(engine.clone());
    Ok(engine)
}

fn random_hex() -> Result<String, getrandom::Error> {
    let mut bytes = [0u8; 32];
    getrandom::fill(&mut bytes)?;
    Ok(bytes.iter().map(|byte| format!("{byte:02x}")).collect())
}

pub fn missing_commands(config: &Config, path: &OsStr) -> Vec<&'static str> {
    let mut programs: Vec<&'static str> = config
        .roles
        .bindings()
        .iter()
        .map(|(_, binding)| mobius_runner::program(binding.harness))
        .collect();
    programs.push("gh");
    programs.push("curl");
    programs.sort();
    programs.dedup();
    programs.retain(|program| mobius_runner::find(program, path).is_none());
    programs
}
