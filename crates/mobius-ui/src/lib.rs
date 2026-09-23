//! Mobius web UI — a thin Dioxus SPA over the REST/SSE API.

pub mod api;
mod chat;
mod lists;
mod panels;
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
    Work,
    Runs,
    Repositories,
    Projects,
    Agents,
    Profiles,
    Harnesses,
}

/// Navigation handle: lets any page open a specific conversation on the
/// Chat page (run/research transcripts).
#[derive(Clone, Copy)]
pub struct Nav {
    pub page: Signal<Page>,
    /// Set to a conversation id to make the Chat page open it.
    pub chat_target: Signal<Option<ConversationId>>,
}

impl Nav {
    pub fn open_conversation(self, id: ConversationId) {
        let mut this = self;
        this.chat_target.set(Some(id));
        this.page.set(Page::Chat);
    }
}

/// Shared entity caches; refreshed on `EntityChanged`/lifecycle events.
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
    pub conversations: Signal<Vec<Conversation>>,
    pub memory: Signal<Vec<MemoryEntry>>,
    pub research: Signal<Vec<Research>>,
    pub error: Signal<Option<String>>,
}

async fn fetch_into<T: serde::de::DeserializeOwned + 'static>(
    mut slot: Signal<Vec<T>>,
    path: &str,
) {
    if let Ok(v) = api::get::<Vec<T>>(path).await {
        slot.set(v);
    }
}

impl Data {
    pub fn refresh(self) {
        spawn(async move {
            fetch_into::<Organization>(self.orgs, "/organizations").await;
            fetch_into::<Repository>(self.repos, "/repositories").await;
            fetch_into::<Project>(self.projects, "/projects").await;
            fetch_into::<Agent>(self.agents, "/agents").await;
            fetch_into::<ModelProfile>(self.profiles, "/model_profiles").await;
            fetch_into::<Harness>(self.harnesses, "/harnesses").await;
            fetch_into::<Task>(self.tasks, "/tasks").await;
            fetch_into::<Run>(self.runs, "/runs").await;
            fetch_into::<Conversation>(self.conversations, "/conversations").await;
            fetch_into::<MemoryEntry>(self.memory, "/memory").await;
            fetch_into::<Research>(self.research, "/research").await;
        });
    }

    /// Refetch just the list `kind` refers to (EntityChanged events arrive at
    /// chat-stream frequency — refreshing everything is a GET storm).
    pub fn refresh_kind(self, kind: EntityKind) {
        spawn(async move {
            match kind {
                EntityKind::Organization => {
                    fetch_into::<Organization>(self.orgs, "/organizations").await;
                }
                EntityKind::Repository => {
                    fetch_into::<Repository>(self.repos, "/repositories").await;
                }
                EntityKind::Project => {
                    fetch_into::<Project>(self.projects, "/projects").await;
                }
                EntityKind::Agent => {
                    fetch_into::<Agent>(self.agents, "/agents").await;
                }
                EntityKind::ModelProfile => {
                    fetch_into::<ModelProfile>(self.profiles, "/model_profiles").await;
                }
                EntityKind::Harness => {
                    fetch_into::<Harness>(self.harnesses, "/harnesses").await;
                }
                EntityKind::Task => {
                    fetch_into::<Task>(self.tasks, "/tasks").await;
                }
                EntityKind::Run => {
                    fetch_into::<Run>(self.runs, "/runs").await;
                }
                EntityKind::Conversation => {
                    fetch_into::<Conversation>(self.conversations, "/conversations").await;
                }
                EntityKind::MemoryEntry => {
                    fetch_into::<MemoryEntry>(self.memory, "/memory").await;
                }
                EntityKind::Research => {
                    fetch_into::<Research>(self.research, "/research").await;
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
    let chat_target = use_signal(|| None::<ConversationId>);
    let mut data = Data {
        orgs: use_signal(Vec::new),
        repos: use_signal(Vec::new),
        projects: use_signal(Vec::new),
        agents: use_signal(Vec::new),
        profiles: use_signal(Vec::new),
        harnesses: use_signal(Vec::new),
        tasks: use_signal(Vec::new),
        runs: use_signal(Vec::new),
        conversations: use_signal(Vec::new),
        memory: use_signal(Vec::new),
        research: use_signal(Vec::new),
        error: use_signal(|| None::<String>),
    };
    use_context_provider(|| data);
    use_context_provider(|| Nav { page, chat_target });
    let events = use_signal(Vec::<EventEnvelope>::new);

    // Refresh only the list an event touches — stream-frequent events
    // (MessageDelta & friends) never trigger a refetch.
    let on_event = Callback::new(move |env: EventEnvelope| {
        match env.event {
            DomainEvent::EntityChanged { kind, .. } => data.refresh_kind(kind),
            DomainEvent::TaskCreated { .. } | DomainEvent::TaskStatusChanged { .. } => {
                data.refresh_kind(EntityKind::Task);
            }
            DomainEvent::RunStarted { .. }
            | DomainEvent::RunUpdated { .. }
            | DomainEvent::RunFinished { .. } => {
                // A finished run also transitions its task.
                data.refresh_kind(EntityKind::Run);
                data.refresh_kind(EntityKind::Task);
            }
            DomainEvent::ResearchStarted { .. } | DomainEvent::ResearchFinished { .. } => {
                data.refresh_kind(EntityKind::Research);
            }
            DomainEvent::MemoryWritten { .. } => {
                data.refresh_kind(EntityKind::MemoryEntry);
            }
            DomainEvent::ConversationCreated { .. }
            | DomainEvent::ConversationStatusChanged { .. } => {
                data.refresh_kind(EntityKind::Conversation);
            }
            _ => {}
        }
    });
    sse::use_event_stream(events, on_event);
    use_hook(|| data.refresh());

    let nav_items = [
        (Page::Chat, "Chat"),
        (Page::Dashboard, "Dashboard"),
        (Page::Work, "Work"),
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
                for (p, label) in nav_items {
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
                    Page::Work => rsx! { lists::WorkPage {} },
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
