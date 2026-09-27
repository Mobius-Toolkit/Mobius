use dioxus::prelude::*;
use mobius_api::{devices, github_app, github_manifest, live, login, logout, workstreams};
use mobius_domain::{FeedRow, Workstream};
use time::macros::format_description;

#[derive(Clone, PartialEq, Routable)]
#[rustfmt::skip]
pub enum Route {
    #[layout(Shell)]
        #[redirect("/", || Route::WorkstreamList {})]
        #[route("/workstreams")]
        WorkstreamList {},
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

#[derive(Clone, Copy)]
struct Feed(Signal<Vec<FeedRow>>);

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

async fn follow_live(
    mut feed: Signal<Vec<FeedRow>>,
    mut workstream_list: Resource<ServerFnResult<Vec<Workstream>>>,
    mut login_shown: Signal<bool>,
) {
    let mut after = None;
    loop {
        match live(after).await {
            Ok(mut events) => {
                while let Some(Ok(row)) = events.recv().await {
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
                    feed.write().push(row);
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
    let Feed(feed) = use_context_provider(|| Feed(Signal::new(Vec::new())));
    use_effect(move || {
        if let Some(Err(error)) = &*app_slug.read()
            && unauthorized(error)
        {
            login_shown.set(true);
        }
    });
    use_effect(move || {
        spawn(follow_live(feed, workstream_list, login_shown));
    });
    match &*app_slug.read() {
        Some(Ok(Some(_))) => rsx! {
            div { class: "shell",
                nav { class: "rail",
                    div { class: "brand", "Mobius" }
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
                div { key: "{workstream.repository}#{workstream.number}", class: "entry",
                    span { class: "grow", "{workstream.title}" }
                    span { class: "muted small", "#{workstream.number}" }
                }
            }
        },
        Some(Err(error)) => rsx! { div { class: "error entry", {error_text(error)} } },
        None => rsx! {},
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
fn Activity() -> Element {
    let Workstreams(workstream_list) = use_context();
    let Feed(feed) = use_context();
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
