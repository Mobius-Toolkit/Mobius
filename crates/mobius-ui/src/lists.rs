//! Work page (tasks grouped by project + research) and the Runs page.

use crate::{Data, Nav, api, panels};
use dioxus::prelude::*;
use mobius_api::*;
use mobius_core::*;

fn fmt_dt(d: &chrono::DateTime<chrono::Utc>) -> String {
    d.format("%Y-%m-%d %H:%M").to_string()
}

/// Work page: tasks grouped by project (hierarchy, run/cancel, latest run
/// link), a new-task form, and a research section.
#[component]
pub fn WorkPage() -> Element {
    let mut data: Data = use_context();
    let nav: Nav = use_context();
    let mut title = use_signal(String::new);
    let mut brief = use_signal(String::new);
    let mut project = use_signal(|| None::<ProjectId>);
    let mut repo = use_signal(|| None::<RepositoryId>);
    let mut parent = use_signal(|| None::<TaskId>);
    let mut kind = use_signal(|| "feature".to_string());

    let projects = {
        let mut p = data.projects.read().clone();
        p.sort_by(|a, b| a.slug.cmp(&b.slug));
        p
    };
    let tasks = data.tasks.read().clone();
    let repos = data.repos.read().clone();
    let runs = data.runs.read().clone();
    let research = data.research.read().clone();

    // Repos hinted by the currently-selected project (for the new-task form).
    let hinted: Vec<Repository> = project()
        .and_then(|pid| projects.iter().find(|p| p.id == pid))
        .map(|p| {
            repos
                .iter()
                .filter(|r| p.repository_ids.contains(&r.id))
                .cloned()
                .collect()
        })
        .unwrap_or_default();
    // Parent candidates: tasks in the selected project.
    let parent_candidates: Vec<Task> = project()
        .map(|pid| {
            tasks
                .iter()
                .filter(|t| t.project_id == pid)
                .cloned()
                .collect()
        })
        .unwrap_or_default();

    rsx! {
        div { class: "page",
            h2 { "Tasks" }
            div { class: "form-inline",
                select {
                    onchange: move |e| {
                        project.set(e.value().parse::<ProjectId>().ok());
                        repo.set(None);
                        parent.set(None);
                    },
                    option { value: "", "project…" }
                    for p in projects.iter() {
                        option {
                            key: "{p.id}",
                            value: "{p.id}",
                            selected: project() == Some(p.id),
                            "{p.slug}"
                        }
                    }
                }
                select {
                    onchange: move |e| repo.set(e.value().parse::<RepositoryId>().ok()),
                    option { value: "", "repo…" }
                    for r in hinted.iter() {
                        option {
                            key: "{r.id}",
                            value: "{r.id}",
                            selected: repo() == Some(r.id),
                            "{r.owner}/{r.name}"
                        }
                    }
                }
                select {
                    onchange: move |e| parent.set(e.value().parse::<TaskId>().ok()),
                    option { value: "", "parent…" }
                    for t in parent_candidates.iter() {
                        option {
                            key: "{t.id}",
                            value: "{t.id}",
                            selected: parent() == Some(t.id),
                            "{t.title}"
                        }
                    }
                }
                select { onchange: move |e| kind.set(e.value()),
                    for k in ["feature", "fix", "spec", "refactor", "review", "housekeeping"] {
                        option { key: "{k}", value: "{k}", selected: kind() == *k, "{k}" }
                    }
                }
                input {
                    placeholder: "title",
                    value: "{title.read()}",
                    oninput: move |e| title.set(e.value()),
                }
                input {
                    placeholder: "brief",
                    value: "{brief.read()}",
                    oninput: move |e| brief.set(e.value()),
                }
                button {
                    onclick: move |_| {
                        let (p, r, pa, t, d, k) =
                            (project(), repo(), parent(), title(), brief(), kind());
                        let Some(p) = p else {
                            data.error.set(Some("pick a project first".into()));
                            return;
                        };
                        if t.trim().is_empty() {
                            return;
                        }
                        spawn(async move {
                            let res = api::post::<CreateTask, Task>(
                                "/tasks",
                                &CreateTask {
                                    project_id: p,
                                    repository_id: r,
                                    title: t,
                                    description: d,
                                    kind: k.parse().ok(),
                                    parent_task_id: pa,
                                    priority: None,
                                    origin_conversation_id: None,
                                },
                            )
                            .await;
                            match res {
                                Ok(_) => {
                                    title.set(String::new());
                                    brief.set(String::new());
                                    data.refresh_kind(EntityKind::Task);
                                }
                                Err(e) => data.error.set(Some(e)),
                            }
                        });
                    },
                    "Create"
                }
            }
            for p in projects.iter() {
                {
                    let rows = panels::task_tree(
                        &tasks
                            .iter()
                            .filter(|t| t.project_id == p.id)
                            .cloned()
                            .collect::<Vec<_>>(),
                    );
                    rsx! {
                        div { class: "work-group", key: "{p.id}",
                            h4 { "{p.slug}" }
                            for (t, depth) in rows {
                                panels::TaskRow {
                                    key: "{t.id}",
                                    task: t,
                                    indent: depth,
                                    runs: runs.clone(),
                                    repos: repos.clone(),
                                }
                            }
                        }
                    }
                }
            }
            h2 { "Research" }
            for r in research.iter() {
                div { class: "row research-item", key: "{r.id}",
                    span { class: "badge", "{r.status}" }
                    span { "{r.question}" }
                    span { class: "muted", " {panels::fmt_research_meta(r)}" }
                    if let Some(cid) = r.conversation_id {
                        button {
                            class: "link",
                            onclick: move |_| nav.open_conversation(cid),
                            "open"
                        }
                    }
                }
            }
        }
    }
}

#[component]
pub fn RunsPage() -> Element {
    let data: Data = use_context();
    let nav: Nav = use_context();
    rsx! {
        div { class: "page",
            h2 { "Runs" }
            table {
                thead {
                    tr {
                        th { "Run" }
                        th { "Task" }
                        th { "Status" }
                        th { "Transcript" }
                        th { "Summary" }
                        th { "Started" }
                    }
                }
                tbody {
                    for r in data.runs.read().iter() {
                        tr { key: "{r.id}",
                            td { class: "mono", "{r.id}" }
                            td { class: "mono", "{r.task_id}" }
                            td { "{r.status}" }
                            td {
                                if let Some(cid) = r.conversation_id {
                                    button {
                                        class: "link",
                                        onclick: move |_| nav.open_conversation(cid),
                                        "open"
                                    }
                                }
                            }
                            td { "{r.summary.clone().unwrap_or_default()}" }
                            td { class: "muted", "{fmt_dt(&r.started_at)}" }
                        }
                    }
                }
            }
        }
    }
}
