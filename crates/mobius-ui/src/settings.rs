//! Settings pages: create/edit/delete forms for domain configuration.

use crate::{Data, api};
use dioxus::prelude::*;
use mobius_api::*;
use mobius_core::*;
use std::collections::BTreeMap;

const POLICIES: &[&str] = &["read_only", "workspace_edits", "auto", "ask_human"];
const ROLES: &[&str] = &[
    "organization",
    "project",
    "researcher",
    "reviewer",
    "housekeeper",
];
const EFFORTS: &[&str] = &["", "low", "medium", "high", "max"];
const PROJECT_STATUSES: &[&str] = &["active", "paused", "archived"];
const AGENT_STATUSES: &[&str] = &["idle", "busy", "disabled"];
const ACTIVITIES: &[&str] = &[
    "plan",
    "implement",
    "research",
    "review",
    "triage",
    "housekeeping",
    "chat",
];

fn parse_kv(lines: &str) -> BTreeMap<String, String> {
    lines
        .lines()
        .filter_map(|l| l.split_once('='))
        .map(|(k, v)| (k.trim().to_string(), v.trim().to_string()))
        .collect()
}

fn kv_string(map: &BTreeMap<String, String>) -> String {
    map.iter()
        .map(|(k, v)| format!("{k}={v}"))
        .collect::<Vec<_>>()
        .join("\n")
}

fn csv(s: &str) -> Vec<String> {
    s.split(',')
        .map(|x| x.trim().to_string())
        .filter(|x| !x.is_empty())
        .collect()
}

fn split_ws(s: &str) -> Vec<String> {
    s.split_whitespace().map(|x| x.to_string()).collect()
}

fn delete_by_id(mut data: Data, path: String) {
    spawn(async move {
        if let Err(e) = api::delete(&path).await {
            data.error.set(Some(e));
        }
        data.refresh();
    });
}

// ---------------------------------------------------------------- Repositories

#[component]
fn RepoRow(
    repo: Repository,
    on_edit: Callback<Repository>,
    on_delete: Callback<RepositoryId>,
) -> Element {
    let repo_id = repo.id;
    rsx! {
        tr {
            td { "{repo.owner}/{repo.name}" }
            td { "{repo.default_branch}" }
            td { class: "muted", "{repo.local_path.as_ref().map(|p| p.display().to_string()).unwrap_or_default()}" }
            td {
                button { onclick: move |_| on_edit.call(repo.clone()), "edit" }
                button { onclick: move |_| on_delete.call(repo_id), "delete" }
            }
        }
    }
}

#[component]
pub fn RepositoriesPage() -> Element {
    let mut data: Data = use_context();
    let mut editing = use_signal(|| None::<RepositoryId>);
    let mut org = use_signal(String::new);
    let mut owner = use_signal(String::new);
    let mut name = use_signal(String::new);
    let mut branch = use_signal(|| "main".to_string());
    let mut path = use_signal(String::new);

    let on_edit = Callback::new(move |r: Repository| {
        editing.set(Some(r.id));
        org.set(r.organization_id.to_string());
        owner.set(r.owner.clone());
        name.set(r.name.clone());
        branch.set(r.default_branch.clone());
        path.set(
            r.local_path
                .as_ref()
                .map(|p| p.display().to_string())
                .unwrap_or_default(),
        );
    });
    let on_delete = Callback::new(move |id: RepositoryId| {
        delete_by_id(data, format!("/repositories/{id}"));
    });

    let submit = move |_| {
        let Ok(oid) = org.read().parse::<OrganizationId>() else {
            data.error.set(Some("pick an organization".into()));
            return;
        };
        let (o, n, b, p) = (
            owner.read().clone(),
            name.read().clone(),
            branch.read().clone(),
            path.read().clone(),
        );
        let edit = editing();
        spawn(async move {
            let r = if let Some(id) = edit {
                api::put::<UpdateRepository, Repository>(
                    &format!("/repositories/{id}"),
                    &UpdateRepository {
                        owner: o,
                        name: n,
                        provider: RepoProvider::GitHub,
                        default_branch: b,
                        local_path: if p.is_empty() { None } else { Some(p) },
                    },
                )
                .await
                .map(|_| ())
            } else {
                api::post::<CreateRepository, Repository>(
                    "/repositories",
                    &CreateRepository {
                        organization_id: oid,
                        owner: o,
                        name: n,
                        provider: None,
                        default_branch: Some(b),
                        local_path: if p.is_empty() { None } else { Some(p) },
                    },
                )
                .await
                .map(|_| ())
            };
            match r {
                Err(e) => data.error.set(Some(e)),
                Ok(()) => {
                    editing.set(None);
                    data.refresh();
                }
            }
        });
    };

    rsx! {
        h2 { "Repositories" }
        table {
            thead { tr { th { "Owner/Name" }, th { "Branch" }, th { "Local path" }, th { "" } } }
            tbody {
                for r in data.repos.read().clone() {
                    RepoRow { key: "{r.id}", repo: r, on_edit, on_delete }
                }
            }
        }
        h3 { if editing().is_some() { "Edit repository" } else { "New repository" } }
        div { class: "form",
            label { "Organization" }
            select { onchange: move |e| org.set(e.value()),
                option { value: "", "—" }
                for o in data.orgs.read().clone() {
                    option { key: "{o.id}", value: "{o.id}", selected: org() == o.id.to_string(), "{o.name}" }
                }
            }
            label { "Owner" }
            input { value: "{owner}", oninput: move |e| owner.set(e.value()) }
            label { "Name" }
            input { value: "{name}", oninput: move |e| name.set(e.value()) }
            label { "Default branch" }
            input { value: "{branch}", oninput: move |e| branch.set(e.value()) }
            label { "Local path" }
            input { value: "{path}", oninput: move |e| path.set(e.value()) }
            button { onclick: submit, "Save" }
        }
    }
}

// ------------------------------------------------------------------ Projects

#[component]
fn ProjectRow(
    project: Project,
    on_edit: Callback<Project>,
    on_delete: Callback<ProjectId>,
) -> Element {
    let project_id = project.id;
    rsx! {
        tr {
            td { "{project.slug}" }
            td { "{project.name}" }
            td { class: "muted", "{project.scope.keywords.join(\", \")}" }
            td { "{project.status}" }
            td {
                button { onclick: move |_| on_edit.call(project.clone()), "edit" }
                button { onclick: move |_| on_delete.call(project_id), "delete" }
            }
        }
    }
}

#[component]
pub fn ProjectsPage() -> Element {
    let mut data: Data = use_context();
    let mut editing = use_signal(|| None::<ProjectId>);
    let mut org = use_signal(String::new);
    let mut repos_hint = use_signal(Vec::<RepositoryId>::new);
    let mut name = use_signal(String::new);
    let mut slug = use_signal(String::new);
    let mut desc = use_signal(String::new);
    let mut keywords = use_signal(String::new);
    let mut labels = use_signal(String::new);
    let mut status = use_signal(|| "active".to_string());

    let on_edit = Callback::new(move |p: Project| {
        editing.set(Some(p.id));
        org.set(p.organization_id.to_string());
        repos_hint.set(p.repository_ids.clone());
        name.set(p.name.clone());
        slug.set(p.slug.clone());
        desc.set(p.description.clone());
        keywords.set(p.scope.keywords.join(", "));
        labels.set(p.scope.labels.join(", "));
        status.set(p.status.to_string());
    });
    let on_delete = Callback::new(move |id: ProjectId| {
        delete_by_id(data, format!("/projects/{id}"));
    });

    let submit = move |_| {
        let oid = org.read().parse::<OrganizationId>().ok();
        // Related repositories are a routing hint, not an ownership link.
        let rids: Vec<RepositoryId> = repos_hint.read().clone();
        let scope = ProjectScope {
            paths: vec![],
            labels: csv(&labels.read()),
            keywords: csv(&keywords.read()),
        };
        let st = status
            .read()
            .parse::<ProjectStatus>()
            .unwrap_or(ProjectStatus::Active);
        let (n, sl, d) = (
            name.read().clone(),
            slug.read().clone(),
            desc.read().clone(),
        );
        let edit = editing();
        spawn(async move {
            let r = if let Some(id) = edit {
                api::put::<UpdateProject, Project>(
                    &format!("/projects/{id}"),
                    &UpdateProject {
                        name: n,
                        slug: sl,
                        description: d,
                        scope,
                        status: st,
                        repository_ids: rids,
                    },
                )
                .await
                .map(|_| ())
            } else {
                api::post::<CreateProject, Project>(
                    "/projects",
                    &CreateProject {
                        organization_id: oid,
                        repository_ids: rids,
                        name: n,
                        slug: sl,
                        description: d,
                        scope,
                        status: Some(st),
                    },
                )
                .await
                .map(|_| ())
            };
            match r {
                Err(e) => data.error.set(Some(e)),
                Ok(()) => {
                    editing.set(None);
                    data.refresh();
                }
            }
        });
    };

    rsx! {
        h2 { "Projects" }
        table {
            thead { tr { th { "Slug" }, th { "Name" }, th { "Keywords" }, th { "Status" }, th { "" } } }
            tbody {
                for p in data.projects.read().clone() {
                    ProjectRow { key: "{p.id}", project: p, on_edit, on_delete }
                }
            }
        }
        h3 { if editing().is_some() { "Edit project" } else { "New project" } }
        div { class: "form",
            label { "Organization" }
            select { onchange: move |e| org.set(e.value()),
                option { value: "", "—" }
                for o in data.orgs.read().clone() {
                    option { key: "{o.id}", value: "{o.id}", selected: org() == o.id.to_string(), "{o.name}" }
                }
            }
            label { "Related repositories (hint)" }
            div { class: "checklist",
                for r in data.repos.read().clone() {
                    label { class: "checkrow", key: "{r.id}",
                        input {
                            r#type: "checkbox",
                            checked: repos_hint.read().contains(&r.id),
                            onchange: move |e| {
                                let id = r.id;
                                if e.checked() {
                                    repos_hint.write().push(id);
                                } else {
                                    repos_hint.write().retain(|x| *x != id);
                                }
                            },
                        }
                        " {r.owner}/{r.name}"
                    }
                }
            }
            label { "Name" }
            input { value: "{name}", oninput: move |e| name.set(e.value()) }
            label { "Slug" }
            input { value: "{slug}", oninput: move |e| slug.set(e.value()) }
            label { "Description" }
            input { value: "{desc}", oninput: move |e| desc.set(e.value()) }
            label { "Keywords (comma separated)" }
            input { value: "{keywords}", oninput: move |e| keywords.set(e.value()) }
            label { "Labels (comma separated)" }
            input { value: "{labels}", oninput: move |e| labels.set(e.value()) }
            label { "Status" }
            select { onchange: move |e| status.set(e.value()),
                for s in PROJECT_STATUSES {
                    option { key: "{s}", value: "{s}", selected: status() == *s, "{s}" }
                }
            }
            button { onclick: submit, "Save" }
        }
    }
}

// ------------------------------------------------------------- Model profiles

#[component]
fn ProfileRow(
    profile: ModelProfile,
    harness_name: String,
    on_edit: Callback<ModelProfile>,
    on_delete: Callback<ModelProfileId>,
) -> Element {
    let profile_id = profile.id;
    rsx! {
        tr {
            td { "{profile.name}" }
            td { "{harness_name}" }
            td { "{profile.model.clone().unwrap_or_default()}" }
            td { "{profile.effort.map(|e| e.to_string()).unwrap_or_default()}" }
            td {
                button { onclick: move |_| on_edit.call(profile.clone()), "edit" }
                button { onclick: move |_| on_delete.call(profile_id), "delete" }
            }
        }
    }
}

#[component]
pub fn ProfilesPage() -> Element {
    let mut data: Data = use_context();
    let mut editing = use_signal(|| None::<ModelProfileId>);
    let mut name = use_signal(String::new);
    let mut harness = use_signal(String::new);
    let mut model = use_signal(String::new);
    let mut effort = use_signal(String::new);
    let mut config = use_signal(String::new);

    let on_edit = Callback::new(move |p: ModelProfile| {
        editing.set(Some(p.id));
        name.set(p.name.clone());
        harness.set(p.harness_id.to_string());
        model.set(p.model.clone().unwrap_or_default());
        effort.set(p.effort.map(|e| e.to_string()).unwrap_or_default());
        config.set(kv_string(&p.config));
    });
    let on_delete = Callback::new(move |id: ModelProfileId| {
        delete_by_id(data, format!("/model_profiles/{id}"));
    });

    let submit = move |_| {
        let Ok(hid) = harness.read().parse::<HarnessId>() else {
            data.error.set(Some("pick a harness".into()));
            return;
        };
        let m = model.read().clone();
        let eff = effort.read().clone();
        let eff = if eff.is_empty() {
            None
        } else {
            eff.parse::<Effort>().ok()
        };
        let cfg = parse_kv(&config.read());
        let n = name.read().clone();
        let edit = editing();
        spawn(async move {
            let r = if let Some(id) = edit {
                api::put::<UpdateModelProfile, ModelProfile>(
                    &format!("/model_profiles/{id}"),
                    &UpdateModelProfile {
                        name: n,
                        harness_id: hid,
                        model: if m.is_empty() { None } else { Some(m) },
                        effort: eff,
                        config: cfg,
                    },
                )
                .await
                .map(|_| ())
            } else {
                api::post::<CreateModelProfile, ModelProfile>(
                    "/model_profiles",
                    &CreateModelProfile {
                        name: n,
                        harness_id: hid,
                        model: if m.is_empty() { None } else { Some(m) },
                        effort: eff,
                        config: cfg,
                    },
                )
                .await
                .map(|_| ())
            };
            match r {
                Err(e) => data.error.set(Some(e)),
                Ok(()) => {
                    editing.set(None);
                    data.refresh();
                }
            }
        });
    };

    rsx! {
        h2 { "Model profiles" }
        table {
            thead { tr { th { "Name" }, th { "Harness" }, th { "Model" }, th { "Effort" }, th { "" } } }
            tbody {
                for p in data.profiles.read().clone() {
                    ProfileRow {
                        key: "{p.id}",
                        profile: p.clone(),
                        harness_name: data.harness_name(p.harness_id),
                        on_edit,
                        on_delete,
                    }
                }
            }
        }
        h3 { if editing().is_some() { "Edit profile" } else { "New profile" } }
        div { class: "form",
            label { "Name" }
            input { value: "{name}", oninput: move |e| name.set(e.value()) }
            label { "Harness" }
            select { onchange: move |e| harness.set(e.value()),
                option { value: "", "—" }
                for h in data.harnesses.read().clone() {
                    option { key: "{h.id}", value: "{h.id}", selected: harness() == h.id.to_string(), "{h.name}" }
                }
            }
            label { "Model" }
            input { value: "{model}", oninput: move |e| model.set(e.value()) }
            label { "Effort" }
            select { onchange: move |e| effort.set(e.value()),
                for ef in EFFORTS {
                    option { key: "{ef}", value: "{ef}", selected: effort() == *ef, "{ef}" }
                }
            }
            label { "Config overrides (key=value per line)" }
            textarea { value: "{config}", oninput: move |e| config.set(e.value()) }
            button { onclick: submit, "Save" }
        }
    }
}

// ----------------------------------------------------------------- Harnesses

#[component]
fn HarnessRow(
    harness: Harness,
    on_edit: Callback<Harness>,
    on_delete: Callback<HarnessId>,
) -> Element {
    let harness_id = harness.id;
    rsx! {
        tr {
            td { "{harness.name}" }
            td { class: "mono", "{harness.command}" }
            td { class: "mono", "{harness.args.join(\" \")}" }
            td { "{harness.default_permission_policy}" }
            td { "{harness.enabled}" }
            td {
                button { onclick: move |_| on_edit.call(harness.clone()), "edit" }
                button { onclick: move |_| on_delete.call(harness_id), "delete" }
            }
        }
    }
}

#[component]
pub fn HarnessesPage() -> Element {
    let mut data: Data = use_context();
    let mut editing = use_signal(|| None::<HarnessId>);
    let mut name = use_signal(String::new);
    let mut command = use_signal(String::new);
    let mut args = use_signal(String::new);
    let mut env = use_signal(String::new);
    let mut policy = use_signal(|| "ask_human".to_string());
    let mut mat = use_signal(String::new);
    let mut enabled = use_signal(|| true);

    let on_edit = Callback::new(move |h: Harness| {
        editing.set(Some(h.id));
        name.set(h.name.clone());
        command.set(h.command.clone());
        args.set(h.args.join(" "));
        env.set(kv_string(&h.env));
        policy.set(h.default_permission_policy.to_string());
        mat.set(h.model_arg_template.join(" "));
        enabled.set(h.enabled);
    });
    let on_delete = Callback::new(move |id: HarnessId| {
        delete_by_id(data, format!("/harnesses/{id}"));
    });

    let submit = move |_| {
        let pol = policy
            .read()
            .parse::<PermissionPolicy>()
            .unwrap_or(PermissionPolicy::AskHuman);
        let (n, c, a, e, m, en) = (
            name.read().clone(),
            command.read().clone(),
            split_ws(&args.read()),
            parse_kv(&env.read()),
            split_ws(&mat.read()),
            enabled(),
        );
        let edit = editing();
        spawn(async move {
            let r = if let Some(id) = edit {
                api::put::<UpdateHarness, Harness>(
                    &format!("/harnesses/{id}"),
                    &UpdateHarness {
                        name: n,
                        command: c,
                        args: a,
                        env: e,
                        default_permission_policy: pol,
                        model_arg_template: m,
                        enabled: en,
                    },
                )
                .await
                .map(|_| ())
            } else {
                api::post::<CreateHarness, Harness>(
                    "/harnesses",
                    &CreateHarness {
                        name: n,
                        command: c,
                        args: a,
                        env: e,
                        default_permission_policy: Some(pol),
                        model_arg_template: m,
                        enabled: Some(en),
                    },
                )
                .await
                .map(|_| ())
            };
            match r {
                Err(e) => data.error.set(Some(e)),
                Ok(()) => {
                    editing.set(None);
                    data.refresh();
                }
            }
        });
    };

    rsx! {
        h2 { "Harnesses" }
        table {
            thead { tr { th { "Name" }, th { "Command" }, th { "Args" }, th { "Policy" }, th { "Enabled" }, th { "" } } }
            tbody {
                for h in data.harnesses.read().clone() {
                    HarnessRow { key: "{h.id}", harness: h, on_edit, on_delete }
                }
            }
        }
        h3 { if editing().is_some() { "Edit harness" } else { "New harness" } }
        div { class: "form",
            label { "Name" }
            input { value: "{name}", oninput: move |e| name.set(e.value()) }
            label { "Command" }
            input { value: "{command}", oninput: move |e| command.set(e.value()) }
            label { "Args (space separated)" }
            input { value: "{args}", oninput: move |e| args.set(e.value()) }
            label { "Env (KEY=value per line)" }
            textarea { value: "{env}", oninput: move |e| env.set(e.value()) }
            label { "Default permission policy" }
            select { onchange: move |e| policy.set(e.value()),
                for p in POLICIES {
                    option { key: "{p}", value: "{p}", selected: policy() == *p, "{p}" }
                }
            }
            label { "Model arg template (e.g. --model {{model}})" }
            input { value: "{mat}", oninput: move |e| mat.set(e.value()) }
            label {
                input {
                    r#type: "checkbox",
                    checked: enabled(),
                    onchange: move |e| enabled.set(e.checked()),
                }
                " Enabled"
            }
            button { onclick: submit, "Save" }
        }
    }
}

// -------------------------------------------------------------------- Agents

#[component]
fn AgentRow(
    agent: Agent,
    profile_name: String,
    on_edit: Callback<Agent>,
    on_delete: Callback<AgentId>,
) -> Element {
    let agent_id = agent.id;
    rsx! {
        tr {
            td { "{agent.name}" }
            td { "{agent.role}" }
            td { "{agent.permission_policy}" }
            td { "{profile_name}" }
            td { "{agent.status}" }
            td {
                button { onclick: move |_| on_edit.call(agent.clone()), "edit" }
                button { onclick: move |_| on_delete.call(agent_id), "delete" }
            }
        }
    }
}

#[component]
pub fn AgentsPage() -> Element {
    let mut data: Data = use_context();
    let mut editing = use_signal(|| None::<AgentId>);
    let mut name = use_signal(String::new);
    let mut role = use_signal(|| "project".to_string());
    let mut policy = use_signal(|| "ask_human".to_string());
    let mut agent_status = use_signal(|| "idle".to_string());
    let mut instructions = use_signal(String::new);
    let mut default_profile = use_signal(String::new);
    let mut org = use_signal(String::new);
    let mut project = use_signal(String::new);
    let mut repo = use_signal(String::new);
    // activity string -> profile id string ("" = use default)
    let mut overrides = use_signal(BTreeMap::<String, String>::new);

    let on_edit = Callback::new(move |a: Agent| {
        editing.set(Some(a.id));
        name.set(a.name.clone());
        org.set(a.organization_id.to_string());
        role.set(a.role.to_string());
        policy.set(a.permission_policy.to_string());
        agent_status.set(a.status.to_string());
        instructions.set(a.instructions.clone());
        default_profile.set(a.profiles.default.to_string());
        project.set(a.project_id.map(|p| p.to_string()).unwrap_or_default());
        repo.set(a.repository_id.map(|r| r.to_string()).unwrap_or_default());
        let mut ov = BTreeMap::new();
        for (act, pid) in &a.profiles.overrides {
            ov.insert(act.to_string(), pid.to_string());
        }
        overrides.set(ov);
    });
    let on_delete = Callback::new(move |id: AgentId| {
        delete_by_id(data, format!("/agents/{id}"));
    });

    let submit = move |_| {
        let Ok(dp) = default_profile.read().parse::<ModelProfileId>() else {
            data.error.set(Some("pick a default profile".into()));
            return;
        };
        let mut ov = BTreeMap::new();
        for (act, pid) in overrides.read().iter() {
            if pid.is_empty() {
                continue;
            }
            if let (Ok(a), Ok(p)) = (act.parse::<Activity>(), pid.parse::<ModelProfileId>()) {
                ov.insert(a, p);
            }
        }
        let role_v = role
            .read()
            .parse::<AgentRole>()
            .unwrap_or(AgentRole::Project);
        let pol = policy
            .read()
            .parse::<PermissionPolicy>()
            .unwrap_or(PermissionPolicy::AskHuman);
        let st = agent_status
            .read()
            .parse::<AgentStatus>()
            .unwrap_or(AgentStatus::Idle);
        let oid = org.read().parse::<OrganizationId>().ok();
        let proj = project.read().parse::<ProjectId>().ok();
        let rep = repo.read().parse::<RepositoryId>().ok();
        let (n, ins) = (name.read().clone(), instructions.read().clone());
        let edit = editing();
        spawn(async move {
            let r = if let Some(id) = edit {
                let Some(oid) = oid else {
                    data.error.set(Some("pick an organization".into()));
                    return;
                };
                api::put::<UpdateAgent, Agent>(
                    &format!("/agents/{id}"),
                    &UpdateAgent {
                        name: n,
                        role: role_v,
                        organization_id: oid,
                        default_profile: dp,
                        profile_overrides: ov,
                        project_id: proj,
                        repository_id: rep,
                        instructions: ins,
                        permission_policy: pol,
                        status: st,
                    },
                )
                .await
                .map(|_| ())
            } else {
                api::post::<CreateAgent, Agent>(
                    "/agents",
                    &CreateAgent {
                        name: n,
                        role: role_v,
                        organization_id: oid,
                        default_profile: dp,
                        profile_overrides: ov,
                        project_id: proj,
                        repository_id: rep,
                        instructions: ins,
                        permission_policy: Some(pol),
                        status: Some(st),
                    },
                )
                .await
                .map(|_| ())
            };
            match r {
                Err(e) => data.error.set(Some(e)),
                Ok(()) => {
                    editing.set(None);
                    data.refresh();
                }
            }
        });
    };

    let override_map = overrides.read().clone();

    rsx! {
        h2 { "Agents" }
        table {
            thead { tr { th { "Name" }, th { "Role" }, th { "Policy" }, th { "Default profile" }, th { "Status" }, th { "" } } }
            tbody {
                for a in data.agents.read().clone() {
                    AgentRow {
                        key: "{a.id}",
                        agent: a.clone(),
                        profile_name: data.profile_name(a.profiles.default),
                        on_edit,
                        on_delete,
                    }
                }
            }
        }
        h3 { if editing().is_some() { "Edit agent" } else { "New agent" } }
        div { class: "form",
            label { "Name" }
            input { value: "{name}", oninput: move |e| name.set(e.value()) }
            label { "Role" }
            select { onchange: move |e| role.set(e.value()),
                for r in ROLES {
                    option { key: "{r}", value: "{r}", selected: role() == *r, "{r}" }
                }
            }
            label { "Permission policy" }
            select { onchange: move |e| policy.set(e.value()),
                for p in POLICIES {
                    option { key: "{p}", value: "{p}", selected: policy() == *p, "{p}" }
                }
            }
            label { "Status" }
            select { onchange: move |e| agent_status.set(e.value()),
                for s in AGENT_STATUSES {
                    option { key: "{s}", value: "{s}", selected: agent_status() == *s, "{s}" }
                }
            }
            label { "Organization" }
            select { onchange: move |e| org.set(e.value()),
                option { value: "", "—" }
                for o in data.orgs.read().clone() {
                    option { key: "{o.id}", value: "{o.id}", selected: org() == o.id.to_string(), "{o.name}" }
                }
            }
            label { "Project (optional)" }
            select { onchange: move |e| project.set(e.value()),
                option { value: "", "—" }
                for p in data.projects.read().clone() {
                    option { key: "{p.id}", value: "{p.id}", selected: project() == p.id.to_string(), "{p.slug}" }
                }
            }
            label { "Repository (optional)" }
            select { onchange: move |e| repo.set(e.value()),
                option { value: "", "—" }
                for r in data.repos.read().clone() {
                    option { key: "{r.id}", value: "{r.id}", selected: repo() == r.id.to_string(), "{r.owner}/{r.name}" }
                }
            }
            label { "Default profile" }
            select { onchange: move |e| default_profile.set(e.value()),
                option { value: "", "—" }
                for p in data.profiles.read().clone() {
                    option { key: "{p.id}", value: "{p.id}", selected: default_profile() == p.id.to_string(), "{p.name}" }
                }
            }
            label { "Instructions (system prompt)" }
            textarea { rows: "4", value: "{instructions}", oninput: move |e| instructions.set(e.value()) }
            h4 { "Per-activity overrides" }
            for act in ACTIVITIES {
                div { class: "row", key: "{act}",
                    span { class: "act", "{act}" }
                    select {
                        onchange: move |e| {
                            let a = act.to_string();
                            let v = e.value();
                            overrides.write().insert(a, v);
                        },
                        option { value: "", "(default)" }
                        for p in data.profiles.read().clone() {
                            option {
                                key: "{p.id}",
                                value: "{p.id}",
                                selected: override_map.get(*act).map(|s| s.as_str()) == Some(p.id.to_string().as_str()),
                                "{p.name}"
                            }
                        }
                    }
                }
            }
            button { onclick: submit, "Save" }
        }
    }
}
