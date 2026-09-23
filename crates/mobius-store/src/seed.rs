//! Idempotent seeding: default harnesses on every startup, and the
//! `seed-dev` dogfooding fixture — the `mobius` organization, the local
//! checkout as a repository, the organization coordinator agent, the model
//! profiles, and the starter projects. Nothing is ever deleted.

use mobius_core::*;
use std::path::Path;
use std::process::Command;

fn now() -> chrono::DateTime<chrono::Utc> {
    chrono::Utc::now()
}

/// Instructions for the organization coordinator (`mobius`). Coordinators
/// never get repository local paths — they research and delegate.
const ORG_COORDINATOR_INSTRUCTIONS: &str = "\
You are the organization coordinator for Mobius. You advise the human and \
coordinate work across the organization's repositories — you do NOT edit \
code yourself, and you do not have filesystem access to repository \
checkouts. When you need code facts, use `mobius research start` to \
delegate research; never guess. When something must physically change, \
direct it to the owning project's coordinator so a task can be created. \
Write durable knowledge to memory explicitly with `mobius memory add`.";

fn harness(name: &str, command: &str, args: &[&str], model_arg_template: &[&str]) -> Harness {
    Harness {
        id: HarnessId::new(),
        name: name.to_string(),
        command: command.to_string(),
        args: args.iter().map(|s| s.to_string()).collect(),
        env: Default::default(),
        default_permission_policy: PermissionPolicy::AskHuman,
        model_arg_template: model_arg_template.iter().map(|s| s.to_string()).collect(),
        enabled: true,
        created_at: now(),
    }
}

/// The built-in harness registry. Devin is the first-class target.
pub fn default_harnesses() -> Vec<Harness> {
    vec![
        harness("devin", "devin", &["acp"], &["--model", "{model}"]),
        // agy (Google Antigravity CLI) has no native ACP mode yet; the
        // community `agy-acp` adapter bridges it.
        harness("agy", "npx", &["-y", "agy-acp"], &[]),
        harness("opencode", "opencode", &["acp"], &[]),
        harness(
            "claude",
            "npx",
            &["-y", "@zed-industries/claude-code-acp"],
            &[],
        ),
        harness("codex", "codex-acp", &[], &[]),
    ]
}

/// Insert the default harnesses if absent (keyed by name). Idempotent.
pub async fn ensure_default_harnesses<S: HarnessRepo>(store: &S) -> StoreResult<()> {
    for h in default_harnesses() {
        if store.find_harness_by_name(&h.name).await?.is_none() {
            store.insert_harness(&h).await?;
        }
    }
    Ok(())
}

/// Owner/name parsed from `git remote get-url origin`, or ("local", dirname)
/// when the checkout has no GitHub remote.
fn repo_owner_name(repo_path: &Path) -> (String, String) {
    let fallback_name = repo_path
        .file_name()
        .map(|n| n.to_string_lossy().to_string())
        .unwrap_or_else(|| "repo".to_string());

    let output = Command::new("git")
        .args(["remote", "get-url", "origin"])
        .current_dir(repo_path)
        .output();
    let Ok(output) = output else {
        return ("local".to_string(), fallback_name);
    };
    if !output.status.success() {
        return ("local".to_string(), fallback_name);
    }
    let url = String::from_utf8_lossy(&output.stdout).trim().to_string();
    parse_github_remote(&url).unwrap_or(("local".to_string(), fallback_name))
}

/// Parse `git@github.com:owner/name.git` or `https://github.com/owner/name(.git)`.
fn parse_github_remote(url: &str) -> Option<(String, String)> {
    let path = if let Some(rest) = url.strip_prefix("git@github.com:") {
        rest.to_string()
    } else if url.contains("github.com/") {
        url.split("github.com/").nth(1)?.to_string()
    } else {
        return None;
    };
    let path = path.trim_end_matches(".git").trim_matches('/');
    let mut parts = path.split('/');
    let owner = parts.next()?.to_string();
    let name = parts.next()?.to_string();
    if owner.is_empty() || name.is_empty() {
        return None;
    }
    Some((owner, name))
}

fn current_branch(repo_path: &Path) -> String {
    Command::new("git")
        .args(["symbolic-ref", "--short", "HEAD"])
        .current_dir(repo_path)
        .output()
        .ok()
        .filter(|o| o.status.success())
        .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| "main".to_string())
}

pub struct DevSeed {
    pub organization: Organization,
    pub repository: Repository,
    pub projects: Vec<Project>,
    pub profiles: Vec<ModelProfile>,
    pub agent: Agent,
}

/// Insert the dogfooding fixture pointing at `repo_path`. Idempotent: every
/// entity is looked up by its natural key before insert.
pub async fn dev_fixture<S: Store>(store: &S, repo_path: &Path) -> StoreResult<DevSeed> {
    let (owner, name) = repo_owner_name(repo_path);
    let branch = current_branch(repo_path);

    let organization = match store.find_organization_by_slug("mobius").await? {
        Some(org) => org,
        None => {
            let org = Organization {
                id: OrganizationId::new(),
                name: "Mobius".to_string(),
                slug: "mobius".to_string(),
                created_at: now(),
            };
            store.insert_organization(&org).await?;
            org
        }
    };

    let repository = match store.find_repository_by_owner_name(&owner, &name).await? {
        Some(repo) => repo,
        None => {
            let repo = Repository {
                id: RepositoryId::new(),
                organization_id: organization.id,
                owner,
                name,
                provider: RepoProvider::GitHub,
                default_branch: branch,
                local_path: Some(repo_path.to_path_buf()),
                created_at: now(),
            };
            store.insert_repository(&repo).await?;
            repo
        }
    };

    let devin = store.find_harness_by_name("devin").await?.ok_or_else(|| {
        StoreError::NotFound(
            "harness 'devin' missing; run ensure_default_harnesses first".to_string(),
        )
    })?;

    let mut profiles = Vec::new();
    for (name, model, effort) in [
        ("swe2-max", "swe-2-high", Effort::Max),
        ("swe2-high", "swe-2-high", Effort::High),
        ("swe2-low", "swe-2-medium", Effort::Low),
    ] {
        let profile = match store.find_model_profile_by_name(name).await? {
            Some(p) => p,
            None => {
                let p = ModelProfile {
                    id: ModelProfileId::new(),
                    name: name.to_string(),
                    harness_id: devin.id,
                    model: Some(model.to_string()),
                    effort: Some(effort),
                    config: Default::default(),
                    created_at: now(),
                };
                store.insert_model_profile(&p).await?;
                p
            }
        };
        profiles.push(profile);
    }
    let profile_id = |name: &str| {
        profiles
            .iter()
            .find(|p| p.name == name)
            .map(|p| p.id)
            .unwrap_or(profiles[0].id)
    };

    // The organization coordinator — long-lived, read-only, no repo paths.
    let agent = match store
        .find_agent_by_role_and_org(organization.id, AgentRole::Organization)
        .await?
    {
        Some(agent) => agent,
        None => {
            let mut overrides = std::collections::BTreeMap::new();
            overrides.insert(Activity::Plan, profile_id("swe2-max"));
            overrides.insert(Activity::Research, profile_id("swe2-low"));
            overrides.insert(Activity::Implement, profile_id("swe2-high"));
            let agent = Agent {
                id: AgentId::new(),
                name: "mobius".to_string(),
                role: AgentRole::Organization,
                organization_id: organization.id,
                profiles: ActivityProfiles {
                    default: profile_id("swe2-high"),
                    overrides,
                },
                project_id: None,
                repository_id: None,
                instructions: ORG_COORDINATOR_INSTRUCTIONS.to_string(),
                permission_policy: PermissionPolicy::ReadOnly,
                status: AgentStatus::Idle,
                created_at: now(),
            };
            store.insert_agent(&agent).await?;
            agent
        }
    };

    let mut projects = Vec::new();
    for (slug, name, description) in [
        (
            "chat",
            "Chat",
            "Human chat surface, streaming and permissions",
        ),
        (
            "memory",
            "Memory",
            "Organization, repository and project memory",
        ),
        ("ingestion", "Ingestion", "Signal ingestion and routing"),
    ] {
        let project = match store.find_project_by_slug(organization.id, slug).await? {
            Some(project) => project,
            None => {
                let project = Project {
                    id: ProjectId::new(),
                    organization_id: organization.id,
                    repository_ids: vec![repository.id],
                    name: name.to_string(),
                    slug: slug.to_string(),
                    description: description.to_string(),
                    scope: ProjectScope::default(),
                    status: ProjectStatus::Active,
                    created_at: now(),
                };
                store.insert_project(&project).await?;
                project
            }
        };
        projects.push(project);
    }

    Ok(DevSeed {
        organization,
        repository,
        projects,
        profiles,
        agent,
    })
}
