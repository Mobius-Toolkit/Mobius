//! `mobius` — the CLI agents and humans use to talk back to a running
//! mobius-server: projects, repositories, tasks, runs, research, memory.
//!
//! The library is the real entry point (`run`); `main.rs` is a thin shell.

use clap::{Parser, Subcommand};
use mobius_api::*;
use mobius_core::*;
use serde::Serialize;
use serde::de::DeserializeOwned;
use std::str::FromStr;

pub const DEFAULT_URL: &str = "http://127.0.0.1:8787";

// ------------------------------------------------------------------ CLI

#[derive(Debug, Parser)]
#[command(name = "mobius", version, about = "Mobius agent-swarm CLI")]
pub struct Cli {
    /// Mobius server URL (or MOBIUS_URL).
    #[arg(long, global = true)]
    pub url: Option<String>,
    /// Print the raw JSON response instead of a table.
    #[arg(long, global = true)]
    pub json: bool,
    #[command(subcommand)]
    pub command: Commands,
}

#[derive(Debug, Subcommand)]
pub enum Commands {
    /// Projects.
    Project {
        #[command(subcommand)]
        command: ProjectCommand,
    },
    /// Repositories.
    Repo {
        #[command(subcommand)]
        command: RepoCommand,
    },
    /// Research questions answered by a read-only agent.
    Research {
        #[command(subcommand)]
        command: ResearchCommand,
    },
    /// Tasks.
    Task {
        #[command(subcommand)]
        command: TaskCommand,
    },
    /// Task runs.
    Run {
        #[command(subcommand)]
        command: RunCommand,
    },
    /// Durable memory entries.
    Memory {
        #[command(subcommand)]
        command: MemoryCommand,
    },
}

#[derive(Debug, Subcommand)]
pub enum ProjectCommand {
    /// List projects.
    List,
}

#[derive(Debug, Subcommand)]
pub enum RepoCommand {
    /// List repositories.
    List,
}

#[derive(Debug, Subcommand)]
pub enum ResearchCommand {
    /// Ask a research question (read-only agent, repository access).
    Start {
        /// The question to answer.
        #[arg(long)]
        question: String,
        /// Repository to read (`owner/name`); repeatable. Default: the
        /// project's hinted repositories (or all org repositories).
        #[arg(long = "repo")]
        repositories: Vec<String>,
        /// Project slug (or MOBIUS_PROJECT).
        #[arg(long)]
        project: Option<String>,
        /// Poll until the research finishes and print its findings.
        #[arg(long)]
        wait: bool,
        /// Seconds before `--wait` gives up.
        #[arg(long, default_value_t = 600)]
        timeout: u64,
    },
    /// Show one research item.
    Show { id: String },
    /// List research (`--mine` = only what this conversation started).
    List {
        #[arg(long)]
        mine: bool,
    },
    /// Cancel running research.
    Cancel { id: String },
}

#[derive(Debug, Subcommand)]
pub enum TaskCommand {
    /// Create a task for a project coordinator to run.
    Create {
        #[arg(long)]
        title: String,
        /// Brief text (or --brief-file).
        #[arg(long)]
        brief: Option<String>,
        /// Read the brief from a file.
        #[arg(long, conflicts_with = "brief")]
        brief_file: Option<String>,
        /// Repository to change (`owner/name`).
        #[arg(long)]
        repo: String,
        /// Project slug (or MOBIUS_PROJECT).
        #[arg(long)]
        project: Option<String>,
        /// Parent task id (must be in the same project).
        #[arg(long)]
        parent: Option<String>,
        /// feature|fix|spec|refactor|review|housekeeping
        #[arg(long, default_value = "feature")]
        kind: String,
        /// low|normal|high|urgent
        #[arg(long)]
        priority: Option<String>,
    },
    /// Start a task (spawns a worktree + implementer run).
    Run { id: String },
    /// Show a task with its runs and children.
    Show { id: String },
    /// List tasks.
    List {
        /// Project slug (or MOBIUS_PROJECT).
        #[arg(long)]
        project: Option<String>,
        /// proposed|approved|queued|running|needs_review|done|failed|cancelled
        #[arg(long)]
        status: Option<String>,
        /// Only tasks created by this conversation (MOBIUS_CONVERSATION).
        #[arg(long)]
        mine: bool,
    },
    /// Cancel a task.
    Cancel { id: String },
}

#[derive(Debug, Subcommand)]
pub enum RunCommand {
    /// Show a run.
    Show { id: String },
}

#[derive(Debug, Subcommand)]
pub enum MemoryCommand {
    /// Add a durable memory entry.
    Add {
        /// fact|decision|convention|gotcha|summary
        #[arg(long)]
        kind: String,
        /// Entry content.
        content: String,
        /// project | org | repo:owner/name (default: MOBIUS_PROJECT's
        /// project, else the organization).
        #[arg(long)]
        scope: Option<String>,
    },
    /// List memory entries.
    List {
        /// project | org | repo:owner/name
        #[arg(long)]
        scope: Option<String>,
    },
}

// ------------------------------------------------------------------ Scope

/// Ambient scope from the environment (injected by the server into harness
/// processes): `MOBIUS_ORG`, `MOBIUS_PROJECT`, `MOBIUS_CONVERSATION`.
#[derive(Debug, Default, Clone)]
pub struct Scope {
    /// `MOBIUS_URL` — server base URL (overridden by `--url`).
    pub url: Option<String>,
    pub organization: Option<String>,
    pub project: Option<String>,
    pub conversation: Option<ConversationId>,
}

impl Scope {
    pub fn from_env() -> Self {
        Self::from(|k| std::env::var(k).ok())
    }

    /// Env-backed construction, injectable for tests.
    pub fn from(get: impl Fn(&str) -> Option<String>) -> Self {
        Scope {
            url: get("MOBIUS_URL"),
            organization: get("MOBIUS_ORG"),
            project: get("MOBIUS_PROJECT"),
            conversation: get("MOBIUS_CONVERSATION")
                .and_then(|v| ConversationId::from_str(&v).ok()),
        }
    }
}

// ------------------------------------------------------------------ Errors

#[derive(Debug, thiserror::Error)]
pub enum CliError {
    #[error("{message}")]
    Api { status: u16, message: String },
    #[error("{0}")]
    Usage(String),
    #[error("research {0} did not finish before the timeout")]
    Timeout(ResearchId),
    #[error(transparent)]
    Http(#[from] reqwest::Error),
    #[error(transparent)]
    Io(#[from] std::io::Error),
}

impl CliError {
    pub fn exit_code(&self) -> i32 {
        match self {
            CliError::Timeout(_) => 2,
            _ => 1,
        }
    }
}

fn usage(msg: impl Into<String>) -> CliError {
    CliError::Usage(msg.into())
}

// ------------------------------------------------------------------ Client

struct Client {
    http: reqwest::Client,
    base: String,
}

impl Client {
    fn new(base: &str) -> Self {
        Client {
            http: reqwest::Client::new(),
            base: base.trim_end_matches('/').to_string(),
        }
    }

    fn url(&self, path: &str) -> String {
        format!("{}{}", self.base, path)
    }

    async fn check(res: reqwest::Response) -> Result<reqwest::Response, CliError> {
        if res.status().is_success() {
            return Ok(res);
        }
        let status = res.status().as_u16();
        let body = res.text().await.unwrap_or_default();
        let message = serde_json::from_str::<ApiError>(&body)
            .map(|e| e.message)
            .unwrap_or_else(|_| {
                if body.is_empty() {
                    format!("HTTP {status}")
                } else {
                    body
                }
            });
        Err(CliError::Api { status, message })
    }

    async fn get<T: DeserializeOwned>(&self, path: &str) -> Result<T, CliError> {
        let res = Self::check(self.http.get(self.url(path)).send().await?).await?;
        Ok(res.json().await?)
    }

    async fn post<T: DeserializeOwned>(
        &self,
        path: &str,
        body: &impl Serialize,
    ) -> Result<T, CliError> {
        let res = Self::check(self.http.post(self.url(path)).json(body).send().await?).await?;
        Ok(res.json().await?)
    }

    async fn post_empty<T: DeserializeOwned>(&self, path: &str) -> Result<T, CliError> {
        let res = Self::check(self.http.post(self.url(path)).send().await?).await?;
        Ok(res.json().await?)
    }
}

// ------------------------------------------------------------------ Tables

/// Simple aligned table; every column padded to its widest cell.
pub fn table(headers: &[&str], rows: &[Vec<String>]) -> String {
    let mut widths: Vec<usize> = headers.iter().map(|h| h.chars().count()).collect();
    for row in rows {
        for (i, cell) in row.iter().enumerate() {
            if i < widths.len() {
                widths[i] = widths[i].max(cell.chars().count());
            }
        }
    }
    let mut out = String::new();
    let line = |cells: &[String]| -> String {
        cells
            .iter()
            .enumerate()
            .map(|(i, c)| {
                let w = *widths.get(i).unwrap_or(&0);
                let mut s = c.clone();
                while s.chars().count() < w {
                    s.push(' ');
                }
                s
            })
            .collect::<Vec<_>>()
            .join("  ")
            .trim_end()
            .to_string()
    };
    out.push_str(&line(
        &headers.iter().map(|h| h.to_string()).collect::<Vec<_>>(),
    ));
    out.push('\n');
    for row in rows {
        out.push_str(&line(row));
        out.push('\n');
    }
    out.trim_end().to_string()
}

fn print_json<T: Serialize>(v: &T) -> Result<String, CliError> {
    serde_json::to_string_pretty(v).map_err(|e| CliError::Usage(format!("json: {e}")))
}

// ------------------------------------------------------------------ Resolvers

async fn resolve_org(client: &Client, scope: &Scope) -> Result<Organization, CliError> {
    let orgs: Vec<Organization> = client.get("/api/v1/organizations").await?;
    if let Some(slug) = &scope.organization {
        return orgs
            .into_iter()
            .find(|o| &o.slug == slug || &o.name == slug)
            .ok_or_else(|| usage(format!("unknown organization {slug:?}")));
    }
    match orgs.as_slice() {
        [only] => Ok(only.clone()),
        [] => Err(usage("no organizations on this server")),
        _ => Err(usage("multiple organizations; set MOBIUS_ORG")),
    }
}

async fn resolve_project(
    client: &Client,
    scope: &Scope,
    slug: Option<&str>,
) -> Result<Project, CliError> {
    let slug = slug
        .map(str::to_string)
        .or_else(|| scope.project.clone())
        .ok_or_else(|| usage("--project required (or set MOBIUS_PROJECT)"))?;
    let projects: Vec<Project> = client.get("/api/v1/projects").await?;
    let mut matches: Vec<Project> = projects
        .into_iter()
        .filter(|p| p.slug == slug || p.name == slug)
        .collect();
    if matches.len() > 1
        && let Ok(org) = resolve_org(client, scope).await
    {
        matches.retain(|p| p.organization_id == org.id);
    }
    match matches.as_slice() {
        [only] => Ok(only.clone()),
        [] => Err(usage(format!("unknown project {slug:?}"))),
        _ => Err(usage(format!(
            "project slug {slug:?} is ambiguous across organizations"
        ))),
    }
}

async fn resolve_repo(client: &Client, owner_name: &str) -> Result<Repository, CliError> {
    let (owner, name) = owner_name
        .split_once('/')
        .ok_or_else(|| usage(format!("repository must be owner/name, got {owner_name:?}")))?;
    let repos: Vec<Repository> = client.get("/api/v1/repositories").await?;
    repos
        .into_iter()
        .find(|r| r.owner == owner && r.name == name)
        .ok_or_else(|| usage(format!("unknown repository {owner_name:?}")))
}

/// `--scope` values: `project`, `org`, `repo:owner/name`.
async fn resolve_scope(
    client: &Client,
    scope: &Scope,
    arg: Option<&str>,
) -> Result<MemoryScope, CliError> {
    match arg {
        Some("project") => {
            let p = resolve_project(client, scope, None).await?;
            Ok(MemoryScope::Project(p.id))
        }
        Some("org") | Some("organization") => Ok(MemoryScope::Organization(
            resolve_org(client, scope).await?.id,
        )),
        Some(s) if s.starts_with("repo:") || s.starts_with("repository:") => {
            let on = s.split_once(':').map(|(_, rest)| rest).unwrap_or("");
            Ok(MemoryScope::Repository(resolve_repo(client, on).await?.id))
        }
        Some(other) => Err(usage(format!(
            "unknown scope {other:?} (expected project|org|repo:owner/name)"
        ))),
        // Default: the ambient project, else the organization.
        None => {
            if scope.project.is_some() {
                let p = resolve_project(client, scope, None).await?;
                Ok(MemoryScope::Project(p.id))
            } else {
                Ok(MemoryScope::Organization(
                    resolve_org(client, scope).await?.id,
                ))
            }
        }
    }
}

// ------------------------------------------------------------------ run

/// Execute the parsed command; returns the text to print on stdout.
pub async fn run(cli: &Cli, scope: &Scope) -> Result<String, CliError> {
    let base = cli
        .url
        .clone()
        .or_else(|| scope.url.clone())
        .unwrap_or_else(|| DEFAULT_URL.to_string());
    let client = Client::new(&base);
    let json = cli.json;

    match &cli.command {
        Commands::Project {
            command: ProjectCommand::List,
        } => {
            let projects: Vec<Project> = client.get("/api/v1/projects").await?;
            if json {
                return print_json(&projects);
            }
            let rows = projects
                .iter()
                .map(|p| {
                    vec![
                        p.id.to_string(),
                        p.slug.clone(),
                        p.name.clone(),
                        p.status.to_string(),
                    ]
                })
                .collect::<Vec<_>>();
            Ok(table(&["ID", "SLUG", "NAME", "STATUS"], &rows))
        }
        Commands::Repo {
            command: RepoCommand::List,
        } => {
            let repos: Vec<Repository> = client.get("/api/v1/repositories").await?;
            if json {
                return print_json(&repos);
            }
            let rows = repos
                .iter()
                .map(|r| {
                    vec![
                        r.id.to_string(),
                        format!("{}/{}", r.owner, r.name),
                        r.provider.to_string(),
                        r.local_path
                            .as_ref()
                            .map(|p| p.display().to_string())
                            .unwrap_or_else(|| "-".into()),
                    ]
                })
                .collect::<Vec<_>>();
            Ok(table(&["ID", "REPO", "PROVIDER", "LOCAL PATH"], &rows))
        }

        Commands::Research { command } => match command {
            ResearchCommand::Start {
                question,
                repositories,
                project,
                wait,
                timeout,
            } => {
                // `--project` wins, else MOBIUS_PROJECT, else org-level research.
                let project = if project.is_some() || scope.project.is_some() {
                    Some(resolve_project(&client, scope, project.as_deref()).await?)
                } else {
                    None
                };
                let mut repo_ids = Vec::new();
                for on in repositories {
                    repo_ids.push(resolve_repo(&client, on).await?.id);
                }
                let research: Research = client
                    .post(
                        "/api/v1/research",
                        &StartResearch {
                            organization_id: None,
                            project_id: project.map(|p| p.id),
                            repositories: repo_ids,
                            question: question.clone(),
                            origin_conversation_id: scope.conversation,
                            model_profile_id: None,
                        },
                    )
                    .await?;
                let research = if *wait {
                    wait_research(&client, research.id, *timeout).await?
                } else {
                    research
                };
                if json {
                    return print_json(&research);
                }
                let mut out = format!(
                    "{}  {}  {}",
                    research.id, research.status, research.question
                );
                if *wait && let Some(findings) = &research.findings {
                    out.push_str("\n\n");
                    out.push_str(findings);
                }
                Ok(out)
            }
            ResearchCommand::Show { id } => {
                let id = ResearchId::from_str(id)
                    .map_err(|_| usage(format!("invalid research id {id:?}")))?;
                let r: Research = client.get(&format!("/api/v1/research/{id}")).await?;
                if json {
                    return print_json(&r);
                }
                Ok(research_line(&r))
            }
            ResearchCommand::List { mine } => {
                let path = if *mine {
                    let cid = scope
                        .conversation
                        .ok_or_else(|| usage("--mine requires MOBIUS_CONVERSATION"))?;
                    format!("/api/v1/research?conversation_id={cid}")
                } else {
                    "/api/v1/research".to_string()
                };
                let items: Vec<Research> = client.get(&path).await?;
                if json {
                    return print_json(&items);
                }
                let rows = items.iter().map(research_row).collect::<Vec<_>>();
                Ok(table(&["ID", "STATUS", "QUESTION"], &rows))
            }
            ResearchCommand::Cancel { id } => {
                let id = ResearchId::from_str(id)
                    .map_err(|_| usage(format!("invalid research id {id:?}")))?;
                let r: Research = client
                    .post_empty(&format!("/api/v1/research/{id}/cancel"))
                    .await?;
                if json {
                    return print_json(&r);
                }
                Ok(research_line(&r))
            }
        },

        Commands::Task { command } => match command {
            TaskCommand::Create {
                title,
                brief,
                brief_file,
                repo,
                project,
                parent,
                kind,
                priority,
            } => {
                let kind = TaskKind::from_str(kind)
                    .map_err(|_| usage(format!("unknown kind {kind:?}")))?;
                if kind == TaskKind::Triage {
                    return Err(usage("kind triage is reserved for signal routing"));
                }
                let priority = priority
                    .as_deref()
                    .map(|p| {
                        Priority::from_str(p).map_err(|_| usage(format!("unknown priority {p:?}")))
                    })
                    .transpose()?;
                let description = match (brief, brief_file) {
                    (Some(b), _) => b.clone(),
                    (None, Some(f)) => std::fs::read_to_string(f)?,
                    (None, None) => String::new(),
                };
                let project = resolve_project(&client, scope, project.as_deref()).await?;
                let repository = resolve_repo(&client, repo).await?;
                let parent_task_id = parent
                    .as_deref()
                    .map(|p| {
                        TaskId::from_str(p).map_err(|_| usage(format!("invalid task id {p:?}")))
                    })
                    .transpose()?;
                let task: Task = client
                    .post(
                        "/api/v1/tasks",
                        &CreateTask {
                            project_id: project.id,
                            repository_id: Some(repository.id),
                            title: title.clone(),
                            description,
                            kind: Some(kind),
                            parent_task_id,
                            priority,
                            origin_conversation_id: scope.conversation,
                        },
                    )
                    .await?;
                if json {
                    return print_json(&task);
                }
                Ok(task_line(&task))
            }
            TaskCommand::Run { id } => {
                let id =
                    TaskId::from_str(id).map_err(|_| usage(format!("invalid task id {id:?}")))?;
                let run: Run = client
                    .post_empty(&format!("/api/v1/tasks/{id}/run"))
                    .await?;
                if json {
                    return print_json(&run);
                }
                Ok(format!("{}  {}  task {}", run.id, run.status, run.task_id))
            }
            TaskCommand::Show { id } => {
                let id =
                    TaskId::from_str(id).map_err(|_| usage(format!("invalid task id {id:?}")))?;
                let view: TaskView = client.get(&format!("/api/v1/tasks/{id}")).await?;
                if json {
                    return print_json(&view);
                }
                let mut out = task_line(&view.task);
                out.push_str(&format!("\n{}", view.task.description));
                if !view.runs.is_empty() {
                    out.push_str("\n\nruns:");
                    for r in &view.runs {
                        out.push_str(&format!("\n  {}  {}", r.id, r.status));
                    }
                }
                if !view.children.is_empty() {
                    out.push_str("\n\nchildren:");
                    for c in &view.children {
                        out.push_str(&format!("\n  {}  {}", c.id, c.title));
                    }
                }
                Ok(out)
            }
            TaskCommand::List {
                project,
                status,
                mine,
            } => {
                let mut params = Vec::new();
                if let Some(slug) = project.clone().or_else(|| scope.project.clone()) {
                    let p = resolve_project(&client, scope, Some(&slug)).await?;
                    params.push(format!("project_id={}", p.id));
                }
                if let Some(status) = status {
                    TaskStatus::from_str(status)
                        .map_err(|_| usage(format!("unknown status {status:?}")))?;
                    params.push(format!("status={status}"));
                }
                if *mine {
                    let cid = scope
                        .conversation
                        .ok_or_else(|| usage("--mine requires MOBIUS_CONVERSATION"))?;
                    params.push(format!("conversation_id={cid}"));
                }
                let path = if params.is_empty() {
                    "/api/v1/tasks".to_string()
                } else {
                    format!("/api/v1/tasks?{}", params.join("&"))
                };
                let tasks: Vec<Task> = client.get(&path).await?;
                if json {
                    return print_json(&tasks);
                }
                let rows = tasks.iter().map(task_row).collect::<Vec<_>>();
                Ok(table(&["ID", "STATUS", "KIND", "TITLE"], &rows))
            }
            TaskCommand::Cancel { id } => {
                let id =
                    TaskId::from_str(id).map_err(|_| usage(format!("invalid task id {id:?}")))?;
                let task: Task = client
                    .post_empty(&format!("/api/v1/tasks/{id}/cancel"))
                    .await?;
                if json {
                    return print_json(&task);
                }
                Ok(task_line(&task))
            }
        },

        Commands::Run {
            command: RunCommand::Show { id },
        } => {
            let id = RunId::from_str(id).map_err(|_| usage(format!("invalid run id {id:?}")))?;
            let run: Run = client.get(&format!("/api/v1/runs/{id}")).await?;
            if json {
                return print_json(&run);
            }
            let mut out = format!("{}  {}  task {}", run.id, run.status, run.task_id,);
            if let Some(repo) = run.repository_id {
                out.push_str(&format!("  repo {repo}"));
            }
            if let Some(branch) = &run.branch {
                out.push_str(&format!("  branch {branch}"));
            }
            if let Some(summary) = &run.summary {
                out.push_str("\n\n");
                out.push_str(summary);
            }
            Ok(out)
        }

        Commands::Memory { command } => match command {
            MemoryCommand::Add {
                kind,
                content,
                scope: scope_arg,
            } => {
                let kind = MemoryKind::from_str(kind)
                    .map_err(|_| usage(format!("unknown kind {kind:?}")))?;
                let mem_scope = resolve_scope(&client, scope, scope_arg.as_deref()).await?;
                let entry: MemoryEntry = client
                    .post(
                        "/api/v1/memory",
                        &CreateMemoryEntry {
                            scope: mem_scope,
                            kind,
                            content: content.clone(),
                            source_conversation_id: scope.conversation,
                        },
                    )
                    .await?;
                if json {
                    return print_json(&entry);
                }
                Ok(format!("{}  {}", entry.id, entry.kind))
            }
            MemoryCommand::List { scope: scope_arg } => {
                let path = if scope_arg.is_none() {
                    "/api/v1/memory".to_string()
                } else {
                    let mem_scope = resolve_scope(&client, scope, scope_arg.as_deref()).await?;
                    let (level, id) = match mem_scope {
                        MemoryScope::Organization(id) => ("organization", id.to_string()),
                        MemoryScope::Repository(id) => ("repository", id.to_string()),
                        MemoryScope::Project(id) => ("project", id.to_string()),
                    };
                    format!("/api/v1/memory?scope={level}:{id}")
                };
                let entries: Vec<MemoryEntry> = client.get(&path).await?;
                if json {
                    return print_json(&entries);
                }
                let rows = entries
                    .iter()
                    .map(|e| {
                        vec![
                            e.id.to_string(),
                            e.kind.to_string(),
                            truncate(&e.content, 60),
                        ]
                    })
                    .collect::<Vec<_>>();
                Ok(table(&["ID", "KIND", "CONTENT"], &rows))
            }
        },
    }
}

// ------------------------------------------------------------------ helpers

async fn wait_research(
    client: &Client,
    id: ResearchId,
    timeout_secs: u64,
) -> Result<Research, CliError> {
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(timeout_secs);
    loop {
        let r: Research = client.get(&format!("/api/v1/research/{id}")).await?;
        match r.status {
            ResearchStatus::Pending | ResearchStatus::Running => {
                if std::time::Instant::now() >= deadline {
                    return Err(CliError::Timeout(id));
                }
                tokio::time::sleep(std::time::Duration::from_secs(2)).await;
            }
            _ => return Ok(r),
        }
    }
}

fn truncate(s: &str, max: usize) -> String {
    let s = s.lines().next().unwrap_or_default();
    if s.chars().count() <= max {
        s.to_string()
    } else {
        format!(
            "{}…",
            s.chars().take(max.saturating_sub(1)).collect::<String>()
        )
    }
}

fn research_row(r: &Research) -> Vec<String> {
    vec![
        r.id.to_string(),
        r.status.to_string(),
        truncate(&r.question, 60),
    ]
}

fn research_line(r: &Research) -> String {
    let mut out = format!("{}  {}  {}", r.id, r.status, r.question);
    if let Some(findings) = &r.findings {
        out.push_str("\n\n");
        out.push_str(findings);
    }
    out
}

fn task_row(t: &Task) -> Vec<String> {
    vec![
        t.id.to_string(),
        t.status.to_string(),
        t.kind.to_string(),
        t.title.clone(),
    ]
}

fn task_line(t: &Task) -> String {
    format!("{}  {}  {}  {}", t.id, t.status, t.kind, t.title)
}
