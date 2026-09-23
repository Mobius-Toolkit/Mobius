//! Prompt builders: the coordinator chat preamble, the "since your last
//! message" updates block, the researcher prompt, and the task-run prompt.

use mobius_core::*;
use std::fmt::Write as _;
use std::path::PathBuf;

/// Char budget for the `# Memory` section of a chat preamble; the oldest
/// entries are dropped first when it overflows.
const MEMORY_CAP: usize = 24_000;

/// A labelled group of memory entries — rendered under `## <label>` inside
/// `# Memory` (e.g. `Organization`, `Repository owner/name`, `Project slug`).
/// Repository labels must be `owner/name` only — never local paths.
pub struct MemoryGroup {
    pub label: String,
    pub entries: Vec<MemoryEntry>,
}

impl MemoryGroup {
    pub fn new(label: impl Into<String>, entries: Vec<MemoryEntry>) -> Self {
        Self {
            label: label.into(),
            entries,
        }
    }
}

/// Everything the coordinator chat preamble renders. Assembled by the
/// session manager from the store; `memory` groups are pre-ordered
/// org → repos → project.
pub struct ChatContext {
    pub org: Organization,
    pub agent: Agent,
    /// `Some` for project-scoped chats, `None` for the org advisor.
    pub project: Option<Project>,
    /// All projects of the organization (the catalogue).
    pub projects: Vec<Project>,
    /// Related repositories — rendered as `owner/name` + branch ONLY:
    /// coordinators must never see local paths.
    pub repositories: Vec<Repository>,
    pub memory: Vec<MemoryGroup>,
    /// Markdown memory files under the org dir (the coordinator's cwd).
    pub memory_files: Vec<PathBuf>,
    /// This conversation's own open research/tasks.
    pub open_research: Vec<Research>,
    pub open_tasks: Vec<Task>,
}

fn repo_label(repo: &Repository) -> String {
    format!(
        "{}/{} (branch `{}`)",
        repo.owner, repo.name, repo.default_branch
    )
}

fn memory_section(groups: &[MemoryGroup], out: &mut String) {
    // Flatten in render order; superseded entries never render.
    let flat: Vec<(&str, &MemoryEntry)> = groups
        .iter()
        .flat_map(|g| {
            g.entries
                .iter()
                .filter(|e| e.superseded_by.is_none())
                .map(|e| (g.label.as_str(), e))
        })
        .collect();
    if flat.is_empty() {
        return;
    }
    // Keep the newest entries within the global cap (entries arrive
    // oldest → newest; walk backwards and drop the oldest first). The cap
    // also covers the `# Memory` header and one `## <label>` heading per
    // group — the section as rendered must stay under it.
    let header = "# Memory\n\n".len()
        + groups
            .iter()
            .filter(|g| g.entries.iter().any(|e| e.superseded_by.is_none()))
            .map(|g| g.label.len() + 6)
            .sum::<usize>();
    let mut kept: Vec<(&str, &MemoryEntry)> = Vec::new();
    let mut size = header;
    for pair in flat.iter().rev() {
        let line = pair.1.content.len() + 16;
        if size + line > MEMORY_CAP {
            break;
        }
        size += line;
        kept.push(*pair);
    }
    kept.reverse();
    if kept.is_empty() {
        return;
    }
    out.push_str("# Memory\n\n");
    let mut current: Option<&str> = None;
    for (label, e) in kept {
        if current != Some(label) {
            let _ = writeln!(out, "## {label}\n");
            current = Some(label);
        }
        let _ = writeln!(out, "- [{}] {}", e.kind, e.content);
    }
    out.push('\n');
}

fn cli_section(project_scoped: bool, out: &mut String) {
    out.push_str(
        "# Mobius CLI\n\n\
        The `mobius` CLI talks back to this server (`MOBIUS_*` environment \
        variables are already set). Useful commands:\n\n\
        - `mobius research start --question \"…\" [--repo owner/name]…` — \
        delegate a code question to a read-only researcher (default repos: \
        the project's hints)\n\
        - `mobius research show ID` / `mobius research list`\n\
        - `mobius memory add --kind fact|decision|convention|gotcha \"…\"` — \
        save durable knowledge\n\
        - `mobius memory list`\n",
    );
    if project_scoped {
        out.push_str(
            "- `mobius task create --title \"…\" --brief \"…\" --repo owner/name \
             [--parent ID] [--kind feature|fix|spec|refactor|review|housekeeping]`\n\
             - `mobius task run ID` / `mobius task list` / `mobius task show ID`\n",
        );
    }
    out.push('\n');
}

/// The first-turn prompt prefix for a human-facing coordinator chat.
pub fn chat_preamble(ctx: &ChatContext) -> String {
    let mut out = String::new();
    out.push_str("# Role\n\n");
    match &ctx.project {
        Some(p) => {
            let _ = writeln!(
                out,
                "You are the coordinator for the project \"{}\" (`{}`) in \
                 organization \"{}\".",
                p.name, p.slug, ctx.org.name
            );
        }
        None => {
            let _ = writeln!(
                out,
                "You are the organization advisor for \"{}\".",
                ctx.org.name
            );
        }
    }
    out.push_str(
        "\nRules:\n\
         - You have NO access to repository code — never attempt to read or \
         modify it, and never guess at code facts.\n\
         - For facts from code, start research with `mobius research start`.\n",
    );
    if ctx.project.is_some() {
        out.push_str(
            "- For anything that must physically change, create a task with \
             `mobius task create` and encode everything the implementer needs \
             in the brief — tasks run unattended in a fresh worktree.\n",
        );
    } else {
        out.push_str(
            "- For anything that must physically change, direct it to the \
             owning project's coordinator.\n",
        );
    }
    out.push_str("- Save durable knowledge explicitly with `mobius memory add`.\n\n");

    let _ = writeln!(
        out,
        "# Organization\n\n{} (`{}`)\n",
        ctx.org.name, ctx.org.slug
    );

    if !ctx.projects.is_empty() {
        out.push_str("# Projects\n\n");
        for p in &ctx.projects {
            let repos = p
                .repository_ids
                .iter()
                .filter_map(|id| ctx.repositories.iter().find(|r| &r.id == id))
                .map(|r| format!("{}/{}", r.owner, r.name))
                .collect::<Vec<_>>()
                .join(", ");
            let _ = writeln!(out, "- `{}` — {} ({})", p.slug, p.name, p.status);
            if !p.description.trim().is_empty() {
                let _ = writeln!(out, "  {}", p.description.trim());
            }
            if !repos.is_empty() {
                let _ = writeln!(out, "  repositories: {repos}");
            }
        }
        out.push('\n');
    }

    if !ctx.repositories.is_empty() {
        out.push_str("# Related repositories\n\n");
        for r in &ctx.repositories {
            let _ = writeln!(out, "- {}", repo_label(r));
        }
        out.push('\n');
    }

    memory_section(&ctx.memory, &mut out);

    if !ctx.memory_files.is_empty() {
        out.push_str("# Memory files\n\n");
        for f in &ctx.memory_files {
            let _ = writeln!(out, "- {}", f.display());
        }
        out.push('\n');
    }

    cli_section(ctx.project.is_some(), &mut out);

    if !ctx.open_research.is_empty() {
        out.push_str("# Open research\n\n");
        for r in &ctx.open_research {
            let id8 = &r.id.to_string()[..8];
            let _ = writeln!(out, "- {id8} {} — {}", r.question, r.status);
        }
        out.push('\n');
    }
    if !ctx.open_tasks.is_empty() {
        out.push_str("# Open tasks\n\n");
        for t in &ctx.open_tasks {
            let id8 = &t.id.to_string()[..8];
            let _ = writeln!(out, "- {id8} {} — {}", t.title, t.status);
        }
        out.push('\n');
    }

    if !ctx.agent.instructions.trim().is_empty() {
        out.push_str("# Instructions\n\n");
        out.push_str(ctx.agent.instructions.trim());
        out.push_str("\n\n");
    }
    out
}

/// The "since your last message" block: finished research (with findings)
/// and task/run status changes that originated from this conversation.
/// The caller pre-filters the inputs; `None` when there is nothing new.
pub fn updates_block(research: &[Research], tasks: &[Task], runs: &[Run]) -> Option<String> {
    if research.is_empty() && tasks.is_empty() && runs.is_empty() {
        return None;
    }
    let mut out = String::from("## Since your last message\n\n");
    for r in research {
        let id8 = &r.id.to_string()[..8];
        let _ = writeln!(out, "- Research {id8} (\"{}\") — {}", r.question, r.status);
        if let Some(findings) = &r.findings {
            let _ = writeln!(out, "  {}", findings.trim());
        }
    }
    for t in tasks {
        let id8 = &t.id.to_string()[..8];
        let _ = writeln!(out, "- Task {id8} \"{}\" — {}", t.title, t.status);
    }
    for r in runs {
        let id8 = &r.id.to_string()[..8];
        let _ = writeln!(out, "- Run {id8} — {}", r.status);
        if let Some(summary) = &r.summary {
            let _ = writeln!(out, "  {}", summary.trim());
        }
    }
    Some(out)
}

/// Context for a research turn: the read-only agent answering a question.
pub struct ResearchContext {
    pub org: Organization,
    pub project: Option<Project>,
    /// Repositories the researcher may read — local paths ARE included here
    /// (researchers run with filesystem access, unlike coordinators).
    pub repositories: Vec<Repository>,
    pub memory: Vec<MemoryGroup>,
    pub research: Research,
}

pub fn research_prompt(ctx: &ResearchContext) -> String {
    let mut out = String::new();
    out.push_str(
        "# Role\n\nYou are a researcher. Answer the question below by reading \
         code — you are READ-ONLY: never modify files. Facts must cite file \
         references (path:line where possible).\n\n",
    );
    let _ = writeln!(
        out,
        "# Organization\n\n{} (`{}`)\n",
        ctx.org.name, ctx.org.slug
    );
    if let Some(p) = &ctx.project {
        let _ = writeln!(out, "# Project\n\n{} (`{}`)", p.name, p.slug);
        if !p.description.trim().is_empty() {
            let _ = writeln!(out, "\n{}", p.description.trim());
        }
        out.push('\n');
    }
    if !ctx.repositories.is_empty() {
        out.push_str("# Repositories\n\n");
        for r in &ctx.repositories {
            let _ = write!(out, "- {}", repo_label(r));
            if let Some(path) = &r.local_path {
                let _ = write!(out, " — checkout: `{}`", path.display());
            }
            out.push('\n');
        }
        out.push('\n');
    }
    memory_section(&ctx.memory, &mut out);
    let _ = writeln!(out, "# Question\n\n{}\n", ctx.research.question.trim());
    out.push_str(
        "# Deliverable\n\nEnd your final message with a `## Findings` section \
         (facts with file references) and, when useful, a `## Open questions` \
         section. The findings text is extracted verbatim — keep it \
         self-contained.\n",
    );
    out
}

/// Context for a task run: the implementer working in a fresh worktree.
pub struct RunContext {
    pub org: Organization,
    pub project: Option<Project>,
    pub repository: Repository,
    pub worktree: PathBuf,
    pub branch: String,
    /// Parent chain briefs, root first.
    pub parents: Vec<Task>,
    /// Pre-ordered org → repo → project.
    pub memory: Vec<MemoryGroup>,
    pub task: Task,
}

pub fn run_prompt(ctx: &RunContext) -> String {
    let mut out = String::new();
    out.push_str(
        "# Role\n\nYou are an implementer. You execute the task below inside \
         the repository worktree; you may read and modify code there.\n\n",
    );
    let _ = writeln!(
        out,
        "# Repository\n\n- {} — worktree `{}`, branch `{}`\n",
        repo_label(&ctx.repository),
        ctx.worktree.display(),
        ctx.branch
    );
    if let Some(p) = &ctx.project {
        let _ = writeln!(out, "# Project\n\n{} (`{}`)", p.name, p.slug);
        if !p.description.trim().is_empty() {
            let _ = writeln!(out, "\n{}", p.description.trim());
        }
        out.push('\n');
    }
    if !ctx.parents.is_empty() {
        out.push_str("# Parent tasks\n\n");
        for p in &ctx.parents {
            let _ = writeln!(out, "- {}", p.title);
            if !p.description.trim().is_empty() {
                let _ = writeln!(out, "  {}", p.description.trim());
            }
        }
        out.push('\n');
    }
    memory_section(&ctx.memory, &mut out);
    let _ = writeln!(out, "# Task: {}\n", ctx.task.title);
    if !ctx.task.description.trim().is_empty() {
        let _ = writeln!(out, "{}\n", ctx.task.description.trim());
    }
    out.push_str(
        "# Expectations\n\n\
         - Commit your changes on the current branch; do NOT push.\n\
         - End your final message with a `## Summary` section describing what \
         changed and why.\n",
    );
    let _ = writeln!(
        out,
        "- Save repository gotchas you discover with \
         `mobius memory add --kind gotcha \"…\" --scope repo:{}/{}`.\n\
         - Save project-level learnings with \
         `mobius memory add --kind fact \"…\" --scope project`.\n",
        ctx.repository.owner, ctx.repository.name
    );
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn org() -> Organization {
        Organization {
            id: OrganizationId::new(),
            name: "Mobius".into(),
            slug: "mobius".into(),
            created_at: chrono::Utc::now(),
        }
    }

    fn repo(local_path: Option<&str>) -> Repository {
        Repository {
            id: RepositoryId::new(),
            organization_id: OrganizationId::new(),
            owner: "o".into(),
            name: "r".into(),
            provider: RepoProvider::GitHub,
            default_branch: "main".into(),
            local_path: local_path.map(Into::into),
            created_at: chrono::Utc::now(),
        }
    }

    fn agent(role: AgentRole) -> Agent {
        Agent {
            id: AgentId::new(),
            name: "a".into(),
            role,
            organization_id: OrganizationId::new(),
            profiles: ActivityProfiles {
                default: ModelProfileId::new(),
                overrides: Default::default(),
            },
            project_id: None,
            repository_id: None,
            instructions: "follow the conventions".into(),
            permission_policy: PermissionPolicy::ReadOnly,
            status: AgentStatus::Idle,
            created_at: chrono::Utc::now(),
        }
    }

    fn project(org_id: OrganizationId, repo_id: RepositoryId) -> Project {
        Project {
            id: ProjectId::new(),
            organization_id: org_id,
            repository_ids: vec![repo_id],
            name: "Chat".into(),
            slug: "chat".into(),
            description: "the chat surface".into(),
            scope: ProjectScope::default(),
            status: ProjectStatus::Active,
            created_at: chrono::Utc::now(),
        }
    }

    fn entry(scope: MemoryScope, content: &str, superseded: bool) -> MemoryEntry {
        MemoryEntry {
            id: MemoryEntryId::new(),
            scope,
            kind: MemoryKind::Fact,
            content: content.into(),
            source_run_id: None,
            source_conversation_id: None,
            superseded_by: superseded.then(MemoryEntryId::new),
            created_at: chrono::Utc::now(),
        }
    }

    fn task(title: &str) -> Task {
        Task {
            id: TaskId::new(),
            project_id: ProjectId::new(),
            agent_id: AgentId::new(),
            parent_task_id: None,
            title: title.into(),
            description: "brief".into(),
            repository_id: None,
            kind: TaskKind::Feature,
            status: TaskStatus::Running,
            origin: TaskOrigin::default(),
            priority: Priority::Normal,
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
        }
    }

    #[test]
    fn coordinator_preamble_has_no_local_paths() {
        let org = org();
        let r = repo(Some("/secret/checkout/path"));
        let p = project(org.id, r.id);
        let ctx = ChatContext {
            org: org.clone(),
            agent: agent(AgentRole::Project),
            project: Some(p.clone()),
            projects: vec![p],
            repositories: vec![r],
            memory: vec![],
            memory_files: vec![],
            open_research: vec![],
            open_tasks: vec![],
        };
        let preamble = chat_preamble(&ctx);
        assert!(!preamble.contains("/secret/checkout/path"));
        assert!(preamble.contains("o/r"));
        assert!(preamble.contains("coordinator"));
        assert!(preamble.contains("mobius task create"));
        assert!(preamble.contains("follow the conventions"));
    }

    #[test]
    fn org_preamble_has_no_task_commands() {
        let org = org();
        let ctx = ChatContext {
            org: org.clone(),
            agent: agent(AgentRole::Organization),
            project: None,
            projects: vec![],
            repositories: vec![repo(None)],
            memory: vec![],
            memory_files: vec![],
            open_research: vec![],
            open_tasks: vec![],
        };
        let preamble = chat_preamble(&ctx);
        assert!(preamble.contains("organization advisor"));
        assert!(!preamble.contains("mobius task create"));
        assert!(preamble.contains("mobius research start"));
    }

    #[test]
    fn superseded_memory_excluded_and_cap_enforced() {
        let org = org();
        let scope = MemoryScope::Organization(org.id);
        // ~100 entries × ~300 chars > 24k cap → oldest dropped.
        let mut memory: Vec<MemoryEntry> = (0..100)
            .map(|i| entry(scope, &format!("{i}:{}", "x".repeat(300)), false))
            .collect();
        // The newest entries survive the cap; superseded never render.
        memory.push(entry(scope, "live fact", false));
        memory.push(entry(scope, "stale fact", true));
        let ctx = ChatContext {
            org,
            agent: agent(AgentRole::Organization),
            project: None,
            projects: vec![],
            repositories: vec![],
            memory: vec![MemoryGroup::new("Organization", memory)],
            memory_files: vec![],
            open_research: vec![],
            open_tasks: vec![],
        };
        let preamble = chat_preamble(&ctx);
        assert!(preamble.contains("## Organization"));
        assert!(preamble.contains("live fact"));
        assert!(!preamble.contains("stale fact"));
        let mem = preamble
            .split("# Memory\n\n")
            .nth(1)
            .and_then(|tail| tail.split("\n# ").next())
            .map(|block| format!("# Memory\n\n{block}"))
            .expect("memory section present");
        let section_len = mem.len();
        assert!(section_len <= MEMORY_CAP, "memory section {section_len}");
        // Entries render as "- [fact] {i}:…"; index 0 is the oldest.
        assert!(!preamble.contains("[fact] 0:"), "oldest dropped");
    }

    #[test]
    fn memory_groups_render_in_order_with_subheadings() {
        let org = org();
        let pid = ProjectId::new();
        let ctx = ChatContext {
            org: org.clone(),
            agent: agent(AgentRole::Organization),
            project: None,
            projects: vec![],
            repositories: vec![],
            memory: vec![
                MemoryGroup::new(
                    "Organization",
                    vec![entry(MemoryScope::Organization(org.id), "org fact", false)],
                ),
                MemoryGroup::new(
                    "Repository o/r",
                    vec![entry(
                        MemoryScope::Repository(RepositoryId::new()),
                        "repo fact",
                        false,
                    )],
                ),
                MemoryGroup::new("Project empty", vec![]),
                MemoryGroup::new(
                    "Project chat",
                    vec![entry(MemoryScope::Project(pid), "project fact", false)],
                ),
            ],
            memory_files: vec![],
            open_research: vec![],
            open_tasks: vec![],
        };
        let preamble = chat_preamble(&ctx);
        let org_pos = preamble.find("## Organization").expect("org group");
        let repo_pos = preamble.find("## Repository o/r").expect("repo group");
        let proj_pos = preamble.find("## Project chat").expect("project group");
        assert!(org_pos < repo_pos && repo_pos < proj_pos);
        // Empty groups don't render a heading.
        assert!(!preamble.contains("## Project empty"));
    }

    #[test]
    fn updates_block_none_when_empty() {
        assert!(updates_block(&[], &[], &[]).is_none());
        let t = task("did a thing");
        let block = updates_block(&[], std::slice::from_ref(&t), &[]).expect("non-empty");
        assert!(block.contains("Since your last message"));
        assert!(block.contains("did a thing"));
        assert!(block.contains("running"));
    }

    #[test]
    fn research_prompt_includes_paths_and_question() {
        let org = org();
        let r = repo(Some("/repos/checkout"));
        let research = Research {
            id: ResearchId::new(),
            organization_id: org.id,
            project_id: None,
            repository_ids: vec![r.id],
            question: "where is routing?".into(),
            status: ResearchStatus::Running,
            findings: None,
            conversation_id: None,
            origin_conversation_id: None,
            model_profile_id: None,
            created_at: chrono::Utc::now(),
            finished_at: None,
        };
        let ctx = ResearchContext {
            org,
            project: None,
            repositories: vec![r],
            memory: vec![],
            research,
        };
        let p = research_prompt(&ctx);
        assert!(p.contains("/repos/checkout"));
        assert!(p.contains("where is routing?"));
        assert!(p.contains("## Findings"));
        assert!(p.contains("READ-ONLY"));
    }

    #[test]
    fn run_prompt_includes_worktree_branch_and_task() {
        let org = org();
        let r = repo(Some("/repos/checkout"));
        let ctx = RunContext {
            org,
            project: None,
            repository: r,
            worktree: PathBuf::from("/data/worktrees/r-abc"),
            branch: "mobius/task-abc12345".into(),
            parents: vec![],
            memory: vec![],
            task: task("add changelog stub"),
        };
        let p = run_prompt(&ctx);
        assert!(p.contains("/data/worktrees/r-abc"));
        assert!(p.contains("mobius/task-abc12345"));
        assert!(p.contains("add changelog stub"));
        assert!(p.contains("## Summary"));
        assert!(p.contains("do NOT push"));
        // The full memory-add command spells out the real repo scope.
        assert!(p.contains("--scope repo:o/r"));
        assert!(p.contains("--scope project"));
    }
}
