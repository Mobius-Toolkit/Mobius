pub mod activity;
pub mod auth;
pub mod config;
pub mod github;
mod poll;
mod trust;
pub mod workstreams;

use std::error::Error;
use std::ffi::OsStr;
use std::sync::{Arc, RwLock};

use config::Config;
use mobius_domain::FeedRow;
use mobius_github::{GitHub, Repository};
use mobius_store::Store;
use tokio::sync::broadcast;

const WORKSTREAM_LABEL: &str = "mobius:workstream";

#[derive(Clone)]
pub struct Engine {
    pub config: Arc<Config>,
    pub store: Store,
    pub github: GitHub,
    repositories: Arc<RwLock<Vec<Repository>>>,
    live: broadcast::Sender<FeedRow>,
}

pub async fn start(
    config: Config,
    store: Store,
    github_api_url: &str,
    github_web_url: &str,
) -> Result<Engine, Box<dyn Error + Send + Sync>> {
    let engine = Engine {
        config: Arc::new(config),
        store,
        github: GitHub::new(github_api_url, github_web_url)?,
        repositories: Arc::default(),
        live: broadcast::channel(256).0,
    };
    auth::start(&engine).await?;
    poll::spawn(engine.clone());
    Ok(engine)
}

pub fn missing_commands(config: &Config, path: &OsStr) -> Vec<&'static str> {
    let mut programs: Vec<&'static str> = config
        .roles
        .bindings()
        .iter()
        .map(|(_, binding)| mobius_runner::program(binding.harness))
        .collect();
    programs.push("gh");
    programs.sort();
    programs.dedup();
    programs.retain(|program| !mobius_runner::on_path(program, path));
    programs
}
