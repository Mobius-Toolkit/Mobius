use std::cmp::Reverse;
use std::collections::HashMap;

use dioxus::prelude::*;
use mobius_api::{
    agent_tree, chat_seen, chat_send, chat_stop, chat_view, devices, github_app, github_manifest,
    inbox_dismiss, inbox_items, live, login, logout, task_list, transcript_lines, unread,
    workstreams,
};
use mobius_domain::{
    AgentNode, Author, ChatMessage, FeedRow, InboxItem, Live, TaskLine, TranscriptLine, Workstream,
};
use time::macros::format_description;

#[derive(Clone, PartialEq, Routable)]
#[rustfmt::skip]
pub enum Route {
    #[layout(Shell)]
        #[redirect("/", || Route::WorkstreamList {})]
        #[route("/workstreams")]
        WorkstreamList {},
        #[route("/workstreams/:owner/:repo/:number")]
        Chat { owner: String, repo: String, number: i64 },
        #[route("/inbox")]
        Inbox {},
        #[route("/activity")]
        Activity {},
        #[route("/devices")]
        Devices {},
        #[route("/github")]
        GitHub {},
}

#[derive(Clone, Copy)]
struct LoginShown(Signal<bool>);

#[derive(Clone, Copy)]
struct AppSlug(Resource<ServerFnResult<Option<String>>>);

#[derive(Clone, Copy)]
struct Workstreams(Resource<ServerFnResult<Vec<Workstream>>>);

type ChatKey = (String, i64);

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
                    .map(|unread| ((unread.repository, unread.workstream), unread.count))
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
                            repository,
                            workstream,
                            writing,
                            error,
                        } => {
                            state
                                .leads
                                .write()
                                .insert((repository, workstream), LeadState { writing, error });
                        }
                        Live::Unread(unread) => {
                            state
                                .unread
                                .write()
                                .insert((unread.repository, unread.workstream), unread.count);
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
    let app_slug = use_resource(github_app);
    use_context_provider(|| AppSlug(app_slug));
    let workstream_list = use_resource(workstreams);
    use_context_provider(|| Workstreams(workstream_list));
    let state = use_context_provider(|| LiveState {
        feed: Signal::new(Vec::new()),
        messages: Signal::new(Vec::new()),
        leads: Signal::new(HashMap::new()),
        unread: Signal::new(HashMap::new()),
        agents: Signal::new(HashMap::new()),
        inbox: Signal::new(HashMap::new()),
    });
    use_effect(move || {
        if let Some(Err(error)) = &*app_slug.read()
            && unauthorized(error)
        {
            login_shown.set(true);
        }
    });
    use_effect(move || {
        spawn(follow_live(state, workstream_list, login_shown));
    });
    let inbox_count = state.inbox.read().len();
    match &*app_slug.read() {
        Some(Ok(Some(_))) => rsx! {
            div { class: "shell",
                nav { class: "rail",
                    div { class: "brand", "Mobius" }
                    Link { class: "entry", active_class: "sel", to: Route::Inbox {},
                        span { class: "grow", "Inbox" }
                        if inbox_count > 0 {
                            span { class: "count", "{inbox_count}" }
                        }
                    }
                    Link { class: "navbtn", active_class: "sel", to: Route::Activity {}, "Activity" }
                    div { class: "label section", "Workstreams" }
                    WorkstreamEntries {}
                    div { class: "grow" }
                    Link { class: "navbtn", active_class: "sel", to: Route::GitHub {}, "GitHub" }
                    Link { class: "navbtn", active_class: "sel", to: Route::Devices {}, "Devices" }
                }
                main { class: "center", Outlet::<Route> {} }
                nav { class: "tabs",
                    Link { active_class: "on", to: Route::WorkstreamList {}, "Workstreams" }
                    Link { active_class: "on", to: Route::Inbox {},
                        "Inbox"
                        if inbox_count > 0 {
                            " "
                            span { class: "count", "{inbox_count}" }
                        }
                    }
                    Link { active_class: "on", to: Route::Activity {}, "Activity" }
                    Link { active_class: "on", to: Route::GitHub {}, "GitHub" }
                    Link { active_class: "on", to: Route::Devices {}, "Devices" }
                }
            }
        },
        Some(Ok(None)) => rsx! { main { class: "center", GitHub {} } },
        Some(Err(error)) => rsx! { p { class: "error note", {error_text(error)} } },
        None => rsx! {},
    }
}

#[component]
fn WorkstreamEntries() -> Element {
    let Workstreams(workstream_list) = use_context();
    match &*workstream_list.read() {
        Some(Ok(list)) => rsx! {
            for workstream in list.clone() {
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
        .get(&(workstream.repository.clone(), workstream.number))
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
        div { class: "head", h2 { "Workstreams" } }
        div { class: "list", WorkstreamEntries {} }
    }
}

#[component]
fn Inbox() -> Element {
    let Workstreams(workstream_list) = use_context();
    let LiveState { inbox, .. } = use_context();
    let mut error = use_signal(String::new);
    let mut items: Vec<InboxItem> = inbox.read().values().cloned().collect();
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
                            {item.time.format(format_description!("[month]-[day] [hour]:[minute]")).unwrap_or_default()}
                        }
                    }
                    a { href: "{item.link}", target: "_blank", "Open on GitHub" }
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
    let Workstreams(workstream_list) = use_context();
    let LiveState { feed, .. } = use_context();
    let mut selected = use_signal(|| None::<(String, i64)>);
    let chips = match &*workstream_list.read() {
        Some(Ok(list)) => list.clone(),
        _ => Vec::new(),
    };
    let rows: Vec<FeedRow> = feed
        .read()
        .iter()
        .rev()
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
                        {row.time.format(format_description!("[month]-[day] [hour]:[minute]")).unwrap_or_default()}
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
    let key = (repository.clone(), number);
    let Workstreams(workstream_list) = use_context();
    let state: LiveState = use_context();
    let history = use_resource(use_reactive(
        (&repository, &number),
        |(repository, number)| async move { chat_view(repository, number).await },
    ));
    let mut text = use_signal(String::new);
    let mut send_error = use_signal(String::new);
    let mut sheet = use_signal(|| false);

    let workstream = match &*workstream_list.read() {
        Some(Ok(list)) => list
            .iter()
            .find(|workstream| workstream.repository == repository && workstream.number == number)
            .cloned(),
        _ => None,
    };
    let (mut messages, history_writing, lead) = match &*history.read() {
        Some(Ok(view)) => (view.messages.clone(), view.writing, Some(view.lead)),
        _ => (Vec::new(), false, None),
    };
    for message in state.messages.read().iter() {
        if message.repository != repository || message.workstream != number {
            continue;
        }
        upsert(&mut messages, message.clone());
    }
    messages.sort_by_key(|message| message.id);
    let lead_state = state.leads.read().get(&key).cloned().unwrap_or(LeadState {
        writing: history_writing,
        error: None,
    });
    let last_lead_message = messages
        .iter()
        .rev()
        .find(|message| message.author != Author::Owner)
        .map(|message| message.id);
    let unread = state.unread.read().get(&key).copied().unwrap_or(0);
    use_effect(use_reactive(
        (&repository, &number, &last_lead_message, &unread),
        |(repository, number, last_lead_message, unread)| {
            if unread > 0
                && let Some(message) = last_lead_message
            {
                spawn(async move {
                    // A failed call keeps the count, and the next Lead message calls again.
                    let _ = chat_seen(repository, number, message).await;
                });
            }
        },
    ));

    let send_repository = repository.clone();
    let stop_repository = repository.clone();
    rsx! {
        div { class: "page",
            div { class: "column",
                div { class: "head",
                    h2 { class: "ellip", {workstream.as_ref().map(|workstream| workstream.title.clone())} }
                    span { class: "num", "#{number}" }
                    if workstream.as_ref().is_some_and(|workstream| workstream.autopilot) {
                        span { class: "chip info", "Autopilot on" }
                    } else {
                        span { class: "chip plain", "Autopilot off" }
                    }
                    span { class: "grow" }
                    if let Some(lead) = lead {
                        span { class: "muted small", "Lead: {lead.name()}" }
                    }
                    button { class: "btn phone", onclick: move |_| sheet.set(true), "Agents" }
                }
                div { class: "chat",
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
                                        {message.time.format(format_description!("[hour]:[minute]")).unwrap_or_default()}
                                    }
                                }
                                p { "{message.text}" }
                            }
                        }
                        if lead_state.writing {
                            div { class: "typing",
                                span { class: "dot live" }
                                "The Lead writes a reply."
                            }
                        }
                        if let Some(error) = lead_state.error {
                            div { class: "error", "The chat session failed: {error}" }
                        }
                    }
                    form {
                        class: "composer",
                        onsubmit: move |event: FormEvent| {
                            let repository = send_repository.clone();
                            async move {
                                event.prevent_default();
                                if text().trim().is_empty() {
                                    return;
                                }
                                match chat_send(repository, number, text()).await {
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
                                placeholder: "Write to the Lead",
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
                                    let repository = stop_repository.clone();
                                    async move {
                                        if let Err(failure) = chat_stop(repository, number).await {
                                            send_error.set(error_text(&failure));
                                        }
                                    }
                                },
                                "Stop"
                            }
                        }
                        button { class: "btn primary", r#type: "submit", "Send" }
                    }
                }
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
            span { class: if line.state == "open" { "chip plain" } else { "chip" }, "{line.state}" }
        }
    }
}

#[component]
fn AgentEntry(node: AgentNode, onclick: EventHandler<MouseEvent>) -> Element {
    let session = &node.session;
    let time = format_description!("[month]-[day] [hour]:[minute]");
    let start = session.started_at.format(time).unwrap_or_default();
    let end = session
        .ended_at
        .map(|ended_at| {
            format!(
                "–{}",
                ended_at
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
            if session.queue_reason.is_some() {
                span { class: "chip warn", "queued" }
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
    let mut open = use_signal(|| !line.folded);
    let mut raw = use_signal(|| false);
    rsx! {
        div { class: if line.error { "tr crit" } else { "tr" },
            span { class: "num",
                {line.time.format(format_description!("[hour]:[minute]")).unwrap_or_default()}
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
                                {login.created_at.format(format_description!("[year]-[month]-[day] [hour]:[minute] UTC")).unwrap_or_default()}
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

async fn create_app(account: String) -> Result<(), String> {
    let origin: String = document::eval("return window.location.origin;")
        .join()
        .await
        .map_err(|failure| failure.to_string())?;
    let form = github_manifest(account, origin)
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
    let AppSlug(app_slug) = use_context();
    let mut account = use_signal(String::new);
    let mut error = use_signal(String::new);
    let body = match &*app_slug.read() {
        Some(Ok(Some(slug))) => rsx! {
            p { class: "note",
                a { href: "https://github.com/apps/{slug}/installations/new", "Install the App on your repositories" }
            }
        },
        _ => rsx! {
            form {
                class: "connect",
                onsubmit: move |event: FormEvent| async move {
                    event.prevent_default();
                    if let Err(text) = create_app(account()).await {
                        error.set(text);
                    }
                },
                label { class: "label", r#for: "account", "Account or organization" }
                input {
                    id: "account",
                    value: account,
                    oninput: move |event| account.set(event.value()),
                }
                div { class: "error", {error} }
                button { class: "btn primary", r#type: "submit", "Create the App" }
            }
        },
    };
    rsx! {
        div { class: "head", h2 { "Connect GitHub" } }
        {body}
    }
}
