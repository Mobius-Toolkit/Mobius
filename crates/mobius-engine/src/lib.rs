pub mod config;

use std::ffi::OsStr;
use std::sync::Arc;

use config::Config;
use mobius_store::Store;

#[derive(Clone)]
pub struct Engine {
    pub config: Arc<Config>,
    pub store: Store,
}

pub fn start(config: Config, store: Store) -> Engine {
    Engine {
        config: Arc::new(config),
        store,
    }
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
