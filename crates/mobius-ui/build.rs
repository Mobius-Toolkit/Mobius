use std::process::{Command, Output};

use sha2::{Digest, Sha256};

// The build identifier distinguishes two versions of the web UI. The running
// app compares it with the identifier that the server gives at `/ui-version`;
// a difference means a new version is on the server.
fn main() {
    println!("cargo:rustc-env=MOBIUS_BUILD={}", build());
    watch();
}

// Without `cargo:rerun-if-changed` Cargo runs this script again only on a
// change inside this crate, so a commit that changes only a dependency (for
// example `mobius-api`) keeps the old identifier. Watch the sources of the
// workspace for an uncommitted edit, and the git files for a commit or a
// checkout.
fn watch() {
    for path in ["../../crates", "../../Cargo.toml", "../../Cargo.lock"] {
        println!("cargo:rerun-if-changed={path}");
    }
    // `HEAD` changes on a detached commit and on a checkout, `index` on a
    // `git add`, and `packed-refs` when a packed ref moves.
    for name in ["HEAD", "index", "packed-refs"] {
        if let Some(path) = git_path(name) {
            println!("cargo:rerun-if-changed={path}");
        }
    }
    // On a branch `HEAD` holds `ref: <ref>` and the ref file changes on a
    // commit. `rev-parse --git-path` resolves the file because `.git` is a
    // file in a worktree.
    if let Some(reference) = git_path("HEAD")
        .and_then(|head| std::fs::read_to_string(head).ok())
        .and_then(|content| content.trim().strip_prefix("ref: ").map(str::to_owned))
        .and_then(|reference| git_path(&reference))
    {
        println!("cargo:rerun-if-changed={reference}");
    }
}

fn git_path(name: &str) -> Option<String> {
    git(&["rev-parse", "--git-path", name]).map(text)
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
