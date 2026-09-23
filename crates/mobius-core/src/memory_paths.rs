//! Pure path helpers for the on-disk memory layout. Shared by the
//! orchestrator (coordinator cwd) and the store's `MemoryDumper`.
//!
//! Layout:
//! - `<data_dir>/memory/<org-slug>/`                    — one dir per org
//! - `<org-dir>/organization.md`                        — org scope memory
//! - `<org-dir>/projects.md`                            — project catalogue
//! - `<org-dir>/repos/<owner>--<name>.md`               — repo scope memory
//! - `<org-dir>/projects/<slug>.md`                     — project scope memory

use std::path::{Path, PathBuf};

/// `<data_dir>/memory/<org-slug>/` — the coordinator working directory.
pub fn org_dir(data_dir: &Path, org_slug: &str) -> PathBuf {
    data_dir.join("memory").join(org_slug)
}

/// `<org-dir>/organization.md`
pub fn organization_file(org_dir: &Path) -> PathBuf {
    org_dir.join("organization.md")
}

/// `<org-dir>/projects.md` — the catalogue every coordinator reads.
pub fn projects_file(org_dir: &Path) -> PathBuf {
    org_dir.join("projects.md")
}

/// `<org-dir>/repos/<owner>--<name>.md`
pub fn repo_file(org_dir: &Path, owner: &str, name: &str) -> PathBuf {
    org_dir.join("repos").join(format!("{owner}--{name}.md"))
}

/// `<org-dir>/projects/<slug>.md`
pub fn project_file(org_dir: &Path, slug: &str) -> PathBuf {
    org_dir.join("projects").join(format!("{slug}.md"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn layout() {
        let data = Path::new("/data");
        let org = org_dir(data, "mobius");
        assert_eq!(org, Path::new("/data/memory/mobius"));
        assert_eq!(
            organization_file(&org),
            Path::new("/data/memory/mobius/organization.md")
        );
        assert_eq!(
            projects_file(&org),
            Path::new("/data/memory/mobius/projects.md")
        );
        assert_eq!(
            repo_file(&org, "MakeDir", "Mobius"),
            Path::new("/data/memory/mobius/repos/MakeDir--Mobius.md")
        );
        assert_eq!(
            project_file(&org, "chat"),
            Path::new("/data/memory/mobius/projects/chat.md")
        );
    }
}
