pub mod auth;
pub mod config;
pub mod github;

use std::error::Error;
use std::ffi::OsStr;
use std::sync::Arc;

use config::Config;
use mobius_github::GitHub;
use mobius_store::Store;

#[derive(Clone)]
pub struct Engine {
    pub config: Arc<Config>,
    pub store: Store,
    pub github: GitHub,
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
    };
    auth::start(&engine).await?;
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
