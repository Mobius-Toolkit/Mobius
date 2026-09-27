use std::collections::{HashMap, HashSet};
use std::sync::{Arc, Mutex};

use axum::extract::{Path, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use serde::Deserialize;
use serde_json::json;
use tokio::net::TcpListener;

pub const APP_ID: i64 = 7;
pub const APP_SLUG: &str = "mobius-test";
pub const APP_PRIVATE_KEY: &str =
    "-----BEGIN RSA PRIVATE KEY-----\nfake\n-----END RSA PRIVATE KEY-----\n";
pub const APP_CLIENT_ID: &str = "Iv23test";
pub const APP_CLIENT_SECRET: &str = "client-secret";

#[derive(Default)]
struct Records {
    account_types: HashMap<String, &'static str>,
    manifest_codes: HashSet<String>,
    user_codes: HashMap<String, String>,
    user_tokens: HashMap<String, String>,
    tokens_given: u32,
}

type Shared = Arc<Mutex<Records>>;

pub struct FakeGitHub {
    pub url: String,
    state: Shared,
}

impl FakeGitHub {
    pub async fn start() -> FakeGitHub {
        let state = Shared::default();
        let router = Router::new()
            .route("/users/{name}", get(account))
            .route("/app-manifests/{code}/conversions", post(convert_manifest))
            .route("/login/oauth/access_token", post(exchange_code))
            .route("/user", get(user))
            .with_state(state.clone());
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!("http://{}", listener.local_addr().unwrap());
        tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        FakeGitHub { url, state }
    }

    pub fn add_account(&self, login: &str, account_type: &'static str) {
        self.state
            .lock()
            .unwrap()
            .account_types
            .insert(login.to_string(), account_type);
    }

    pub fn add_manifest_code(&self, code: &str) {
        self.state
            .lock()
            .unwrap()
            .manifest_codes
            .insert(code.to_string());
    }

    pub fn add_user_code(&self, code: &str, login: &str) {
        self.state
            .lock()
            .unwrap()
            .user_codes
            .insert(code.to_string(), login.to_string());
    }
}

fn not_found() -> Response {
    (
        StatusCode::NOT_FOUND,
        Json(json!({ "message": "Not Found" })),
    )
        .into_response()
}

async fn account(State(state): State<Shared>, Path(name): Path<String>) -> Response {
    match state.lock().unwrap().account_types.get(&name) {
        Some(account_type) => Json(json!({ "login": name, "type": account_type })).into_response(),
        None => not_found(),
    }
}

async fn convert_manifest(State(state): State<Shared>, Path(code): Path<String>) -> Response {
    if !state.lock().unwrap().manifest_codes.remove(&code) {
        return not_found();
    }
    (
        StatusCode::CREATED,
        Json(json!({
            "id": APP_ID,
            "slug": APP_SLUG,
            "pem": APP_PRIVATE_KEY,
            "client_id": APP_CLIENT_ID,
            "client_secret": APP_CLIENT_SECRET
        })),
    )
        .into_response()
}

#[derive(Deserialize)]
struct CodeExchange {
    client_id: String,
    client_secret: String,
    code: String,
}

async fn exchange_code(
    State(state): State<Shared>,
    Json(exchange): Json<CodeExchange>,
) -> Response {
    let mut records = state.lock().unwrap();
    if exchange.client_id != APP_CLIENT_ID || exchange.client_secret != APP_CLIENT_SECRET {
        return Json(json!({
            "error": "incorrect_client_credentials",
            "error_description": "The client_id and/or client_secret passed are incorrect."
        }))
        .into_response();
    }
    let Some(login) = records.user_codes.remove(&exchange.code) else {
        return Json(json!({
            "error": "bad_verification_code",
            "error_description": "The code passed is incorrect or expired."
        }))
        .into_response();
    };
    records.tokens_given += 1;
    let number = records.tokens_given;
    records.user_tokens.insert(format!("ghu_{number}"), login);
    Json(json!({
        "access_token": format!("ghu_{number}"),
        "expires_in": 28800,
        "refresh_token": format!("ghr_{number}"),
        "refresh_token_expires_in": 15638400,
        "scope": "",
        "token_type": "bearer"
    }))
    .into_response()
}

async fn user(State(state): State<Shared>, headers: HeaderMap) -> Response {
    let token = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.strip_prefix("Bearer "))
        .unwrap_or_default();
    match state.lock().unwrap().user_tokens.get(token) {
        Some(login) => Json(json!({ "login": login })).into_response(),
        None => (
            StatusCode::UNAUTHORIZED,
            Json(json!({ "message": "Bad credentials" })),
        )
            .into_response(),
    }
}
