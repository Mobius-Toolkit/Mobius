//! Renders memory entries to markdown files under `<data_dir>/memory/` so
//! harness agents can grep them inside their worktree.
//!
//! Layout:
//! - `<data_dir>/memory/<org-slug>.md`                — organization scope
//! - `<data_dir>/memory/<owner>--<repo>/repo.md`      — repository scope
//! - `<data_dir>/memory/<owner>--<repo>/<slug>.md`    — project scope

use mobius_core::*;
use std::collections::BTreeMap;
use std::path::{Path, PathBuf};

pub struct MemoryDumper;

fn render(entries: &[MemoryEntry], title: &str) -> String {
    let mut by_kind: BTreeMap<MemoryKind, Vec<&MemoryEntry>> = BTreeMap::new();
    for e in entries.iter().filter(|e| e.superseded_by.is_none()) {
        by_kind.entry(e.kind).or_default().push(e);
    }
    let mut out = format!("# {title}\n\n");
    for (kind, entries) in by_kind {
        out.push_str(&format!("## {kind}s\n\n"));
        for e in entries {
            out.push_str(&format!("- {}\n", e.content));
        }
        out.push('\n');
    }
    out
}

impl MemoryDumper {
    /// Write all memory markdown files. `store` is used to resolve scope ids
    /// to human-readable slugs.
    pub async fn dump<S: Store>(store: &S, data_dir: &Path) -> StoreResult<()> {
        let memory_dir = data_dir.join("memory");
        std::fs::create_dir_all(&memory_dir)
            .map_err(|e| StoreError::Database(format!("mkdir {memory_dir:?}: {e}")))?;

        let orgs = store.list_organizations().await?;
        let repos = store.list_repositories().await?;
        let projects = store.list_projects().await?;
        let entries = store.list_memory_entries().await?;

        for org in &orgs {
            let scope = MemoryScope::Organization(org.id);
            let scoped = store.list_memory_by_scope(&scope).await?;
            if scoped.is_empty() {
                continue;
            }
            let path = memory_dir.join(format!("{}.md", org.slug));
            write_file(
                &path,
                &render(&scoped, &format!("Organization: {}", org.name)),
            )?;
        }

        for repo in &repos {
            let dir = memory_dir.join(format!("{}--{}", repo.owner, repo.name));
            let scope = MemoryScope::Repository(repo.id);
            let scoped = store.list_memory_by_scope(&scope).await?;
            if !scoped.is_empty() {
                std::fs::create_dir_all(&dir)
                    .map_err(|e| StoreError::Database(format!("mkdir {dir:?}: {e}")))?;
                write_file(
                    &dir.join("repo.md"),
                    &render(
                        &scoped,
                        &format!("Repository: {}/{}", repo.owner, repo.name),
                    ),
                )?;
            }
            for project in projects.iter().filter(|p| p.repository_id == repo.id) {
                let scope = MemoryScope::Project(project.id);
                let scoped = store.list_memory_by_scope(&scope).await?;
                if scoped.is_empty() {
                    continue;
                }
                std::fs::create_dir_all(&dir)
                    .map_err(|e| StoreError::Database(format!("mkdir {dir:?}: {e}")))?;
                write_file(
                    &dir.join(format!("{}.md", project.slug)),
                    &render(&scoped, &format!("Project: {}", project.name)),
                )?;
            }
        }

        let _ = entries; // entries fetched for future cross-scope dumps
        Ok(())
    }
}

fn write_file(path: &PathBuf, content: &str) -> StoreResult<()> {
    std::fs::write(path, content).map_err(|e| StoreError::Database(format!("write {path:?}: {e}")))
}
