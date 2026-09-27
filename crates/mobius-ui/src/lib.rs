use dioxus::prelude::*;
use mobius_api::{devices, github_app, github_manifest, login, logout};
use time::macros::format_description;

#[derive(Clone, PartialEq, Routable)]
#[rustfmt::skip]
pub enum Route {
    #[layout(Shell)]
        #[redirect("/", || Route::Devices {})]
        #[route("/devices")]
        Devices {},
        #[route("/github")]
        GitHub {},
}

#[derive(Clone, Copy)]
struct LoginShown(Signal<bool>);

#[derive(Clone, Copy)]
struct AppSlug(Resource<ServerFnResult<Option<String>>>);

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

#[component]
fn Frame() -> Element {
    let LoginShown(mut login_shown) = use_context();
    let app_slug = use_resource(github_app);
    use_context_provider(|| AppSlug(app_slug));
    use_effect(move || {
        if let Some(Err(error)) = &*app_slug.read()
            && unauthorized(error)
        {
            login_shown.set(true);
        }
    });
    match &*app_slug.read() {
        Some(Ok(Some(_))) => rsx! {
            div { class: "shell",
                nav { class: "rail",
                    div { class: "brand", "Mobius" }
                    div { class: "grow" }
                    Link { class: "navbtn", active_class: "sel", to: Route::GitHub {}, "GitHub" }
                    Link { class: "navbtn", active_class: "sel", to: Route::Devices {}, "Devices" }
                }
                main { class: "center", Outlet::<Route> {} }
                nav { class: "tabs",
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
