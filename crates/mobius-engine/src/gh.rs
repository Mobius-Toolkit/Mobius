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
    let Some(repository) = engine
        .callers
        .lock()
        .unwrap()
        .get(&key)
        .filter(|caller| caller.role == chat::ROLE)
        .map(|caller| caller.repository.clone())
    else {
        return StatusCode::NOT_FOUND.into_response();
    };
    let app_id = match engine.repository(&repository) {
        Ok(repository) => repository.app_id,
        Err(error) => return (StatusCode::INTERNAL_SERVER_ERROR, error).into_response(),
    };
    match github::user_token(&engine, app_id).await {
        Ok(token) => token.into_response(),
        Err(error) => (StatusCode::INTERNAL_SERVER_ERROR, error.to_string()).into_response(),
    }
}
