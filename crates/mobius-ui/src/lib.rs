//! Mobius web UI — a thin Dioxus SPA over the REST/SSE API.

pub mod api;
mod chat;
mod lists;
mod settings;
mod sse;

use dioxus::prelude::*;
// `mobius_core::Signal` collides with `dioxus::prelude::Signal` under globs.
use dioxus::prelude::Signal;
use mobius_core::*;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Page {
    Chat,
    Dashboard,
    Tasks,
    Runs,
    Repositories,
    Projects,
    Agents,
    Profiles,
    Harnesses,
}

/// Shared entity caches; refreshed on `EntityChanged` events.
#[derive(Clone, Copy)]
pub struct Data {
    pub orgs: Signal<Vec<Organization>>,
    pub repos: Signal<Vec<Repository>>,
    pub projects: Signal<Vec<Project>>,
    pub agents: Signal<Vec<Agent>>,
    pub profiles: Signal<Vec<ModelProfile>>,
    pub harnesses: Signal<Vec<Harness>>,
    pub tasks: Signal<Vec<Task>>,
    pub runs: Signal<Vec<Run>>,
    pub error: Signal<Option<String>>,
}

impl Data {
    pub fn refresh(self) {
        spawn(async move {
            let mut d = self;
            if let Ok(v) = api::get::<Vec<Organization>>("/organizations").await {
                d.orgs.set(v);
            }
            if let Ok(v) = api::get::<Vec<Repository>>("/repositories").await {
                d.repos.set(v);
            }
            if let Ok(v) = api::get::<Vec<Project>>("/projects").await {
                d.projects.set(v);
            }
            if let Ok(v) = api::get::<Vec<Agent>>("/agents").await {
                d.agents.set(v);
            }
            if let Ok(v) = api::get::<Vec<ModelProfile>>("/model_profiles").await {
                d.profiles.set(v);
            }
            if let Ok(v) = api::get::<Vec<Harness>>("/harnesses").await {
                d.harnesses.set(v);
            }
            if let Ok(v) = api::get::<Vec<Task>>("/tasks").await {
                d.tasks.set(v);
            }
            if let Ok(v) = api::get::<Vec<Run>>("/runs").await {
                d.runs.set(v);
            }
        });
    }

    /// Refetch just the list `kind` refers to (EntityChanged events arrive at
    /// chat-stream frequency — refreshing everything is a GET storm).
    pub fn refresh_kind(self, kind: EntityKind) {
        spawn(async move {
            let mut d = self;
            match kind {
                EntityKind::Organization => {
                    if let Ok(v) = api::get::<Vec<Organization>>("/organizations").await {
                        d.orgs.set(v);
                    }
                }
                EntityKind::Repository => {
                    if let Ok(v) = api::get::<Vec<Repository>>("/repositories").await {
                        d.repos.set(v);
                    }
                }
                EntityKind::Project => {
                    if let Ok(v) = api::get::<Vec<Project>>("/projects").await {
                        d.projects.set(v);
                    }
                }
                EntityKind::Agent => {
                    if let Ok(v) = api::get::<Vec<Agent>>("/agents").await {
                        d.agents.set(v);
                    }
                }
                EntityKind::ModelProfile => {
                    if let Ok(v) = api::get::<Vec<ModelProfile>>("/model_profiles").await {
                        d.profiles.set(v);
                    }
                }
                EntityKind::Harness => {
                    if let Ok(v) = api::get::<Vec<Harness>>("/harnesses").await {
                        d.harnesses.set(v);
                    }
                }
                EntityKind::Task => {
                    if let Ok(v) = api::get::<Vec<Task>>("/tasks").await {
                        d.tasks.set(v);
                    }
                }
                EntityKind::Run => {
                    if let Ok(v) = api::get::<Vec<Run>>("/runs").await {
                        d.runs.set(v);
                    }
                }
                _ => {}
            }
        });
    }

    pub fn profile_name(self, id: ModelProfileId) -> String {
        self.profiles
            .read()
            .iter()
            .find(|p| p.id == id)
            .map(|p| p.name.clone())
            .unwrap_or_else(|| id.to_string())
    }

    pub fn harness_name(self, id: HarnessId) -> String {
        self.harnesses
            .read()
            .iter()
            .find(|h| h.id == id)
            .map(|h| h.name.clone())
            .unwrap_or_else(|| id.to_string())
    }
}

const MAIN_CSS: Asset = asset!("/assets/main.css");

#[component]
pub fn App() -> Element {
    let mut page = use_signal(|| Page::Chat);
    let mut data = Data {
        orgs: use_signal(Vec::new),
        repos: use_signal(Vec::new),
        projects: use_signal(Vec::new),
        agents: use_signal(Vec::new),
        profiles: use_signal(Vec::new),
        harnesses: use_signal(Vec::new),
        tasks: use_signal(Vec::new),
        runs: use_signal(Vec::new),
        error: use_signal(|| None::<String>),
    };
    use_context_provider(|| data);
    let events = use_signal(Vec::<EventEnvelope>::new);

    // Refresh only the list an EntityChanged event touches; Message,
    // Conversation, PermissionRequest, Signal and MemoryEntry events are
    // handled by the chat page or not rendered in a list at all.
    let on_event = Callback::new(move |env: EventEnvelope| {
        if let DomainEvent::EntityChanged { kind, .. } = env.event {
            data.refresh_kind(kind);
        }
    });
    sse::use_event_stream(events, on_event);
    use_hook(|| data.refresh());

    let nav = [
        (Page::Chat, "Chat"),
        (Page::Dashboard, "Dashboard"),
        (Page::Tasks, "Tasks"),
        (Page::Runs, "Runs"),
        (Page::Repositories, "Repositories"),
        (Page::Projects, "Projects"),
        (Page::Agents, "Agents"),
        (Page::Profiles, "Profiles"),
        (Page::Harnesses, "Harnesses"),
    ];

    rsx! {
        document::Stylesheet { href: MAIN_CSS }
        div { class: "app",
            nav { class: "sidebar",
                h1 { "Mobius" }
                for (p, label) in nav {
                    button {
                        key: "{label}",
                        class: if page() == p { "nav active" } else { "nav" },
                        onclick: move |_| page.set(p),
                        "{label}"
                    }
                }
            }
            main { class: "content",
                if let Some(e) = data.error.read().clone() {
                    div { class: "error-banner",
                        "{e}"
                        button { onclick: move |_| data.error.set(None), "×" }
                    }
                }
                match page() {
                    Page::Chat => rsx! { chat::ChatPage {} },
                    Page::Dashboard => rsx! { Dashboard {} },
                    Page::Tasks => rsx! { lists::TasksPage {} },
                    Page::Runs => rsx! { lists::RunsPage {} },
                    Page::Repositories => rsx! { settings::RepositoriesPage {} },
                    Page::Projects => rsx! { settings::ProjectsPage {} },
                    Page::Agents => rsx! { settings::AgentsPage {} },
                    Page::Profiles => rsx! { settings::ProfilesPage {} },
                    Page::Harnesses => rsx! { settings::HarnessesPage {} },
                }
            }
        }
    }
}

#[component]
fn Dashboard() -> Element {
    let data: Data = use_context();
    rsx! {
        h2 { "Dashboard" }
        div { class: "cards",
            div { class: "card", b { "{data.orgs.read().len()}" }, span { "organizations" } }
            div { class: "card", b { "{data.repos.read().len()}" }, span { "repositories" } }
            div { class: "card", b { "{data.projects.read().len()}" }, span { "projects" } }
            div { class: "card", b { "{data.agents.read().len()}" }, span { "agents" } }
            div { class: "card", b { "{data.tasks.read().len()}" }, span { "tasks" } }
            div { class: "card", b { "{data.runs.read().len()}" }, span { "runs" } }
        }
        h3 { "Organizations" }
        ul {
            for org in data.orgs.read().iter() {
                li { key: "{org.id}", b { "{org.name}" }, " — {org.slug}" }
            }
        }
    }
}
