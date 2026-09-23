//! Chat page: grouped conversation sidebar (organization + per project),
//! streaming thread, tool-call cards, permission banner, config selectors,
//! Memory/Research/Tasks side panels, new-conversation dialog.

use crate::{Data, Nav, api, panels};
use dioxus::prelude::*;
// `mobius_core::Signal` collides with `dioxus::prelude::Signal` under globs.
use dioxus::prelude::Signal;
use mobius_api::*;
use mobius_core::*;
use std::cell::RefCell;
use std::rc::Rc;
use wasm_bindgen::JsCast;
use wasm_bindgen::closure::Closure;

/// Subscribe to `?conversation_id=` filtered SSE; recreates the source when
/// the selected conversation changes.
fn use_conv_events(selected: Signal<Option<ConversationId>>, on_event: Callback<EventEnvelope>) {
    let holder = use_hook(|| Rc::new(RefCell::new(None::<web_sys::EventSource>)));
    use_effect(move || {
        if let Some(old) = holder.borrow_mut().take() {
            old.close();
        }
        if let Some(id) = selected() {
            let url = format!("/api/v1/events?conversation_id={id}");
            if let Ok(es) = web_sys::EventSource::new(&url) {
                let cb = Closure::wrap(Box::new(move |ev: web_sys::MessageEvent| {
                    if let Some(text) = ev.data().as_string()
                        && let Ok(env) = serde_json::from_str::<EventEnvelope>(&text)
                    {
                        on_event.call(env);
                    }
                }) as Box<dyn FnMut(_)>);
                es.set_onmessage(Some(cb.as_ref().unchecked_ref()));
                cb.forget();
                *holder.borrow_mut() = Some(es);
            }
        }
    });
}

fn refresh_view(
    id: ConversationId,
    mut view: Signal<Option<ConversationView>>,
    mut status: Signal<ConversationStatus>,
) {
    spawn(async move {
        if let Ok(v) = api::get::<ConversationView>(&format!("/conversations/{id}")).await {
            status.set(v.conversation.status);
            view.set(Some(v));
        }
    });
}

fn send_message(
    id: ConversationId,
    text: String,
    mut input: Signal<String>,
    mut busy: Signal<bool>,
    mut send_error: Signal<Option<String>>,
    mut data_error: Signal<Option<String>>,
) {
    if text.trim().is_empty() {
        return;
    }
    input.set(String::new());
    busy.set(true);
    spawn(async move {
        let r = api::post::<PostMessage, Message>(
            &format!("/conversations/{id}/messages"),
            &PostMessage { text },
        )
        .await;
        busy.set(false);
        match r {
            Ok(_) => send_error.set(None),
            Err(e) => {
                let m = format!("send failed: {e}");
                data_error.set(Some(m.clone()));
                send_error.set(Some(m));
            }
        }
    });
}

/// Which right-column panel is open (one at a time).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Panel {
    Memory,
    Research,
    Tasks,
}

/// The "+" target for a new chat: `None` = organization, `Some` = project.
/// `show_new` adds the outer "dialog open" Option.
type NewChatScope = Option<ProjectId>;

#[component]
fn ConvRow(
    c: Conversation,
    selected: Signal<Option<ConversationId>>,
    view: Signal<Option<ConversationView>>,
    status: Signal<ConversationStatus>,
    draft: Signal<String>,
    pending: Signal<Option<PermissionRequest>>,
) -> Element {
    let dot = format!("dot {}", c.status);
    rsx! {
        div {
            key: "{c.id}",
            class: if selected() == Some(c.id) { "conv active" } else { "conv" },
            onclick: move |_| {
                let id = c.id;
                selected.set(Some(id));
                draft.set(String::new());
                pending.set(None);
                refresh_view(id, view, status);
            },
            span { class: "{dot}" }
            b { "{c.title}" }
        }
    }
}

#[component]
pub fn ChatPage() -> Element {
    let mut data: Data = use_context();
    let nav: Nav = use_context();
    let mut selected = use_signal(|| None::<ConversationId>);
    let mut view = use_signal(|| None::<ConversationView>);
    let mut draft = use_signal(String::new);
    let mut input = use_signal(String::new);
    let mut pending = use_signal(|| None::<PermissionRequest>);
    let mut status = use_signal(|| ConversationStatus::Idle);
    let send_error = use_signal(|| None::<String>);
    let mut show_new = use_signal(|| None::<NewChatScope>);
    let busy = use_signal(|| false);
    let mut panel = use_signal(|| None::<Panel>);

    use_hook(move || data.refresh_kind(EntityKind::Conversation));

    // Another page (Work/Runs/panels) asked us to open a conversation.
    {
        let mut nav = nav;
        use_effect(move || {
            // Read-then-clear: `chat_target()` clones + drops the borrow.
            let target = (nav.chat_target)();
            if let Some(id) = target {
                nav.chat_target.set(None);
                selected.set(Some(id));
                draft.set(String::new());
                pending.set(None);
                refresh_view(id, view, status);
            }
        });
    }

    // Conversation-scoped SSE.
    let mut upsert_message = move |message: Message| {
        if let Some(v) = view.write().as_mut() {
            match v.messages.iter_mut().find(|m| m.id == message.id) {
                Some(slot) => *slot = message,
                None => v.messages.push(message),
            }
        }
    };
    let on_event = Callback::new(move |env: EventEnvelope| match env.event {
        DomainEvent::MessageDelta { text, .. } => {
            draft.write().push_str(&text);
        }
        DomainEvent::MessageUpdated { message, .. } => {
            // A block just flushed mid-turn (tool card, thought, …) — the
            // streamed text it swallowed is no longer a live delta.
            draft.set(String::new());
            upsert_message(message);
        }
        DomainEvent::MessageAppended { message, .. } => {
            draft.set(String::new());
            upsert_message(message);
        }
        DomainEvent::PermissionRequested { request } => {
            pending.set(Some(request));
        }
        DomainEvent::ConversationStatusChanged {
            conversation_id,
            status: s,
        } => {
            status.set(s);
            // Keep the sidebar entry live too, not just the open thread.
            if let Some(c) = data
                .conversations
                .write()
                .iter_mut()
                .find(|c| c.id == conversation_id)
            {
                c.status = s;
            }
        }
        DomainEvent::ConversationConfigChanged { config_options, .. } => {
            if let Some(v) = view.write().as_mut() {
                v.conversation.config_options = config_options;
            }
        }
        _ => {}
    });
    use_conv_events(selected, on_event);

    // Sidebar: chats only (run/research transcripts open from the panels),
    // grouped organization first then per project, newest first.
    let mut chats: Vec<Conversation> = data
        .conversations
        .read()
        .iter()
        .filter(|c| c.kind == ConversationKind::Chat)
        .cloned()
        .collect();
    chats.sort_by_key(|a| std::cmp::Reverse(a.updated_at));
    let orgs = data.orgs.read().clone();
    let projects = {
        let mut p = data.projects.read().clone();
        p.sort_by(|a, b| a.slug.cmp(&b.slug));
        p
    };
    // Owned groups: rsx `for` iterators must not borrow locals that inner
    // closures capture.
    let org_groups: Vec<(Organization, Vec<Conversation>)> = orgs
        .iter()
        .map(|o| {
            (
                o.clone(),
                chats
                    .iter()
                    .filter(|c| c.organization_id == o.id && c.project_id.is_none())
                    .cloned()
                    .collect(),
            )
        })
        .collect();
    let proj_groups: Vec<(Project, Vec<Conversation>)> = projects
        .iter()
        .map(|p| {
            (
                p.clone(),
                chats
                    .iter()
                    .filter(|c| c.project_id == Some(p.id))
                    .cloned()
                    .collect(),
            )
        })
        .collect();

    let conv_view = view.read().clone();
    let conv = conv_view.as_ref().map(|v| v.conversation.clone());
    // Breadcrumb: org name, project slug when the conversation has one.
    let org_name = conv
        .as_ref()
        .and_then(|c| {
            orgs.iter()
                .find(|o| o.id == c.organization_id)
                .map(|o| o.name.clone())
        })
        .unwrap_or_default();
    let project_slug = conv.as_ref().and_then(|c| c.project_id).and_then(|pid| {
        projects
            .iter()
            .find(|p| p.id == pid)
            .map(|p| p.slug.clone())
    });

    rsx! {
        div { class: "chat-layout",
            aside { class: "conv-list",
                for (org, convs) in org_groups {
                    div { class: "conv-group", key: "org-{org.id}",
                        div { class: "conv-group-head",
                            span { "Organization: {org.name}" }
                            button {
                                title: "New organization chat",
                                onclick: move |_| show_new.set(Some(None)),
                                "+"
                            }
                        }
                        for c in convs {
                            ConvRow {
                                key: "{c.id}",
                                c,
                                selected,
                                view,
                                status,
                                draft,
                                pending,
                            }
                        }
                    }
                }
                for (p, convs) in proj_groups {
                    div { class: "conv-group", key: "proj-{p.id}",
                        div { class: "conv-group-head",
                            span { "{p.slug}" }
                            button {
                                title: "New project chat",
                                onclick: move |_| {
                                    show_new.set(Some(Some(p.id)));
                                },
                                "+"
                            }
                        }
                        for c in convs {
                            ConvRow {
                                key: "{c.id}",
                                c,
                                selected,
                                view,
                                status,
                                draft,
                                pending,
                            }
                        }
                    }
                }
            }
            section { class: "chat-main",
                if let Some(conv) = conv.clone() {
                    div { class: "chat-header",
                        span { class: "crumbs",
                            b { "{org_name}" }
                            if let Some(slug) = &project_slug {
                                " / {slug}"
                            }
                            " · {conv.title}"
                        }
                        span { class: "muted", " {status()}" }
                        for opt in conv.config_options.clone() {
                            if !opt.options.is_empty() {
                                label { class: "cfg", key: "{opt.id}",
                                    "{opt.name}: "
                                    select {
                                        value: "{opt.current_value.clone().unwrap_or_default()}",
                                        onchange: move |e| {
                                            let cid = opt.id.clone();
                                            let val = e.value();
                                            let id = conv.id;
                                            spawn(async move {
                                                let _ = api::post::<
                                                    SetConversationConfig,
                                                    Vec<SessionConfigOption>,
                                                >(
                                                    &format!("/conversations/{id}/config"),
                                                    &SetConversationConfig {
                                                        config_id: cid,
                                                        value: val,
                                                    },
                                                )
                                                .await;
                                            });
                                        },
                                        for choice in opt.options.clone() {
                                            option {
                                                key: "{choice.value}",
                                                value: "{choice.value}",
                                                "{choice.name}"
                                            }
                                        }
                                    }
                                }
                            }
                        }
                        button {
                            onclick: move |_| {
                                let id = conv.id;
                                spawn(async move {
                                    let _ = api::post_empty::<serde_json::Value>(
                                        &format!("/conversations/{id}/cancel"),
                                        &serde_json::json!({}),
                                    )
                                    .await;
                                });
                            },
                            "Cancel"
                        }
                        button {
                            class: if panel() == Some(Panel::Memory) { "toggle active" } else { "toggle" },
                            onclick: move |_| {
                                panel.set(if panel() == Some(Panel::Memory) {
                                    None
                                } else {
                                    Some(Panel::Memory)
                                });
                            },
                            "Memory ({panels::memory_count(&data, &conv)})"
                        }
                        button {
                            class: if panel() == Some(Panel::Research) { "toggle active" } else { "toggle" },
                            onclick: move |_| {
                                panel.set(if panel() == Some(Panel::Research) {
                                    None
                                } else {
                                    Some(Panel::Research)
                                });
                            },
                            "Research ({panels::research_count(&data, &conv)})"
                        }
                        if conv.project_id.is_some() {
                            button {
                                class: if panel() == Some(Panel::Tasks) { "toggle active" } else { "toggle" },
                                onclick: move |_| {
                                    panel.set(if panel() == Some(Panel::Tasks) {
                                        None
                                    } else {
                                        Some(Panel::Tasks)
                                    });
                                },
                                "Tasks ({panels::task_count(&data, &conv)})"
                            }
                        }
                    }
                    div { class: "chat-body",
                        div { class: "thread",
                            for msg in conv_view.as_ref().map(|v| v.messages.clone()).unwrap_or_default() {
                                div {
                                    key: "{msg.id}",
                                    class: match msg.author {
                                        MessageAuthor::Human => "msg human",
                                        MessageAuthor::Agent => "msg agent",
                                        MessageAuthor::System => "msg system",
                                    },
                                    for block in msg.blocks.iter() {
                                        BlockView { block: block.clone() }
                                    }
                                }
                            }
                            if !draft.read().is_empty() {
                                div { class: "msg agent streaming",
                                    p { "{draft.read()}" }
                                }
                            } else if status() == ConversationStatus::Streaming || busy() {
                                div { class: "msg agent streaming",
                                    span { class: "typing",
                                        i {}
                                        i {}
                                        i {}
                                    }
                                }
                            }
                            if let Some(req) = pending.read().clone() {
                                div { class: "permission-banner",
                                    b { "Permission requested: " }
                                    span { "{req.tool_call.title.clone().unwrap_or_default()} ({req.tool_call.kind.clone().unwrap_or_default()})" }
                                    div { class: "row",
                                        for opt in req.options.clone() {
                                            button {
                                                key: "{opt.option_id}",
                                                class: if opt.kind.contains("allow") { "allow" } else { "deny" },
                                                onclick: move |_| {
                                                    let pid = req.id;
                                                    let oid = opt.option_id.clone();
                                                    pending.set(None);
                                                    spawn(async move {
                                                        let _ = api::post_empty(
                                                            &format!("/permissions/{pid}"),
                                                            &ResolvePermission { option_id: oid },
                                                        )
                                                        .await;
                                                    });
                                                },
                                                "{opt.name}"
                                            }
                                        }
                                    }
                                }
                            }
                        }
                        match panel() {
                            Some(Panel::Memory) => rsx! {
                                panels::MemoryPanel {
                                    conv: conv.clone(),
                                    on_close: move |_| panel.set(None),
                                }
                            },
                            Some(Panel::Research) => rsx! {
                                panels::ResearchPanel {
                                    conv: conv.clone(),
                                    on_close: move |_| panel.set(None),
                                }
                            },
                            Some(Panel::Tasks) => rsx! {
                                panels::TasksPanel {
                                    conv: conv.clone(),
                                    on_close: move |_| panel.set(None),
                                }
                            },
                            None => rsx! {},
                        }
                    }
                    // The composer stays live for chats and runs (the human
                    // can steer an implementer); research transcripts are
                    // read-only.
                    if conv.kind != ConversationKind::Research {
                        div { class: "composer",
                            textarea {
                                value: "{input.read()}",
                                placeholder: "Message…",
                                oninput: move |e| input.set(e.value()),
                                onkeydown: move |e| {
                                    if e.key() == Key::Enter {
                                        e.prevent_default();
                                        // Never pass `input.read().…` inline into a
                                        // call that writes `input` — the read guard
                                        // lives to the end of the statement and the
                                        // write panics. `input()` clones + drops.
                                        if !(busy() || status() == ConversationStatus::Streaming)
                                            && let Some(id) = selected()
                                        {
                                            let text = input();
                                            send_message(id, text, input, busy, send_error, data.error);
                                        }
                                    }
                                },
                            }
                            button {
                                disabled: busy() || status() == ConversationStatus::Streaming,
                                onclick: move |_| {
                                    if let Some(id) = selected() {
                                        let text = input();
                                        send_message(id, text, input, busy, send_error, data.error);
                                    }
                                },
                                "Send"
                            }
                        }
                        if let Some(e) = send_error.read().as_ref() {
                            p { class: "error", "{e}" }
                        }
                    }
                } else {
                    div { class: "empty",
                        p { "Pick a conversation or start a new one." }
                        button { onclick: move |_| show_new.set(Some(None)), "New conversation" }
                    }
                }
            }
        }
        if let Some(preset) = show_new() {
            NewConversation {
                preset_project: preset,
                on_done: move |id: Option<ConversationId>| {
                    show_new.set(None);
                    if let Some(id) = id {
                        data.refresh_kind(EntityKind::Conversation);
                        selected.set(Some(id));
                        refresh_view(id, view, status);
                    }
                },
            }
        }
    }
}

#[component]
fn BlockView(block: ContentBlock) -> Element {
    match &block {
        ContentBlock::Text { text } => rsx! { p { class: "text", "{text}" } },
        ContentBlock::Thought { text } => {
            rsx! { p { class: "thought", "{text}" } }
        }
        ContentBlock::ToolCall {
            title,
            tool_kind,
            status,
            ..
        } => rsx! {
            div { class: "tool-card",
                b { "{title}" }
                span { class: "muted", " {tool_kind.clone().unwrap_or_default()} · {status.clone().unwrap_or_default()}" }
            }
        },
        ContentBlock::Plan { raw } => rsx! {
            pre { class: "plan", "{serde_json::to_string_pretty(raw).unwrap_or_default()}" }
        },
    }
}

/// `preset_project`: `Some(id)` when the project group's "+" was clicked,
/// `None` for the organization group.
#[component]
fn NewConversation(
    preset_project: Option<ProjectId>,
    on_done: Callback<Option<ConversationId>>,
) -> Element {
    let data: Data = use_context();
    let mut project_id = use_signal(|| preset_project);
    let mut profile_id = use_signal(|| None::<ModelProfileId>);
    let mut title = use_signal(String::new);
    let mut error = use_signal(|| None::<String>);

    let projects = data.projects.read().clone();
    let profiles = data.profiles.read().clone();

    rsx! {
        div { class: "modal-backdrop",
            div { class: "modal",
                h3 { "New conversation" }
                p { class: "muted",
                    "Chats run as the project coordinator (or the organization coordinator when no project is picked)."
                }
                label { "Project" }
                select {
                    value: "{project_id().map(|p| p.to_string()).unwrap_or_default()}",
                    onchange: move |e| project_id.set(e.value().parse::<ProjectId>().ok()),
                    option { value: "", "— organization —" }
                    for p in projects.iter() {
                        option { key: "{p.id}", value: "{p.id}", "{p.slug}" }
                    }
                }
                label { "Model profile" }
                select {
                    onchange: move |e| profile_id.set(e.value().parse::<ModelProfileId>().ok()),
                    option { value: "", "— agent default —" }
                    for p in profiles.iter() {
                        option { key: "{p.id}", value: "{p.id}", "{p.name}" }
                    }
                }
                label { "Title" }
                input {
                    value: "{title.read()}",
                    oninput: move |e| title.set(e.value()),
                }
                if let Some(e) = error.read().clone() {
                    p { class: "error", "{e}" }
                }
                div { class: "row",
                    button {
                        onclick: move |_| {
                            let pid = project_id();
                            let mpid = profile_id();
                            let t = title.read().clone();
                            spawn(async move {
                                match api::post::<CreateConversation, Conversation>(
                                    "/conversations",
                                    &CreateConversation {
                                        organization_id: None,
                                        project_id: pid,
                                        title: if t.is_empty() { None } else { Some(t) },
                                        model_profile_id: mpid,
                                    },
                                )
                                .await
                                {
                                    Ok(c) => on_done.call(Some(c.id)),
                                    Err(e) => error.set(Some(e)),
                                }
                            });
                        },
                        "Start"
                    }
                    button { onclick: move |_| on_done.call(None), "Close" }
                }
            }
        }
    }
}
