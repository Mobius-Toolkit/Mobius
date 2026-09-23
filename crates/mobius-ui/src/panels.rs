//! Right-column panels on the chat page: Memory, Research, Tasks — all
//! scoped to the open conversation's project (or organization).

use crate::{Data, Nav, api};
use dioxus::prelude::*;
use mobius_api::*;
use mobius_core::*;

fn badge(text: &str) -> Element {
    rsx! { span { class: "badge", "{text}" } }
}

/// A memory row plus a resolved "source" link (chat or run conversation).
type MemRow = (MemoryEntry, Option<(ConversationId, &'static str)>);

fn fmt_dt(d: &chrono::DateTime<chrono::Utc>) -> String {
    d.format("%m-%d %H:%M").to_string()
}

// -------------------------------------------------------------------- Memory

#[component]
pub fn MemoryPanel(conv: Conversation, on_close: Callback<()>) -> Element {
    let mut data: Data = use_context();
    let nav: Nav = use_context();
    let mut kind = use_signal(|| "fact".to_string());
    let mut content = use_signal(String::new);

    // Sections: project entries first, then org, then the hinted repos.
    // Each row carries a resolved "source" link (chat or run conversation).
    let mut sections: Vec<(String, Vec<MemRow>)> = Vec::new();
    {
        let all = data.memory.read().clone();
        let runs = data.runs.read().clone();
        let projects = data.projects.read().clone();
        let repos = data.repos.read().clone();
        let mut pick = |label: String, scope: MemoryScope| {
            let rows: Vec<MemRow> = all
                .iter()
                .filter(|e| e.scope == scope)
                .map(|e| {
                    let link = e.source_conversation_id.map(|c| (c, "chat")).or_else(|| {
                        e.source_run_id.and_then(|rid| {
                            runs.iter()
                                .find(|r| r.id == rid)
                                .and_then(|r| r.conversation_id)
                                .map(|c| (c, "run"))
                        })
                    });
                    (e.clone(), link)
                })
                .collect();
            if !rows.is_empty() {
                sections.push((label, rows));
            }
        };
        if let Some(pid) = conv.project_id {
            pick("project".into(), MemoryScope::Project(pid));
            if let Some(p) = projects.iter().find(|p| p.id == pid) {
                for rid in &p.repository_ids {
                    if let Some(r) = repos.iter().find(|r| r.id == *rid) {
                        pick(
                            format!("repo {}/{}", r.owner, r.name),
                            MemoryScope::Repository(*rid),
                        );
                    }
                }
            }
        }
        pick(
            "org".into(),
            MemoryScope::Organization(conv.organization_id),
        );
    }

    rsx! {
        div { class: "panel",
            div { class: "panel-head",
                h4 { "Memory" }
                button { onclick: move |_| on_close.call(()), "×" }
            }
            for (label, entries) in sections {
                h5 { class: "panel-group", key: "{label}", "{label}" }
                for (e, link) in entries {
                    div { class: "mem-entry", key: "{e.id}",
                        {badge(&e.kind.to_string())}
                        span { class: "mem-content", "{e.content}" }
                        if let Some((cid, what)) = link {
                            button {
                                class: "link",
                                title: "open source conversation",
                                onclick: move |_| nav.open_conversation(cid),
                                "{what}"
                            }
                        }
                        button {
                            class: "link danger",
                            onclick: move |_| {
                                let id = e.id;
                                spawn(async move {
                                    let _ = api::delete(&format!("/memory/{id}")).await;
                                    data.refresh_kind(EntityKind::MemoryEntry);
                                });
                            },
                            "×"
                        }
                    }
                }
            }
            div { class: "mem-add",
                select { onchange: move |e| kind.set(e.value()),
                    for k in ["fact", "decision", "convention", "gotcha", "summary"] {
                        option { key: "{k}", value: "{k}", selected: kind() == *k, "{k}" }
                    }
                }
                textarea {
                    rows: "2",
                    placeholder: "Durable fact for this chat's scope…",
                    value: "{content}",
                    oninput: move |e| content.set(e.value()),
                }
                button {
                    onclick: move |_| {
                        let text = content();
                        if text.trim().is_empty() {
                            return;
                        }
                        let k = kind().parse::<MemoryKind>().unwrap_or(MemoryKind::Fact);
                        let conv_id = conv.id;
                        let mscope = conv.memory_scope();
                        spawn(async move {
                            let r = api::post::<CreateMemoryEntry, MemoryEntry>(
                                "/memory",
                                &CreateMemoryEntry {
                                    scope: mscope,
                                    kind: k,
                                    content: text,
                                    source_conversation_id: Some(conv_id),
                                },
                            )
                            .await;
                            match r {
                                Ok(_) => {
                                    content.set(String::new());
                                    data.refresh_kind(EntityKind::MemoryEntry);
                                }
                                Err(e) => data.error.set(Some(e)),
                            }
                        });
                    },
                    "Add"
                }
            }
        }
    }
}

// ------------------------------------------------------------------ Research

#[component]
pub fn ResearchPanel(conv: Conversation, on_close: Callback<()>) -> Element {
    let mut data: Data = use_context();
    let nav: Nav = use_context();
    let mut question = use_signal(String::new);
    let mut checked = use_signal(Vec::<RepositoryId>::new);
    let mut expanded = use_signal(|| None::<ResearchId>);

    let items: Vec<Research> = data
        .research
        .read()
        .iter()
        .filter(|r| match conv.project_id {
            Some(pid) => r.project_id == Some(pid),
            None => r.organization_id == conv.organization_id && r.project_id.is_none(),
        })
        .cloned()
        .collect();

    // Candidate repositories: the project's hints; org chats get every org
    // repo with a checkout.
    let candidates: Vec<Repository> = match conv
        .project_id
        .and_then(|pid| data.projects.read().iter().find(|p| p.id == pid).cloned())
    {
        Some(p) => data
            .repos
            .read()
            .iter()
            .filter(|r| p.repository_ids.contains(&r.id))
            .cloned()
            .collect(),
        None => data
            .repos
            .read()
            .iter()
            .filter(|r| r.organization_id == conv.organization_id && r.local_path.is_some())
            .cloned()
            .collect(),
    };
    // Precheck everything once the candidates are known.
    if checked.read().is_empty() && !candidates.is_empty() {
        let ids: Vec<RepositoryId> = candidates.iter().map(|r| r.id).collect();
        checked.set(ids);
    }

    rsx! {
        div { class: "panel",
            div { class: "panel-head",
                h4 { "Research" }
                button { onclick: move |_| on_close.call(()), "×" }
            }
            for r in items {
                div { class: "research-item", key: "{r.id}",
                    div {
                        class: "row",
                        onclick: move |_| {
                            expanded.set(if expanded() == Some(r.id) { None } else { Some(r.id) });
                        },
                        {badge(&r.status.to_string())}
                        span { class: "research-q", "{r.question}" }
                        if let Some(cid) = r.conversation_id {
                            button {
                                class: "link",
                                onclick: move |e| {
                                    e.stop_propagation();
                                    nav.open_conversation(cid);
                                },
                                "open"
                            }
                        }
                    }
                    if expanded() == Some(r.id) {
                        if let Some(f) = &r.findings {
                            pre { class: "findings", "{f}" }
                        } else {
                            p { class: "muted", "no findings yet" }
                        }
                    }
                }
            }
            div { class: "mem-add",
                textarea {
                    rows: "2",
                    placeholder: "Ask a code question (read-only researcher)…",
                    value: "{question}",
                    oninput: move |e| question.set(e.value()),
                }
                for repo in candidates {
                    label { class: "checkrow", key: "{repo.id}",
                        input {
                            r#type: "checkbox",
                            checked: checked.read().contains(&repo.id),
                            onchange: move |e| {
                                let id = repo.id;
                                if e.checked() {
                                    checked.write().push(id);
                                } else {
                                    checked.write().retain(|x| *x != id);
                                }
                            },
                        }
                        " {repo.owner}/{repo.name}"
                    }
                }
                button {
                    onclick: move |_| {
                        let q = question();
                        if q.trim().is_empty() {
                            return;
                        }
                        let repos = checked.read().clone();
                        let conv_id = conv.id;
                        let org_id = conv.organization_id;
                        let proj_id = conv.project_id;
                        spawn(async move {
                            let r = api::post::<StartResearch, Research>(
                                "/research",
                                &StartResearch {
                                    organization_id: Some(org_id),
                                    project_id: proj_id,
                                    repositories: repos,
                                    question: q,
                                    origin_conversation_id: Some(conv_id),
                                    model_profile_id: None,
                                },
                            )
                            .await;
                            match r {
                                Ok(_) => {
                                    question.set(String::new());
                                    data.refresh_kind(EntityKind::Research);
                                }
                                Err(e) => data.error.set(Some(e)),
                            }
                        });
                    },
                    "Ask"
                }
            }
        }
    }
}

// --------------------------------------------------------------------- Tasks

/// Depth-first task rows: (task, indent depth). Children render right
/// under their parent.
pub fn task_tree(tasks: &[Task]) -> Vec<(Task, usize)> {
    fn visit(tasks: &[Task], parent: Option<TaskId>, depth: usize, out: &mut Vec<(Task, usize)>) {
        let mut kids: Vec<&Task> = tasks
            .iter()
            .filter(|t| t.parent_task_id == parent)
            .collect();
        kids.sort_by_key(|t| t.created_at);
        for t in kids {
            out.push((t.clone(), depth));
            visit(tasks, Some(t.id), depth + 1, out);
        }
    }
    let mut out = Vec::new();
    visit(tasks, None, 0, &mut out);
    out
}

/// Runnable states for the Run button.
fn can_run(status: TaskStatus) -> bool {
    use TaskStatus::*;
    matches!(status, Proposed | Approved | Queued | Failed)
}

fn terminal(status: TaskStatus) -> bool {
    use TaskStatus::*;
    matches!(status, Done | Cancelled | Failed)
}

/// One task row — shared by the chat-side Tasks panel and the Work page.
#[component]
pub fn TaskRow(task: Task, runs: Vec<Run>, repos: Vec<Repository>, indent: usize) -> Element {
    let mut data: Data = use_context();
    let nav: Nav = use_context();
    let t = task.clone();
    let latest: Option<Run> = runs
        .iter()
        .filter(|r| r.task_id == t.id)
        .max_by_key(|r| r.started_at)
        .cloned();
    let repo_label = t
        .repository_id
        .and_then(|rid| {
            repos
                .iter()
                .find(|r| r.id == rid)
                .map(|r| format!("{}/{}", r.owner, r.name))
        })
        .unwrap_or_else(|| "-".into());
    rsx! {
        div {
            class: "task-row",
            style: "padding-left: {indent * 16}px",
            div { class: "task-top",
                {badge(&t.status.to_string())}
                span { class: "muted", "{t.kind}" }
                span { class: "task-title", "{t.title}" }
            }
            div { class: "task-sub",
                span { class: "muted", "{repo_label}" }
                if let Some(r) = latest {
                    button {
                        class: "link",
                        title: "open run transcript",
                        onclick: move |_| {
                            if let Some(cid) = r.conversation_id {
                                nav.open_conversation(cid);
                            }
                        },
                        "{r.status}"
                    }
                }
                if can_run(t.status) {
                    button {
                        class: "link",
                        onclick: move |_| {
                            let id = t.id;
                            spawn(async move {
                                let r = api::post_empty::<serde_json::Value>(
                                    &format!("/tasks/{id}/run"),
                                    &serde_json::json!({}),
                                )
                                .await;
                                if let Err(e) = r {
                                    data.error.set(Some(e));
                                }
                                data.refresh_kind(EntityKind::Task);
                                data.refresh_kind(EntityKind::Run);
                            });
                        },
                        "Run"
                    }
                }
                if !terminal(t.status) {
                    button {
                        class: "link danger",
                        onclick: move |_| {
                            let id = t.id;
                            spawn(async move {
                                let _ = api::post_empty::<serde_json::Value>(
                                    &format!("/tasks/{id}/cancel"),
                                    &serde_json::json!({}),
                                )
                                .await;
                                data.refresh_kind(EntityKind::Task);
                            });
                        },
                        "Cancel"
                    }
                }
            }
        }
    }
}

#[component]
pub fn TasksPanel(conv: Conversation, on_close: Callback<()>) -> Element {
    let data: Data = use_context();
    let pid = match conv.project_id {
        Some(pid) => pid,
        None => {
            return rsx! {
                div { class: "panel",
                    div { class: "panel-head",
                        h4 { "Tasks" }
                        button { onclick: move |_| on_close.call(()), "×" }
                    }
                    p { class: "muted", "Tasks live under a project." }
                }
            };
        }
    };
    let rows = {
        let tasks: Vec<Task> = data
            .tasks
            .read()
            .iter()
            .filter(|t| t.project_id == pid)
            .cloned()
            .collect();
        task_tree(&tasks)
    };
    let empty = rows.is_empty();
    rsx! {
        div { class: "panel",
            div { class: "panel-head",
                h4 { "Tasks" }
                button { onclick: move |_| on_close.call(()), "×" }
            }
            for (t, depth) in rows {
                TaskRow {
                    key: "{t.id}",
                    task: t,
                    indent: depth,
                    runs: data.runs.read().clone(),
                    repos: data.repos.read().clone(),
                }
            }
            if empty {
                p { class: "muted", "No tasks yet." }
            }
            p { class: "muted", "Create tasks from the Work page or `mobius task create`." }
        }
    }
}

/// Count helpers for the header toggle buttons.
pub fn memory_count(data: &Data, conv: &Conversation) -> usize {
    let mut scopes = vec![MemoryScope::Organization(conv.organization_id)];
    if let Some(pid) = conv.project_id {
        scopes.push(MemoryScope::Project(pid));
        if let Some(p) = data.projects.read().iter().find(|p| p.id == pid) {
            scopes.extend(
                p.repository_ids
                    .iter()
                    .map(|id| MemoryScope::Repository(*id)),
            );
        }
    }
    data.memory
        .read()
        .iter()
        .filter(|e| scopes.contains(&e.scope))
        .count()
}

pub fn research_count(data: &Data, conv: &Conversation) -> usize {
    data.research
        .read()
        .iter()
        .filter(|r| match conv.project_id {
            Some(pid) => r.project_id == Some(pid),
            None => r.organization_id == conv.organization_id && r.project_id.is_none(),
        })
        .count()
}

pub fn task_count(data: &Data, conv: &Conversation) -> usize {
    match conv.project_id {
        Some(pid) => data
            .tasks
            .read()
            .iter()
            .filter(|t| t.project_id == pid)
            .count(),
        None => 0,
    }
}

/// Used by the Work page research section.
pub fn fmt_research_meta(r: &Research) -> String {
    format!("{} · {}", r.status, fmt_dt(&r.created_at))
}
