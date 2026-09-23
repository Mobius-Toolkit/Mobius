//! Renders memory + the project catalogue to markdown under
//! `<data_dir>/memory/<org-slug>/` so coordinator agents can read them —
//! that directory is the coordinator's working directory.
//!
//! Layout (see `mobius_core::memory_paths`):
//! - `<org-dir>/organization.md`          — organization scope
//! - `<org-dir>/projects.md`              — project catalogue
//! - `<org-dir>/repos/<owner>--<name>.md` — repository scope
//! - `<org-dir>/projects/<slug>.md`       — project scope

use mobius_core::{memory_paths, *};
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

fn render_catalogue(
    org: &Organization,
    projects: &[Project],
    repos: &[Repository],
    tasks: &[Task],
    entries: &[MemoryEntry],
) -> String {
    let mut out = format!("# {} projects\n\n", org.name);
    for p in projects {
        let repo_names = p
            .repository_ids
            .iter()
            .filter_map(|id| repos.iter().find(|r| &r.id == id))
            .map(|r| format!("{}/{}", r.owner, r.name))
            .collect::<Vec<_>>()
            .join(", ");
        let open_tasks = tasks
            .iter()
            .filter(|t| {
                t.project_id == p.id
                    && !matches!(
                        t.status,
                        TaskStatus::Done | TaskStatus::Cancelled | TaskStatus::Failed
                    )
            })
            .count();
        let memory = entries
            .iter()
            .filter(|e| e.scope == MemoryScope::Project(p.id) && e.superseded_by.is_none())
            .count();
        out.push_str(&format!("## {} — {} ({})\n\n", p.slug, p.name, p.status));
        if !p.description.trim().is_empty() {
            out.push_str(&format!("{}\n\n", p.description.trim()));
        }
        out.push_str(&format!(
            "- repositories: {}\n- open tasks: {open_tasks}\n- memory entries: {memory}\n\n",
            if repo_names.is_empty() {
                "none".to_string()
            } else {
                repo_names
            },
        ));
    }
    out
}

impl MemoryDumper {
    /// Write all memory markdown files for every organization.
    pub async fn dump<S: Store>(store: &S, data_dir: &Path) -> StoreResult<()> {
        let repos = store.list_repositories().await?;
        let tasks = store.list_tasks().await?;
        let entries = store.list_memory_entries().await?;
        for org in store.list_organizations().await? {
            Self::dump_org(store, data_dir, &org, &repos, &tasks, &entries).await?;
        }
        Ok(())
    }

    async fn dump_org<S: Store>(
        store: &S,
        data_dir: &Path,
        org: &Organization,
        repos: &[Repository],
        tasks: &[Task],
        entries: &[MemoryEntry],
    ) -> StoreResult<()> {
        let org_dir = memory_paths::org_dir(data_dir, &org.slug);
        for dir in [
            org_dir.clone(),
            org_dir.join("repos"),
            org_dir.join("projects"),
        ] {
            std::fs::create_dir_all(&dir)
                .map_err(|e| StoreError::Database(format!("mkdir {dir:?}: {e}")))?;
        }

        let projects = store.list_projects_by_organization(org.id).await?;
        let org_repos: Vec<Repository> = repos
            .iter()
            .filter(|r| r.organization_id == org.id)
            .cloned()
            .collect();

        let org_entries = store
            .list_memory_by_scope(&MemoryScope::Organization(org.id))
            .await?;
        write_file(
            &memory_paths::organization_file(&org_dir),
            &render(&org_entries, &format!("Organization: {}", org.name)),
        )?;
        write_file(
            &memory_paths::projects_file(&org_dir),
            &render_catalogue(org, &projects, &org_repos, tasks, entries),
        )?;

        for repo in &org_repos {
            let scoped = store
                .list_memory_by_scope(&MemoryScope::Repository(repo.id))
                .await?;
            write_file(
                &memory_paths::repo_file(&org_dir, &repo.owner, &repo.name),
                &render(
                    &scoped,
                    &format!("Repository: {}/{}", repo.owner, repo.name),
                ),
            )?;
        }
        for project in &projects {
            let scoped = store
                .list_memory_by_scope(&MemoryScope::Project(project.id))
                .await?;
            write_file(
                &memory_paths::project_file(&org_dir, &project.slug),
                &render(&scoped, &format!("Project: {}", project.name)),
            )?;
        }
        Ok(())
    }
}

fn write_file(path: &PathBuf, content: &str) -> StoreResult<()> {
    std::fs::write(path, content).map_err(|e| StoreError::Database(format!("write {path:?}: {e}")))
}
