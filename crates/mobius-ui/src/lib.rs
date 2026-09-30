mod markdown;

use std::cmp::Reverse;
use std::collections::HashMap;

use dioxus::prelude::*;
use markdown::Markdown;
use mobius_api::{
    agent_tree, chat_seen, chat_send, chat_stop, chat_view, devices, github_apps, github_manifest,
    inbox_dismiss, inbox_items, inbox_resume, live, login, logout, organizations, server_agents,
    task_list, transcript_lines, unread, workstreams,
};
use mobius_domain::{
    AgentNode, Author, ChatMessage, FeedRow, InboxItem, InboxKind, Live, PAUSED, TaskLine,
    TranscriptLine, Workstream,
};
use time::UtcOffset;
use time::macros::format_description;

const MAIN_CSS: Asset = asset!("/assets/main.css");

#[derive(Clone, PartialEq, Routable)]
#[rustfmt::skip]
pub enum Route {
    #[layout(Shell)]
        #[redirect("/", || Route::WorkstreamList {})]
        #[route("/workstreams")]
        WorkstreamList {},
        #[route("/workstreams/:owner/:repo/:number")]
        Chat { owner: String, repo: String, number: i64 },
        #[route("/workstreams/new")]
        NewWorkstream {},
        #[route("/server-agents")]
        ServerAgents {},
        #[route("/inbox")]
        Inbox {},
        #[route("/activity")]
        Activity {},
        #[route("/devices")]
        Devices {},
        #[route("/github")]
        GitHub {},
        #[route("/settings")]
        Settings {},
}

#[component]
pub fn App() -> Element {
    // The on-screen keyboard shrinks the layout viewport instead of scrolling the
    // page, so the chat input stays above the keyboard; the tab bar hides on focus.
    use_effect(|| {
        document::eval(
            r#"let meta = document.head.querySelector('meta[name="viewport"]');
if (!meta) {
    meta = document.createElement("meta");
    meta.name = "viewport";
    document.head.append(meta);
}
meta.content = "width=device-width, initial-scale=1, interactive-widget=resizes-content";"#,
        );
    });
    rsx! {
        document::Link { rel: "icon", r#type: "image/svg+xml", href: "/icon.svg" }
        document::Link { rel: "apple-touch-icon", href: "/apple-touch-icon.png" }
        document::Link { rel: "manifest", href: "/manifest.webmanifest" }
        document::Meta { name: "theme-color", content: "#2d5f8b" }
        document::Meta { name: "mobile-web-app-capable", content: "yes" }
        document::Meta { name: "apple-mobile-web-app-capable", content: "yes" }
        document::Meta { name: "apple-mobile-web-app-title", content: "Mobius" }
        document::Meta { name: "apple-mobile-web-app-status-bar-style", content: "default" }
        // The service worker makes the app installable. It keeps no cache.
        document::Script { "navigator.serviceWorker?.register('/sw.js');" }
        document::Stylesheet { href: MAIN_CSS }
        Router::<Route> {}
    }
}

/// The axum router of the web UI. The app shell, the service worker, and the
/// web app manifest get `Cache-Control: no-cache`, so the browser always asks
/// the server for them.
#[cfg(feature = "server")]
pub fn router() -> dioxus::server::axum::Router {
    use dioxus::server::axum::extract::Request;
    use dioxus::server::axum::http::HeaderValue;
    use dioxus::server::axum::http::header::{CACHE_CONTROL, CONTENT_TYPE};
    use dioxus::server::axum::middleware::{Next, from_fn};
    use dioxus::server::axum::response::Response;

    async fn no_cache(request: Request, next: Next) -> Response {
        let pwa_file = matches!(
            request.uri().path(),
            "/" | "/sw.js" | "/manifest.webmanifest"
        );
        let mut response = next.run(request).await;
        // Every other path of the app renders the same shell.
        let shell = response
            .headers()
            .get(CONTENT_TYPE)
            .is_some_and(|value| value.as_bytes().starts_with(b"text/html"));
        if pwa_file || shell {
            response
                .headers_mut()
                .insert(CACHE_CONTROL, HeaderValue::from_static("no-cache"));
        }
        response
    }

    dioxus::server::router(App).layer(from_fn(no_cache))
}

#[derive(Clone, Copy)]
struct LoginShown(Signal<bool>);

#[derive(Clone, Copy)]
struct AppSlugs(Resource<ServerFnResult<Vec<String>>>);

#[derive(Clone, Copy)]
struct Organizations(Resource<ServerFnResult<Vec<String>>>);

// The organization of the repositories that the pages show.
#[derive(Clone, Copy)]
struct Organization(Signal<String>);

// The offset of the time zone of the browser. The server gives each time in UTC.
#[derive(Clone, Copy)]
struct LocalOffset(Signal<UtcOffset>);

#[derive(Clone, Copy)]
struct Workstreams(Resource<ServerFnResult<Vec<Workstream>>>);

// The organization, the repository, and the Workstream. The Triager chat has the empty repository.
type ChatKey = (String, String, i64);

#[derive(Clone, PartialEq)]
struct LeadState {
    writing: bool,
    error: Option<String>,
}

#[derive(Clone, Copy)]
struct LiveState {
    feed: Signal<Vec<FeedRow>>,
    messages: Signal<Vec<ChatMessage>>,
    leads: Signal<HashMap<ChatKey, LeadState>>,
    unread: Signal<HashMap<ChatKey, i64>>,
    agents: Signal<HashMap<i64, AgentNode>>,
    inbox: Signal<HashMap<i64, InboxItem>>,
    // The Workstream that the Triager chat created last.
    created: Signal<Option<(String, i64)>>,
}

fn select_organization(mut organization: Signal<String>, name: String) {
    if *organization.peek() == name {
        return;
    }
    let value = serde_json::to_string(&name).unwrap_or_default();
    document::eval(&format!(
        "try {{ localStorage.setItem('organization', {value}); }} catch {{}}"
    ));
    organization.set(name);
}

fn switchable() -> bool {
    let Organizations(list) = use_context();
    matches!(&*list.read(), Some(Ok(list)) if list.len() > 1)
}

fn unauthorized(error: &ServerFnError) -> bool {
    matches!(error, ServerFnError::ServerError { code: 401, .. })
}

fn error_text(error: &ServerFnError) -> String {
    match error {
        ServerFnError::ServerError { message, .. } => message.clone(),
        _ => error.to_string(),
    }
}

#[component]
fn Shell() -> Element {
    let LoginShown(login_shown) = use_context_provider(|| LoginShown(Signal::new(false)));
    if login_shown() {
        return rsx! { Login {} };
    }
    rsx! { Frame {} }
}

fn upsert(messages: &mut Vec<ChatMessage>, message: ChatMessage) {
    match messages.iter_mut().find(|known| known.id == message.id) {
        Some(known) => *known = message,
        None => messages.push(message),
    }
}

async fn follow_live(
    mut state: LiveState,
    mut workstream_list: Resource<ServerFnResult<Vec<Workstream>>>,
    mut login_shown: Signal<bool>,
) {
    let mut after = None;
    loop {
        if let Ok(counts) = unread().await {
            state.unread.set(
                counts
                    .into_iter()
                    .map(|unread| {
                        (
                            (unread.organization, unread.repository, unread.workstream),
                            unread.count,
                        )
                    })
                    .collect(),
            );
        }
        if let Ok(items) = inbox_items().await {
            state
                .inbox
                .set(items.into_iter().map(|item| (item.id, item)).collect());
        }
        match live(after).await {
            Ok(mut events) => {
                while let Some(Ok(event)) = events.recv().await {
                    match event {
                        Live::Feed(row) => {
                            after = Some(row.id);
                            let known = match &*workstream_list.peek() {
                                Some(Ok(list)) => list.iter().any(|workstream| {
                                    workstream.repository == row.repository
                                        && workstream.number == row.workstream
                                }),
                                _ => false,
                            };
                            if !known && !workstream_list.pending() {
                                workstream_list.restart();
                            }
                            state.feed.write().push(row);
                        }
                        Live::Message(message) => upsert(&mut state.messages.write(), message),
                        Live::Lead {
                            organization,
                            repository,
                            workstream,
                            writing,
                            error,
                        } => {
                            state.leads.write().insert(
                                (organization, repository, workstream),
                                LeadState { writing, error },
                            );
                        }
                        Live::Unread(unread) => {
                            state.unread.write().insert(
                                (unread.organization, unread.repository, unread.workstream),
                                unread.count,
                            );
                        }
                        Live::Agent(node) => {
                            state.agents.write().insert(node.session.id, node);
                        }
                        Live::Inbox(item) if item.dismissed_at.is_some() => {
                            state.inbox.write().remove(&item.id);
                        }
                        Live::Inbox(item) => {
                            state.inbox.write().insert(item.id, item);
                        }
                        Live::Workstreams => workstream_list.restart(),
                        Live::WorkstreamCreated { repository, number } => {
                            workstream_list.restart();
                            state.created.set(Some((repository, number)));
                        }
                    }
                }
            }
            Err(error) if unauthorized(&error) => {
                login_shown.set(true);
                return;
            }
            Err(_) => {}
        }
        // The web build has no timer crate, so a JavaScript timer makes the delay.
        let _ = document::eval("await new Promise(resolve => setTimeout(resolve, 1000));").await;
    }
}

#[component]
fn Frame() -> Element {
    let LoginShown(mut login_shown) = use_context();
    let app_slugs = use_resource(github_apps);
    use_context_provider(|| AppSlugs(app_slugs));
    let workstream_list = use_resource(workstreams);
    use_context_provider(|| Workstreams(workstream_list));
    // The poll finds a new organization when it finds its repositories, and the Workstream list then loads again.
    let organization_list = use_resource(move || async move {
        workstream_list.read();
        organizations().await
    });
    use_context_provider(|| Organizations(organization_list));
    let Organization(organization) =
        use_context_provider(|| Organization(Signal::new(String::new())));
    let LocalOffset(mut local_offset) =
        use_context_provider(|| LocalOffset(Signal::new(UtcOffset::UTC)));
    use_hook(move || {
        spawn(async move {
            // `getTimezoneOffset` gives the minutes from the local time to UTC, so the sign is the reverse of `UtcOffset`.
            let minutes: i32 = document::eval("return new Date().getTimezoneOffset();")
                .join()
                .await
                .unwrap_or_default();
            if let Ok(offset) = UtcOffset::from_whole_seconds(-minutes * 60) {
                local_offset.set(offset);
            }
        })
    });
    let state = use_context_provider(|| LiveState {
        feed: Signal::new(Vec::new()),
        messages: Signal::new(Vec::new()),
        leads: Signal::new(HashMap::new()),
        unread: Signal::new(HashMap::new()),
        agents: Signal::new(HashMap::new()),
        inbox: Signal::new(HashMap::new()),
        created: Signal::new(None),
    });
    use_effect(move || {
        if let Some(Err(error)) = &*app_slugs.read()
            && unauthorized(error)
        {
            login_shown.set(true);
        }
    });
    use_effect(move || {
        let Some(Ok(list)) = organization_list() else {
            return;
        };
        if list.contains(&organization.peek()) {
            return;
        }
        spawn(async move {
            let saved: String = document::eval(
                "try { return localStorage.getItem('organization') ?? ''; } catch { return ''; }",
            )
            .join()
            .await
            .unwrap_or_default();
            // A Chat page can select the organization of its route during the read.
            if list.contains(&organization.peek()) {
                return;
            }
            let name = if list.contains(&saved) {
                saved
            } else {
                list.first().cloned().unwrap_or_default()
            };
            select_organization(organization, name);
        });
    });
    use_effect(move || {
        spawn(follow_live(state, workstream_list, login_shown));
    });
    let inbox_count = state
        .inbox
        .read()
        .values()
        .filter(|item| item.organization == organization())
        .count();
    let switch = switchable();
    match &*app_slugs.read() {
        Some(Ok(slugs)) if !slugs.is_empty() => rsx! {
            div { class: "shell",
                nav { class: "rail",
                    if switch {
                        OrganizationSwitch {}
                    } else {
                        div { class: "brand", "Mobius" }
                    }
                    Link { class: "entry", active_class: "sel", to: Route::Inbox {},
                        span { class: "grow", "Inbox" }
                        if inbox_count > 0 {
                            span { class: "count", "{inbox_count}" }
                        }
                    }
                    Link { class: "navbtn", active_class: "sel", to: Route::Activity {}, "Activity" }
                    Link { class: "navbtn", active_class: "sel", to: Route::ServerAgents {}, "Server agents" }
                    div { class: "label section", "Workstreams" }
                    WorkstreamEntries {}
                    Link { class: "navbtn", active_class: "sel", to: Route::NewWorkstream {}, "+ New Workstream" }
                    div { class: "grow" }
                    Link { class: "navbtn", active_class: "sel", to: Route::GitHub {}, "GitHub" }
                    Link { class: "navbtn", active_class: "sel", to: Route::Devices {}, "Devices" }
                }
                main { class: "center", Outlet::<Route> {} }
                nav { class: "tabs",
                    Link { active_class: "on", to: Route::WorkstreamList {},
                        span { class: "glyph", "◎" }
                        "Workstreams"
                    }
                    Link { active_class: "on", to: Route::Inbox {},
                        span { class: "glyph", "▤" }
                        span {
                            "Inbox"
                            if inbox_count > 0 {
                                " "
                                span { class: "count", "{inbox_count}" }
                            }
                        }
                    }
                    Link { active_class: "on", to: Route::Activity {},
                        span { class: "glyph", "≡" }
                        "Activity"
                    }
                    Link { active_class: "on", to: Route::Settings {},
                        // U+FE0E selects the text form of the gear, not the emoji.
                        span { class: "glyph", "\u{2699}\u{fe0e}" }
                        "Settings"
                    }
                }
            }
        },
        Some(Ok(_)) => rsx! { main { class: "center", GitHub {} } },
        Some(Err(error)) => rsx! { p { class: "error note", {error_text(error)} } },
        None => rsx! {},
    }
}

#[component]
fn OrganizationSwitch() -> Element {
    let Organizations(organization_list) = use_context();
    let Organization(organization) = use_context();
    let state: LiveState = use_context();
    let mut open = use_signal(|| false);
    let list = match &*organization_list.read() {
        Some(Ok(list)) => list.clone(),
        _ => Vec::new(),
    };
    let mut work: HashMap<String, i64> = HashMap::new();
    for item in state.inbox.read().values() {
        *work.entry(item.organization.clone()).or_default() += 1;
    }
    for ((organization, _, _), count) in state.unread.read().iter() {
        *work.entry(organization.clone()).or_default() += count;
    }
    let selected = organization();
    let elsewhere = work
        .iter()
        .any(|(name, count)| *name != selected && *count > 0);
    rsx! {
        div { class: "orgs",
            button { class: "switch", onclick: move |_| open.toggle(),
                span { class: "ellip", "{selected}" }
                if elsewhere {
                    span { class: "dot live" }
                }
                span { class: "muted", "▾" }
            }
            if open() {
                div { class: "backdrop", onclick: move |_| open.set(false) }
                div { class: "orgmenu",
                    div { class: "label", "Organizations" }
                    for name in list {
                        button {
                            key: "{name}",
                            class: if name == selected { "entry sel" } else { "entry" },
                            onclick: {
                                let name = name.clone();
                                move |_| {
                                    select_organization(organization, name.clone());
                                    open.set(false);
                                }
                            },
                            span { class: "grow", "{name}" }
                            if name != selected && work.get(&name).is_some_and(|count| *count > 0) {
                                span { class: "count", "{work[&name]}" }
                            }
                        }
                    }
                }
            }
        }
    }
}

#[component]
fn WorkstreamEntries() -> Element {
    let Workstreams(workstream_list) = use_context();
    let Organization(organization) = use_context();
    match &*workstream_list.read() {
        Some(Ok(list)) => rsx! {
            for workstream in list.iter().filter(|workstream| mobius_domain::organization(&workstream.repository) == organization()).cloned() {
                WorkstreamEntry { key: "{workstream.repository}#{workstream.number}", workstream }
            }
        },
        Some(Err(error)) => rsx! { div { class: "error entry", {error_text(error)} } },
        None => rsx! {},
    }
}

#[component]
fn WorkstreamEntry(workstream: Workstream) -> Element {
    let state: LiveState = use_context();
    let (owner, repo) = workstream.repository.split_once('/').unwrap_or_default();
    let unread = state
        .unread
        .read()
        .get(&(
            owner.to_string(),
            workstream.repository.clone(),
            workstream.number,
        ))
        .copied()
        .unwrap_or(0);
    rsx! {
        Link {
            class: "entry",
            active_class: "sel",
            to: Route::Chat { owner: owner.to_string(), repo: repo.to_string(), number: workstream.number },
            span { class: "grow", "{workstream.title}" }
            span { class: "muted small", "#{workstream.number}" }
            if unread > 0 {
                span { class: "count", "{unread}" }
            }
        }
    }
}

#[component]
fn WorkstreamList() -> Element {
    rsx! {
        div { class: "head",
            if switchable() {
                div { class: "phone grow", OrganizationSwitch {} }
                h2 { class: "desktop grow", "Workstreams" }
            } else {
                h2 { class: "grow", "Workstreams" }
            }
            Link { class: "btn primary", to: Route::NewWorkstream {}, "+ New" }
        }
        div { class: "list",
            WorkstreamEntries {}
            Link { class: "entry", to: Route::ServerAgents {},
                span { class: "grow", "Server agents" }
            }
        }
    }
}

#[component]
fn Settings() -> Element {
    rsx! {
        div { class: "head", h2 { "Settings" } }
        div { class: "list",
            Link { class: "entry", to: Route::GitHub {},
                span { class: "grow", "GitHub" }
            }
            Link { class: "entry", to: Route::Devices {},
                span { class: "grow", "Devices" }
            }
        }
    }
}

#[component]
fn NewWorkstream() -> Element {
    let state: LiveState = use_context();
    let Organizations(organization_list) = use_context();
    let Organization(organization) = use_context();
    let mut created = state.created;
    let navigator = use_navigator();
    // An earlier Workstream of the Triager chat does not open a chat.
    use_hook(|| created.set(None));
    use_effect(move || {
        if let Some((repository, number)) = created() {
            created.set(None);
            let (owner, repo) = repository.split_once('/').unwrap_or_default();
            navigator.push(Route::Chat {
                owner: owner.to_string(),
                repo: repo.to_string(),
                number,
            });
        }
    });
    if !matches!(&*organization_list.read(), Some(Ok(list)) if list.contains(&organization())) {
        return rsx! {
            div { class: "head", h2 { "New Workstream" } }
            p { class: "muted note", "Mobius reads the repositories from GitHub. The Triager chat opens after this step." }
        };
    }
    rsx! {
        div { class: "page",
            Conversation {
                organization: organization(),
                repository: String::new(),
                number: 0,
                agent: "Triager",
                brief: None,
                head: rsx! { h2 { class: "ellip", "New Workstream" } },
                tail: rsx! {},
            }
        }
    }
}

#[component]
fn ServerAgents() -> Element {
    let state: LiveState = use_context();
    let Organization(organization) = use_context();
    let tree = use_resource(server_agents);
    let mut selected = use_signal(|| None::<i64>);
    let mut nodes: HashMap<i64, AgentNode> = match &*tree.read() {
        Some(Ok(list)) => list
            .iter()
            .map(|node| (node.session.id, node.clone()))
            .collect(),
        _ => HashMap::new(),
    };
    for node in state.agents.read().values() {
        if node.role != "Triager" {
            continue;
        }
        // A session never starts again, so an ended node is newer than a live node.
        let known = nodes.get(&node.session.id);
        if known.is_none_or(|known| known.session.ended_at.is_none()) {
            nodes.insert(node.session.id, node.clone());
        }
    }
    let mut nodes: Vec<AgentNode> = nodes
        .into_values()
        .filter(|node| node.session.organization == organization())
        .collect();
    nodes.sort_by_key(|node| Reverse(node.session.id));
    if let Some(node) = selected().and_then(|id| nodes.iter().find(|node| node.session.id == id)) {
        return rsx! {
            div { class: "head",
                button { class: "back", onclick: move |_| selected.set(None), "‹ Server agents" }
                h2 { class: "ellip grow", "{node.role} {node.title}" }
            }
            Transcript { session: node.session.id }
        };
    }
    rsx! {
        div { class: "head", h2 { "Server agents" } }
        div { class: "label section", "Triager" }
        div { class: "list",
            if let Some(Err(error)) = &*tree.read() {
                div { class: "error note", {error_text(error)} }
            }
            if nodes.is_empty() {
                div { class: "muted small note", "No Triager sessions." }
            }
            for node in nodes {
                AgentEntry {
                    key: "{node.session.id}",
                    node: node.clone(),
                    onclick: move |_| selected.set(Some(node.session.id)),
                }
            }
        }
    }
}

#[component]
fn Inbox() -> Element {
    let LocalOffset(local_offset) = use_context();
    let Workstreams(workstream_list) = use_context();
    let LiveState { inbox, .. } = use_context();
    let Organization(organization) = use_context();
    let mut error = use_signal(String::new);
    let mut items: Vec<InboxItem> = inbox
        .read()
        .values()
        .filter(|item| item.organization == organization())
        .cloned()
        .collect();
    items.sort_by_key(|item| Reverse(item.id));
    let titles: HashMap<(String, i64), String> = match &*workstream_list.read() {
        Some(Ok(list)) => list
            .iter()
            .map(|workstream| {
                (
                    (workstream.repository.clone(), workstream.number),
                    workstream.title.clone(),
                )
            })
            .collect(),
        _ => HashMap::new(),
    };
    rsx! {
        div { class: "head", h2 { "Inbox" } }
        div { class: "error note", {error} }
        div { class: "list",
            if items.is_empty() {
                div { class: "muted small note", "Nothing waits for you." }
            }
            for item in items {
                div { key: "{item.id}", class: "item",
                    span { class: "chip", {item.kind.name()} }
                    div { class: "grow",
                        div { "{item.text}" }
                        div { class: "muted small",
                            {titles.get(&(item.repository.clone(), item.workstream)).cloned().unwrap_or_else(|| format!("#{}", item.workstream))}
                            " · #{item.issue} · "
                            {item.time.to_offset(local_offset()).format(format_description!("[month]-[day] [hour]:[minute]")).unwrap_or_default()}
                        }
                    }
                    if item.kind == InboxKind::UsageLimit {
                        button {
                            class: "btn primary",
                            onclick: move |_| async move {
                                match inbox_resume(item.id).await {
                                    Ok(()) => error.set(String::new()),
                                    Err(failure) => error.set(error_text(&failure)),
                                }
                            },
                            "Resume now"
                        }
                    } else {
                        a { href: "{item.link}", target: "_blank", "Open on GitHub" }
                    }
                    button {
                        class: "btn",
                        onclick: move |_| async move {
                            match inbox_dismiss(item.id).await {
                                Ok(()) => error.set(String::new()),
                                Err(failure) => error.set(error_text(&failure)),
                            }
                        },
                        "Dismiss"
                    }
                }
            }
        }
    }
}

#[component]
fn Activity() -> Element {
    let LocalOffset(local_offset) = use_context();
    let Workstreams(workstream_list) = use_context();
    let LiveState { feed, .. } = use_context();
    let Organization(organization) = use_context();
    let mut selected = use_signal(|| None::<(String, i64)>);
    let chips: Vec<Workstream> = match &*workstream_list.read() {
        Some(Ok(list)) => list
            .iter()
            .filter(|workstream| {
                mobius_domain::organization(&workstream.repository) == organization()
            })
            .cloned()
            .collect(),
        _ => Vec::new(),
    };
    let rows: Vec<FeedRow> = feed
        .read()
        .iter()
        .rev()
        .filter(|row| mobius_domain::organization(&row.repository) == organization())
        .filter(|row| {
            selected.read().as_ref().is_none_or(|(repository, number)| {
                row.repository == *repository && row.workstream == *number
            })
        })
        .cloned()
        .collect();
    rsx! {
        div { class: "head", h2 { "Activity" } }
        div { class: "chips",
            button {
                class: if selected.read().is_none() { "chip on" } else { "chip" },
                onclick: move |_| selected.set(None),
                "All"
            }
            for workstream in chips {
                button {
                    key: "{workstream.repository}#{workstream.number}",
                    class: if selected.read().as_ref() == Some(&(workstream.repository.clone(), workstream.number)) { "chip on" } else { "chip" },
                    onclick: move |_| selected.set(Some((workstream.repository.clone(), workstream.number))),
                    "{workstream.title}"
                }
            }
        }
        div { class: "list",
            for row in rows {
                div { key: "{row.id}", class: "item",
                    span { class: "muted small",
                        {row.time.to_offset(local_offset()).format(format_description!("[month]-[day] [hour]:[minute]")).unwrap_or_default()}
                    }
                    span { class: "grow", "@{row.actor} {row.text}" }
                    a { href: "{row.link}", target: "_blank", "#{row.issue}" }
                }
            }
        }
    }
}

#[component]
fn Chat(owner: String, repo: String, number: i64) -> Element {
    let repository = format!("{owner}/{repo}");
    let Workstreams(workstream_list) = use_context();
    let Organization(organization) = use_context();
    use_effect(use_reactive((&owner,), move |(owner,)| {
        select_organization(organization, owner)
    }));
    let mut sheet = use_signal(|| false);
    let workstream = match &*workstream_list.read() {
        Some(Ok(list)) => list
            .iter()
            .find(|workstream| workstream.repository == repository && workstream.number == number)
            .cloned(),
        _ => None,
    };
    rsx! {
        div { class: "page",
            Conversation {
                // A different Workstream mounts a new Conversation, so its state resets.
                key: "{repository}#{number}",
                organization: owner.clone(),
                repository: repository.clone(),
                number,
                agent: "Lead",
                brief: workstream.clone(),
                head: rsx! {
                    h2 { class: "ellip", {workstream.as_ref().map(|workstream| workstream.title.clone())} }
                    span { class: "num", "#{number}" }
                    if workstream.as_ref().is_some_and(|workstream| workstream.autopilot) {
                        span { class: "chip info", "Autopilot on" }
                    } else {
                        span { class: "chip plain", "Autopilot off" }
                    }
                },
                tail: rsx! {
                    button { class: "btn phone", onclick: move |_| sheet.set(true), "Agents" }
                },
            }
            aside { class: "side",
                Agents { repository: repository.clone(), number }
            }
            if sheet() {
                div { class: "sheet",
                    Agents { repository: repository.clone(), number, on_close: move |_| sheet.set(false) }
                }
            }
        }
    }
}

// The messages of the voice input: "started", "text" with the transcript, "error" with the code,
// "stopping" when a tap only asks the live session to stop, and "end" when the session ends.
const MIC_SCRIPT: &str = r#"
const Recognition = window.SpeechRecognition || window.webkitSpeechRecognition;
if (!Recognition) {
    dioxus.send({ type: "error", value: "unsupported" });
    dioxus.send({ type: "end" });
} else if (window.__mobiusMic) {
    const mic = window.__mobiusMic;
    try {
        mic.stop();
        dioxus.send({ type: "stopping" });
    } catch {
        mic.onend = mic.onresult = mic.onerror = null;
        window.__mobiusMic = null;
        dioxus.send({ type: "end" });
    }
} else {
    const recognition = new Recognition();
    window.__mobiusMic = recognition;
    recognition.lang = "en-US";
    recognition.continuous = false;
    recognition.interimResults = false;
    recognition.onresult = (event) => {
        const parts = [];
        for (const result of event.results) {
            parts.push(result[0].transcript);
        }
        dioxus.send({ type: "text", value: parts.join(" ") });
    };
    recognition.onerror = (event) => {
        if (event.error !== "aborted") {
            dioxus.send({ type: "error", value: event.error || "unknown" });
        }
    };
    recognition.onend = () => {
        if (window.__mobiusMic === recognition) {
            window.__mobiusMic = null;
            dioxus.send({ type: "end" });
        }
    };
    try {
        recognition.start();
        dioxus.send({ type: "started" });
    } catch {
        window.__mobiusMic = null;
        dioxus.send({ type: "error", value: "start" });
        dioxus.send({ type: "end" });
    }
}
"#;

fn mic_error(code: Option<&str>) -> String {
    match code {
        Some("unsupported") => "Voice input is not supported in this browser.".to_string(),
        Some("not-allowed") | Some("service-not-allowed") => {
            "Microphone access is denied. Allow the microphone in the browser settings.".to_string()
        }
        Some(code) => format!("Voice input failed: {code}"),
        None => "Voice input failed.".to_string(),
    }
}

#[component]
fn Conversation(
    organization: String,
    repository: String,
    number: i64,
    agent: &'static str,
    brief: Option<Workstream>,
    head: Element,
    tail: Element,
) -> Element {
    let key = (organization.clone(), repository.clone(), number);
    let state: LiveState = use_context();
    let LocalOffset(local_offset) = use_context();
    let history = use_resource(use_reactive(
        (&organization, &repository, &number),
        |(organization, repository, number)| async move {
            chat_view(organization, repository, number).await
        },
    ));
    let mut text = use_signal(String::new);
    let mut send_error = use_signal(String::new);
    let mut mic_active = use_signal(|| false);
    let mut brief_open = use_signal(|| None::<bool>);
    use_drop(|| {
        document::eval("window.__mobiusMic?.stop();");
    });

    let (mut messages, history_writing, harness) = match &*history.read() {
        Some(Ok(view)) => (view.messages.clone(), view.writing, Some(view.lead)),
        _ => (Vec::new(), false, None),
    };
    for message in state.messages.read().iter() {
        if message.organization != organization
            || message.repository != repository
            || message.workstream != number
        {
            continue;
        }
        upsert(&mut messages, message.clone());
    }
    messages.sort_by_key(|message| message.id);
    // While the history loads, `messages` is still empty, so the default waits for it.
    let open =
        brief_open().unwrap_or(matches!(&*history.read(), Some(Ok(_))) && messages.is_empty());
    let lead_state = state.leads.read().get(&key).cloned().unwrap_or(LeadState {
        writing: history_writing,
        error: None,
    });
    let last_agent_message = messages
        .iter()
        .rev()
        .find(|message| message.author != Author::Owner)
        .map(|message| message.id);
    let unread = state.unread.read().get(&key).copied().unwrap_or(0);
    use_effect(use_reactive(
        (
            &organization,
            &repository,
            &number,
            &last_agent_message,
            &unread,
        ),
        |(organization, repository, number, last_agent_message, unread)| {
            if unread > 0
                && let Some(message) = last_agent_message
            {
                spawn(async move {
                    // A failed call keeps the count, and the next message of the agent calls again.
                    let _ = chat_seen(organization, repository, number, message).await;
                });
            }
        },
    ));

    let send_key = (organization.clone(), repository.clone());
    let stop_key = (organization.clone(), repository.clone());
    rsx! {
        div { class: "column",
            div { class: "head",
                {head}
                span { class: "grow" }
                if let Some(harness) = harness {
                    span { class: "muted small ellip", "{agent}: {harness.name()}" }
                }
                {tail}
            }
            div { class: "chat",
                if let Some(workstream) = brief {
                    div { class: "brief",
                        button {
                            class: "briefhead",
                            onclick: move |_| brief_open.set(Some(!open)),
                            span { class: "grow ellip", "{workstream.title}" }
                            span { class: "muted", if open { "▾" } else { "▸" } }
                        }
                        if open {
                            Markdown { text: workstream.body }
                        }
                    }
                }
                div { class: "msgs",
                    if let Some(Err(error)) = &*history.read() {
                        div { class: "error", {error_text(error)} }
                    }
                    if messages.is_empty() {
                        div { class: "muted small empty", "No messages. Write to start a chat session." }
                    }
                    for message in messages {
                        div {
                            key: "{message.id}",
                            class: if message.author == Author::Owner { "msg owner" } else { "msg" },
                            div { class: "meta",
                                span {
                                    if message.author == Author::TellOwner {
                                        "Lead · event session"
                                    } else {
                                        {message.author.name()}
                                    }
                                }
                                span {
                                    {message.time.to_offset(local_offset()).format(format_description!("[hour]:[minute]")).unwrap_or_default()}
                                }
                            }
                            Markdown { text: message.text }
                        }
                    }
                    if lead_state.writing {
                        div { class: "typing",
                            span { class: "dot live" }
                            "The {agent} writes a reply."
                        }
                    }
                    if let Some(error) = lead_state.error {
                        div { class: "error", "The chat session failed: {error}" }
                    }
                }
                form {
                    class: "composer",
                    onsubmit: move |event: FormEvent| {
                        let (organization, repository) = send_key.clone();
                        async move {
                            event.prevent_default();
                            if text().trim().is_empty() {
                                return;
                            }
                            match chat_send(organization, repository, number, text()).await {
                                Ok(()) => {
                                    text.set(String::new());
                                    send_error.set(String::new());
                                }
                                Err(failure) => send_error.set(error_text(&failure)),
                            }
                        }
                    },
                    div { class: "grow",
                        textarea {
                            placeholder: "Write to the {agent}",
                            value: text,
                            oninput: move |event| text.set(event.value()),
                        }
                        div { class: "error", {send_error} }
                    }
                    if lead_state.writing {
                        button {
                            class: "btn danger",
                            r#type: "button",
                            onclick: move |_| {
                                let (organization, repository) = stop_key.clone();
                                async move {
                                    if let Err(failure) = chat_stop(organization, repository, number).await {
                                        send_error.set(error_text(&failure));
                                    }
                                }
                            },
                            "Stop"
                        }
                    }
                    button {
                        class: if mic_active() { "btn mic live" } else { "btn mic" },
                        r#type: "button",
                        onclick: move |_| {
                            let mut eval = document::eval(MIC_SCRIPT);
                            spawn(async move {
                                while let Ok(message) = eval.recv::<serde_json::Value>().await {
                                    let Some(kind) =
                                        message.get("type").and_then(|kind| kind.as_str())
                                    else {
                                        break;
                                    };
                                    match kind {
                                        "started" => {
                                            send_error.set(String::new());
                                            mic_active.set(true);
                                        }
                                        "text" => {
                                            if let Some(spoken) = message
                                                .get("value")
                                                .and_then(|value| value.as_str())
                                            {
                                                let mut current = text.peek().clone();
                                                if !current.is_empty() && !current.ends_with(' ') {
                                                    current.push(' ');
                                                }
                                                current.push_str(spoken.trim());
                                                text.set(current);
                                            }
                                        }
                                        "error" => send_error.set(mic_error(
                                            message
                                                .get("value")
                                                .and_then(|value| value.as_str()),
                                        )),
                                        "stopping" => break,
                                        _ => {}
                                    }
                                    if kind == "end" {
                                        mic_active.set(false);
                                        break;
                                    }
                                }
                            });
                        },
                        if mic_active() { "Stop mic" } else { "Mic" }
                    }
                    button { class: "btn primary", r#type: "submit", "Send" }
                }
            }
        }
    }
}

#[component]
fn Agents(repository: String, number: i64, on_close: Option<EventHandler>) -> Element {
    let state: LiveState = use_context();
    let tree = use_resource(use_reactive(
        (&repository, &number),
        |(repository, number)| async move { agent_tree(repository, number).await },
    ));
    let mut tasks_tab = use_signal(|| false);
    let mut selected = use_signal(|| None::<i64>);

    let mut nodes: HashMap<i64, AgentNode> = match &*tree.read() {
        Some(Ok(list)) => list
            .iter()
            .map(|node| (node.session.id, node.clone()))
            .collect(),
        _ => HashMap::new(),
    };
    for node in state.agents.read().values() {
        if node.session.repository != repository || node.session.workstream != number {
            continue;
        }
        // A session never starts again, so an ended node is newer than a live node.
        let known = nodes.get(&node.session.id);
        if known.is_none_or(|known| known.session.ended_at.is_none()) {
            nodes.insert(node.session.id, node.clone());
        }
    }
    let mut nodes: Vec<AgentNode> = nodes.into_values().collect();
    nodes.sort_by_key(|node| Reverse(node.session.id));
    let close = on_close.map(|on_close| {
        rsx! {
            button { class: "btn ghost", onclick: move |_| on_close.call(()), "Close" }
        }
    });

    if let Some(node) = selected().and_then(|id| nodes.iter().find(|node| node.session.id == id)) {
        return rsx! {
            div { class: "head",
                button { class: "back", onclick: move |_| selected.set(None), "‹ Agents" }
                h2 { class: "ellip grow", "{node.role} {node.title}" }
                {close}
            }
            Transcript { session: node.session.id }
        };
    }
    rsx! {
        div { class: "sidetabs",
            button { class: if !tasks_tab() { "on" }, onclick: move |_| tasks_tab.set(false), "Agents" }
            button { class: if tasks_tab() { "on" }, onclick: move |_| tasks_tab.set(true), "Tasks" }
            span { class: "grow" }
            {close}
        }
        div { class: "scroll",
            if tasks_tab() {
                Tasks { repository: repository.clone(), number }
            } else {
                if let Some(Err(error)) = &*tree.read() {
                    div { class: "error note", {error_text(error)} }
                }
                for node in nodes {
                    AgentEntry {
                        key: "{node.session.id}",
                        node: node.clone(),
                        onclick: move |_| selected.set(Some(node.session.id)),
                    }
                }
            }
        }
    }
}

// The tab mounts this component each time it opens, so each open reads the list again.
#[component]
fn Tasks(repository: String, number: i64) -> Element {
    let lines = use_resource(use_reactive(
        (&repository, &number),
        |(repository, number)| async move { task_list(repository, number).await },
    ));
    match &*lines.read() {
        None => rsx! {},
        Some(Err(error)) => rsx! {
            div { class: "error note", {error_text(error)} }
        },
        Some(Ok(lines)) if lines.is_empty() => rsx! {
            div { class: "muted small note", "No tasks." }
        },
        Some(Ok(lines)) => rsx! {
            for line in lines.iter().cloned() {
                TaskEntry { key: "{line.number}", line }
            }
        },
    }
}

#[component]
fn TaskEntry(line: TaskLine) -> Element {
    rsx! {
        a { class: "node", href: "{line.url}", target: "_blank",
            span { class: "grow", "#{line.number} {line.title}" }
            for blocker in line.blocked_by.iter() {
                span { class: "muted small",
                    match &blocker.workstream_title {
                        Some(title) => format!("blocked by #{} (Workstream \"{title}\")", blocker.number),
                        None => format!("blocked by #{}", blocker.number),
                    }
                }
            }
            span { class: if line.state == "open" { "chip plain" } else { "chip" }, "{line.state}" }
        }
    }
}

#[component]
fn AgentEntry(node: AgentNode, onclick: EventHandler<MouseEvent>) -> Element {
    let LocalOffset(local_offset) = use_context();
    let session = &node.session;
    let time = format_description!("[month]-[day] [hour]:[minute]");
    let start = session
        .started_at
        .to_offset(local_offset())
        .format(time)
        .unwrap_or_default();
    let end = session
        .ended_at
        .map(|ended_at| {
            format!(
                "–{}",
                ended_at
                    .to_offset(local_offset())
                    .format(format_description!("[hour]:[minute]"))
                    .unwrap_or_default()
            )
        })
        .unwrap_or_default();
    let dot = match (&session.queue_reason, session.ended_at) {
        (Some(_), _) => "dot queued",
        (None, None) => "dot live",
        (None, Some(_)) => "dot ended",
    };
    let detail = session
        .queue_reason
        .clone()
        .unwrap_or_else(|| format!("{start}{end}"));
    rsx! {
        button { class: "node", onclick: move |event| onclick.call(event),
            span { class: dot }
            span { class: "grow",
                span { class: "role", "{node.role}" }
                " {node.title}"
                div { class: "muted small", "{session.harness.name()} · {session.model} · {detail}" }
            }
            if let Some(reason) = &session.queue_reason {
                if reason.starts_with(PAUSED) {
                    span { class: "chip warn", "paused" }
                } else {
                    span { class: "chip warn", "queued" }
                }
            }
        }
    }
}

#[component]
fn Transcript(session: i64) -> Element {
    let lines = use_resource(use_reactive(&session, |session| async move {
        transcript_lines(session).await
    }));
    rsx! {
        div { class: "scroll tx",
            match &*lines.read() {
                Some(Ok(lines)) => rsx! {
                    for line in lines.clone() {
                        TranscriptEntry { key: "{line.id}", line }
                    }
                },
                Some(Err(error)) => rsx! { div { class: "error", {error_text(error)} } },
                None => rsx! {},
            }
        }
        div { class: "readonly", "Read only. The Owner talks only to the Lead." }
    }
}

#[component]
fn TranscriptEntry(line: TranscriptLine) -> Element {
    let LocalOffset(local_offset) = use_context();
    let mut open = use_signal(|| !line.folded);
    let mut raw = use_signal(|| false);
    rsx! {
        div { class: if line.error { "tr crit" } else { "tr" },
            span { class: "num",
                {line.time.to_offset(local_offset()).format(format_description!("[hour]:[minute]")).unwrap_or_default()}
            }
            span { class: "k", "{line.kind}" }
            div {
                span { "{line.text}" }
                if let Some(name) = &line.harness_tool_name {
                    span { class: "muted small", " {name}" }
                }
                if line.folded && line.body.is_some() {
                    button { class: "btn ghost small", onclick: move |_| open.toggle(),
                        if open() { "Hide" } else { "Show" }
                    }
                }
                button { class: "btn ghost small", onclick: move |_| raw.toggle(), "Raw" }
                if let Some(body) = line.body.as_ref().filter(|_| open()) {
                    pre { "{body}" }
                }
                if raw() {
                    pre { "{line.raw}" }
                }
            }
        }
    }
}

#[component]
fn Login() -> Element {
    let LoginShown(mut login_shown) = use_context();
    let mut password = use_signal(String::new);
    let mut error = use_signal(String::new);
    rsx! {
        div { class: "login",
            form {
                onsubmit: move |event: FormEvent| async move {
                    event.prevent_default();
                    match login(password()).await {
                        Ok(_) => login_shown.set(false),
                        Err(failure) => error.set(error_text(&failure)),
                    }
                },
                div { class: "brand", "Mobius" }
                label { class: "label", r#for: "password", "Access password" }
                input {
                    id: "password",
                    r#type: "password",
                    autocomplete: "current-password",
                    value: password,
                    oninput: move |event| password.set(event.value()),
                }
                div { class: "error", {error} }
                button { class: "btn primary", r#type: "submit", "Log in" }
            }
        }
    }
}

#[component]
fn Devices() -> Element {
    let LocalOffset(local_offset) = use_context();
    let LoginShown(mut login_shown) = use_context();
    let mut resource = use_resource(devices);
    use_effect(move || {
        if let Some(Err(error)) = &*resource.read()
            && unauthorized(error)
        {
            login_shown.set(true);
        }
    });
    let list = match &*resource.read() {
        Some(Ok(devices)) => rsx! {
            div { class: "list",
                for login in devices.logins.clone() {
                    div { key: "{login.id}", class: "item",
                        div { class: "grow",
                            div { "{login.user_agent}" }
                            div { class: "muted small",
                                "logged in "
                                {login.created_at.to_offset(local_offset()).format(format_description!("[year]-[month]-[day] [hour]:[minute]")).unwrap_or_default()}
                            }
                        }
                        if login.id == devices.this_device {
                            span { class: "chip", "this device" }
                        }
                        button {
                            class: "btn",
                            onclick: move |_| async move {
                                if logout(login.id).await.is_ok() {
                                    resource.restart();
                                }
                            },
                            "Log out"
                        }
                    }
                }
            }
        },
        Some(Err(error)) => rsx! { p { class: "error note", {error_text(error)} } },
        None => rsx! {},
    };
    rsx! {
        div { class: "head", h2 { "Devices" } }
        {list}
    }
}

async fn create_app(account: String, name: String) -> Result<(), String> {
    let origin: String = document::eval("return window.location.origin;")
        .join()
        .await
        .map_err(|failure| failure.to_string())?;
    let form = github_manifest(account, name, origin)
        .await
        .map_err(|failure| error_text(&failure))?;
    let url = serde_json::to_string(&form.url).map_err(|failure| failure.to_string())?;
    let manifest = serde_json::to_string(&form.manifest).map_err(|failure| failure.to_string())?;
    document::eval(&format!(
        r#"
        const form = document.createElement("form");
        form.method = "post";
        form.action = {url};
        const field = document.createElement("input");
        field.type = "hidden";
        field.name = "manifest";
        field.value = {manifest};
        form.append(field);
        document.body.append(form);
        form.submit();
        "#
    ));
    Ok(())
}

#[component]
fn GitHub() -> Element {
    let AppSlugs(app_slugs) = use_context();
    let mut account = use_signal(String::new);
    let mut name = use_signal(String::new);
    let mut error = use_signal(String::new);
    let slugs = match &*app_slugs.read() {
        Some(Ok(slugs)) => slugs.clone(),
        _ => Vec::new(),
    };
    rsx! {
        div { class: "head", h2 { "Connect GitHub" } }
        for slug in slugs.iter() {
            p { key: "{slug}", class: "note",
                a { href: "https://github.com/apps/{slug}/installations/new", "Install {slug} on your repositories" }
            }
        }
        if !slugs.is_empty() {
            div { class: "label note", "Add an organization" }
        }
        form {
            class: "connect",
            onsubmit: move |event: FormEvent| async move {
                event.prevent_default();
                if let Err(text) = create_app(account(), name()).await {
                    error.set(text);
                }
            },
            label { class: "label", r#for: "account", "Account or organization" }
            input {
                id: "account",
                value: account,
                oninput: move |event| account.set(event.value()),
            }
            label { class: "label", r#for: "name", "App name" }
            input {
                id: "name",
                placeholder: "Mobius {account}",
                value: name,
                oninput: move |event| name.set(event.value()),
            }
            p { class: "muted small", "GitHub App names are unique on all of GitHub. Use a name that no other App has, for example with your account name." }
            div { class: "error", {error} }
            button { class: "btn primary", r#type: "submit", "Create the App" }
        }
    }
}
