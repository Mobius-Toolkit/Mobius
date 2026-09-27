use dioxus::fullstack::{Redirect, SetCookie, SetHeader};
use dioxus::prelude::*;
use mobius_domain::{Devices, ManifestForm};

#[cfg(feature = "server")]
use dioxus::fullstack::headers::UserAgent;
#[cfg(feature = "server")]
use dioxus::fullstack::{Cookie, TypedHeader};
#[cfg(feature = "server")]
use dioxus::server::axum::Extension;
#[cfg(feature = "server")]
use dioxus::server::axum::extract::{FromRequestParts, Query};
#[cfg(feature = "server")]
use dioxus::server::http::request::Parts;
#[cfg(feature = "server")]
use mobius_engine::{Engine, auth, github};
#[cfg(feature = "server")]
use mobius_store::Store;
#[cfg(feature = "server")]
use serde::Deserialize;

#[cfg(feature = "server")]
const SESSION_MAX_AGE_SECONDS: u32 = 400 * 24 * 60 * 60;

#[cfg(feature = "server")]
pub struct DeviceId(pub i64);

#[cfg(feature = "server")]
impl<S: Send + Sync> FromRequestParts<S> for DeviceId {
    type Rejection = StatusCode;

    async fn from_request_parts(parts: &mut Parts, state: &S) -> Result<Self, StatusCode> {
        let TypedHeader(cookie) = TypedHeader::<Cookie>::from_request_parts(parts, state)
            .await
            .map_err(|_| StatusCode::UNAUTHORIZED)?;
        let token = cookie
            .get("mobius_session")
            .ok_or(StatusCode::UNAUTHORIZED)?;
        let engine = parts
            .extensions
            .get::<Engine>()
            .ok_or(StatusCode::INTERNAL_SERVER_ERROR)?;
        match auth::check(engine, token).await {
            Ok(Some(device)) => Ok(DeviceId(device)),
            Ok(None) => Err(StatusCode::UNAUTHORIZED),
            Err(_) => Err(StatusCode::INTERNAL_SERVER_ERROR),
        }
    }
}

#[cfg(feature = "server")]
#[derive(Deserialize)]
pub struct Callback {
    code: String,
}

#[post("/api/login", engine: Extension<Engine>, user_agent: TypedHeader<UserAgent>)]
pub async fn login(password: String) -> ServerFnResult<SetHeader<SetCookie>> {
    let Some(token) = auth::login(&engine, &password, user_agent.as_str())
        .await
        .map_err(ServerFnError::new)?
    else {
        return Err(
            HttpError::new(StatusCode::UNAUTHORIZED, "The access password is wrong.").into(),
        );
    };
    SetHeader::new(format!(
        "mobius_session={token}; HttpOnly; Path=/; SameSite=Lax; Max-Age={SESSION_MAX_AGE_SECONDS}"
    ))
    .map_err(ServerFnError::new)
}

#[get("/api/devices", device: DeviceId, store: Extension<Store>)]
pub async fn devices() -> ServerFnResult<Devices> {
    Ok(Devices {
        this_device: device.0,
        logins: store
            .device_logins()
            .list()
            .await
            .map_err(ServerFnError::new)?,
    })
}

#[post("/api/devices/logout", _device: DeviceId, engine: Extension<Engine>)]
pub async fn logout(id: i64) -> ServerFnResult<()> {
    auth::logout(&engine, id)
        .await
        .map_err(ServerFnError::new)?;
    Ok(())
}

#[get("/api/github/app", _device: DeviceId, store: Extension<Store>)]
pub async fn github_app() -> ServerFnResult<Option<String>> {
    Ok(store
        .github_app()
        .get()
        .await
        .map_err(ServerFnError::new)?
        .map(|app| app.slug))
}

#[post("/api/github/manifest", _device: DeviceId, engine: Extension<Engine>)]
pub async fn github_manifest(account: String, origin: String) -> ServerFnResult<ManifestForm> {
    github::manifest_form(&engine, &account, &origin)
        .await
        .map_err(ServerFnError::new)
}

// The query is an extractor after `DeviceId`, so a request with no cookie gets 401 before the query is parsed.
#[get("/api/github/manifest-callback", _device: DeviceId, query: Query<Callback>, engine: Extension<Engine>)]
pub async fn github_manifest_callback() -> ServerFnResult<Redirect> {
    github::convert_manifest(&engine, &query.code)
        .await
        .map_err(ServerFnError::new)?;
    Ok(Redirect::to("/github"))
}

#[get("/api/github/user-callback", _device: DeviceId, query: Query<Callback>, engine: Extension<Engine>)]
pub async fn github_user_callback() -> ServerFnResult<Redirect> {
    if !github::authorize_user(&engine, &query.code)
        .await
        .map_err(ServerFnError::new)?
    {
        return Err(HttpError::new(
            StatusCode::FORBIDDEN,
            "The GitHub login is not a trusted user.",
        )
        .into());
    }
    Ok(Redirect::to("/github"))
}
