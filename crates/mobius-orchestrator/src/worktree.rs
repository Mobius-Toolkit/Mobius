//! Git worktree management for runs — shells out to `git worktree add`.

use crate::error::OrchestratorError;
use std::path::{Path, PathBuf};

pub struct WorktreeManager {
    /// Directory under which worktrees are created (e.g. `<data_dir>/worktrees`).
    pub dir: PathBuf,
}

impl WorktreeManager {
    pub fn new(dir: PathBuf) -> Self {
        Self { dir }
    }

    /// `git -C <repo> worktree add <dir>/<safe_branch> -b <branch>`; returns the
    /// worktree path. Idempotent-ish: if the path exists it is reused.
    pub async fn create(
        &self,
        repo_path: &Path,
        branch: &str,
    ) -> Result<PathBuf, OrchestratorError> {
        let safe = branch.replace('/', "-");
        let path = self.dir.join(&safe);
        if path.exists() {
            return Ok(path);
        }
        std::fs::create_dir_all(&self.dir)?;
        let output = tokio::process::Command::new("git")
            .arg("-C")
            .arg(repo_path)
            .args(["worktree", "add"])
            .arg(&path)
            .args(["-b", branch])
            .output()
            .await?;
        if !output.status.success() {
            return Err(OrchestratorError::NotLive(format!(
                "git worktree add failed: {}",
                String::from_utf8_lossy(&output.stderr)
            )));
        }
        Ok(path)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn creates_worktree() {
        let dir = tempfile::tempdir().expect("tmp");
        let repo = dir.path().join("repo");
        std::fs::create_dir_all(&repo).expect("mkdir");
        for args in [
            vec!["init"],
            vec!["config", "user.email", "t@t"],
            vec!["config", "user.name", "t"],
            vec!["commit", "--allow-empty", "-m", "init"],
        ] {
            let out = std::process::Command::new("git")
                .args(&args)
                .current_dir(&repo)
                .output()
                .expect("git");
            assert!(out.status.success(), "git {args:?}: {out:?}");
        }
        let mgr = WorktreeManager::new(dir.path().join("wts"));
        let path = mgr.create(&repo, "mobius/task-1").await.expect("worktree");
        assert!(path.join(".git").exists());
        // Reuse: second call succeeds with the same path.
        let again = mgr.create(&repo, "mobius/task-1").await.expect("reuse");
        assert_eq!(path, again);
    }
}
