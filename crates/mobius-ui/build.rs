use std::process::{Command, Output};

use sha2::{Digest, Sha256};

// The build identifier distinguishes two versions of the web UI. The running
// app compares it with the identifier that the server gives at `/ui-version`;
// a difference means a new version is on the server.
fn main() {
    println!("cargo:rustc-env=MOBIUS_BUILD={}", build());
}

fn build() -> String {
    git(&["rev-parse", "HEAD"])
        .map(|output| {
            let commit = text(output);
            match uncommitted() {
                Some(changes) => format!("{commit}-{changes}"),
                None => commit,
            }
        })
        .unwrap_or_else(|| std::env::var("CARGO_PKG_VERSION").unwrap_or_default())
}

fn git(arguments: &[&str]) -> Option<Output> {
    Command::new("git")
        .args(arguments)
        .output()
        .ok()
        .filter(|output| output.status.success())
}

fn text(output: Output) -> String {
    String::from_utf8_lossy(&output.stdout).trim().to_string()
}

// The short hash of the uncommitted changes, so a rebuild after an edit counts
// as a new version too. `None` on a clean tree.
fn uncommitted() -> Option<String> {
    let status = git(&["status", "--porcelain"])?;
    if status.stdout.is_empty() {
        return None;
    }
    let diff = git(&["diff", "HEAD"])?;
    let hash = Sha256::new()
        .chain_update(&status.stdout)
        .chain_update(&diff.stdout)
        .finalize();
    Some(format!(
        "{:x}",
        u32::from_be_bytes(hash[..4].try_into().ok()?)
    ))
}
