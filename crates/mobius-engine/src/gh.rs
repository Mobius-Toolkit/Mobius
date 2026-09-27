use axum::Router;
use axum::extract::{Path, State};
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use axum::routing::get;

use crate::{Engine, chat, github};

pub(crate) fn url(engine: &Engine, key: &str) -> String {
    format!("http://127.0.0.1:{}/gh-token/{key}", engine.port)
}

pub fn router(engine: Engine) -> Router {
    Router::new()
        .route("/gh-token/{key}", get(token))
        .with_state(engine)
}

async fn token(State(engine): State<Engine>, Path(key): Path<String>) -> Response {
    let chat_session = engine
        .callers
        .lock()
        .unwrap()
        .get(&key)
        .is_some_and(|caller| caller.role == chat::ROLE);
    if !chat_session {
        return StatusCode::NOT_FOUND.into_response();
    }
    match github::user_token(&engine).await {
        Ok(token) => token.into_response(),
        Err(error) => (StatusCode::INTERNAL_SERVER_ERROR, error.to_string()).into_response(),
    }
}
