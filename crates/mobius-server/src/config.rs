//! `mobius.toml` — infrastructure configuration only. Domain configuration
//! (orgs, repos, projects, agents, profiles, harnesses) lives in SQLite.

use serde::Deserialize;
use std::path::{Path, PathBuf};

#[derive(Debug, Clone, Deserialize, Default)]
#[serde(default)]
pub struct Config {
    pub server: ServerConfig,
    pub github: GithubConfig,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(default)]
pub struct ServerConfig {
    pub bind: String,
    pub data_dir: PathBuf,
    pub ui_dir: Option<PathBuf>,
    pub log: String,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(default)]
pub struct GithubConfig {
    /// Polling interval for `gh`-based ingestion.
    pub poll_interval_secs: u64,
    /// `gh` binary path/name.
    pub gh_binary: String,
}

impl Default for ServerConfig {
    fn default() -> Self {
        Self {
            bind: "127.0.0.1:8787".to_string(),
            data_dir: PathBuf::from("./.mobius-data"),
            ui_dir: None,
            log: "info".to_string(),
        }
    }
}

impl Default for GithubConfig {
    fn default() -> Self {
        Self {
            poll_interval_secs: 300,
            gh_binary: "gh".to_string(),
        }
    }
}

impl Config {
    pub fn load(path: &Path) -> Result<Self, ConfigError> {
        if !path.exists() {
            tracing::info!(path = %path.display(), "no config file; using defaults");
            return Ok(Self::default());
        }
        let text = std::fs::read_to_string(path)
            .map_err(|e| ConfigError(format!("read {}: {e}", path.display())))?;
        toml::from_str(&text).map_err(|e| ConfigError(format!("parse {}: {e}", path.display())))
    }

    pub fn db_path(&self) -> PathBuf {
        self.server.data_dir.join("mobius.db")
    }
}

#[derive(Debug, thiserror::Error)]
#[error("{0}")]
pub struct ConfigError(pub String);
