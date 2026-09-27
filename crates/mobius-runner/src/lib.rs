use std::env;
use std::ffi::OsStr;
use std::fs;
use std::os::unix::fs::PermissionsExt;

use mobius_domain::Harness;

pub fn program(harness: Harness) -> &'static str {
    match harness {
        Harness::ClaudeCode => "claude-agent-acp",
        Harness::Antigravity => "agy_acp_server",
        Harness::Devin => "devin",
    }
}

pub fn on_path(program: &str, path: &OsStr) -> bool {
    env::split_paths(path).any(|dir| {
        fs::metadata(dir.join(program))
            .is_ok_and(|metadata| metadata.is_file() && metadata.permissions().mode() & 0o111 != 0)
    })
}
