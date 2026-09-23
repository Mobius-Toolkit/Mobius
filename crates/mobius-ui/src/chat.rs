//! Chat page: conversation list, streaming thread, tool-call cards,
//! permission banner, config selectors, new-conversation dialog.

use crate::{Data, api};
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

fn refresh_conversations(mut conversations: Signal<Vec<Conversation>>) {
    spawn(async move {
        if let Ok(v) = api::get::<Vec<Conversation>>("/conversations").await {
            conversations.set(v);
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

#[component]
pub fn ChatPage() -> Element {
    let data: Data = use_context();
    let mut conversations = use_signal(Vec::<Conversation>::new);
    let mut selected = use_signal(|| None::<ConversationId>);
    let mut view = use_signal(|| None::<ConversationView>);
    let mut draft = use_signal(String::new);
    let mut input = use_signal(String::new);
    let mut pending = use_signal(|| None::<PermissionRequest>);
    let mut status = use_signal(|| ConversationStatus::Idle);
    let send_error = use_signal(|| None::<String>);
    let mut show_new = use_signal(|| false);
    let busy = use_signal(|| false);

    use_hook(move || refresh_conversations(conversations));

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
            if let Some(c) = conversations
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

    let conv_view = view.read().clone();
    let conv = conv_view.as_ref().map(|v| v.conversation.clone());

    rsx! {
        div { class: "chat-layout",
            aside { class: "conv-list",
                div { class: "row",
                    h3 { "Conversations" }
                    button { onclick: move |_| show_new.set(true), "+" }
                }
                for c in conversations.read().clone() {
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
                        b { "{c.title}" }
                        span { class: "muted", " {c.status}" }
                    }
                }
            }
            section { class: "chat-main",
                if let Some(conv) = conv.clone() {
                    div { class: "chat-header",
                        b { "{conv.title}" }
                        span { class: "muted", " {status()} · {conv.workdir.display()}" }
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
                    }
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
                } else {
                    div { class: "empty",
                        p { "Pick a conversation or start a new one." }
                        button { onclick: move |_| show_new.set(true), "New conversation" }
                    }
                }
            }
        }
        if show_new() {
            NewConversation {
                on_done: move |id: Option<ConversationId>| {
                    show_new.set(false);
                    if let Some(id) = id {
                        refresh_conversations(conversations);
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

#[component]
fn NewConversation(on_done: Callback<Option<ConversationId>>) -> Element {
    let data: Data = use_context();
    let mut agent_id = use_signal(|| None::<AgentId>);
    let mut workdir = use_signal(String::new);
    let mut title = use_signal(String::new);
    let mut error = use_signal(|| None::<String>);

    let agents = data.agents.read().clone();
    let repos = data.repos.read().clone();
    let profiles = data.profiles.read().clone();

    let resolved = agent_id().and_then(|id| {
        let agent = agents.iter().find(|a| a.id == id)?.clone();
        let pid = agent.profiles.profile_for(Activity::Chat);
        let profile = profiles.iter().find(|p| p.id == pid)?.clone();
        Some(profile)
    });

    rsx! {
        div { class: "modal-backdrop",
            div { class: "modal",
                h3 { "New conversation" }
                label { "Agent" }
                select {
                    onchange: move |e| {
                        let id = e.value().parse::<AgentId>().ok();
                        agent_id.set(id);
                        if let Some(id) = id
                            && let Some(a) = agents.iter().find(|a| a.id == id)
                        {
                            let wd = a.repository_id.and_then(|rid| {
                                repos
                                    .iter()
                                    .find(|r| r.id == rid)
                                    .and_then(|r| r.local_path.clone())
                            });
                            if let Some(wd) = wd {
                                workdir.set(wd.display().to_string());
                            }
                        }
                    },
                    option { value: "", "— pick an agent —" }
                    for a in agents.iter() {
                        option { key: "{a.id}", value: "{a.id}", "{a.name} ({a.role})" }
                    }
                }
                if let Some(profile) = &resolved {
                    p { class: "muted",
                        "chat profile: {profile.name} · harness {data.harness_name(profile.harness_id)}"
                        " · model {profile.model.clone().unwrap_or_else(|| \"-\".into())}"
                        " · effort {profile.effort.map(|e| e.to_string()).unwrap_or_else(|| \"-\".into())}"
                    }
                }
                label { "Workdir" }
                input {
                    value: "{workdir.read()}",
                    oninput: move |e| workdir.set(e.value()),
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
                            let Some(id) = agent_id() else {
                                error.set(Some("pick an agent".into()));
                                return;
                            };
                            let wd = workdir.read().clone();
                            let t = title.read().clone();
                            spawn(async move {
                                match api::post::<CreateConversation, Conversation>(
                                    "/conversations",
                                    &CreateConversation {
                                        agent_id: id,
                                        workdir: wd,
                                        title: if t.is_empty() { None } else { Some(t) },
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
