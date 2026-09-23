//! Typed REST client over `gloo-net`, aligned on `mobius-api` DTOs.

use gloo_net::http::Request;
use mobius_api::ApiError;
use serde::Serialize;
use serde::de::DeserializeOwned;

const BASE: &str = "/api/v1";

async fn check(resp: gloo_net::http::Response) -> Result<gloo_net::http::Response, String> {
    if resp.ok() {
        return Ok(resp);
    }
    let status = resp.status();
    let body: Result<ApiError, _> = resp.json().await;
    match body {
        Ok(e) if !e.fields.is_empty() => Err(format!(
            "{status}: {} ({})",
            e.message,
            e.fields
                .iter()
                .map(|(k, v)| format!("{k}: {v}"))
                .collect::<Vec<_>>()
                .join(", ")
        )),
        Ok(e) => Err(format!("{status}: {}", e.message)),
        Err(_) => Err(format!("{status}")),
    }
}

pub async fn get<T: DeserializeOwned>(path: &str) -> Result<T, String> {
    let resp = check(
        Request::get(&format!("{BASE}{path}"))
            .send()
            .await
            .map_err(|e| e.to_string())?,
    )
    .await?;
    resp.json().await.map_err(|e| e.to_string())
}

pub async fn post<B: Serialize, T: DeserializeOwned>(path: &str, body: &B) -> Result<T, String> {
    let resp = check(
        Request::post(&format!("{BASE}{path}"))
            .json(body)
            .map_err(|e| e.to_string())?
            .send()
            .await
            .map_err(|e| e.to_string())?,
    )
    .await?;
    resp.json().await.map_err(|e| e.to_string())
}

/// POST that returns no meaningful body.
pub async fn post_empty<B: Serialize>(path: &str, body: &B) -> Result<(), String> {
    check(
        Request::post(&format!("{BASE}{path}"))
            .json(body)
            .map_err(|e| e.to_string())?
            .send()
            .await
            .map_err(|e| e.to_string())?,
    )
    .await?;
    Ok(())
}

pub async fn put<B: Serialize, T: DeserializeOwned>(path: &str, body: &B) -> Result<T, String> {
    let resp = check(
        Request::put(&format!("{BASE}{path}"))
            .json(body)
            .map_err(|e| e.to_string())?
            .send()
            .await
            .map_err(|e| e.to_string())?,
    )
    .await?;
    resp.json().await.map_err(|e| e.to_string())
}

pub async fn delete(path: &str) -> Result<(), String> {
    check(
        Request::delete(&format!("{BASE}{path}"))
            .send()
            .await
            .map_err(|e| e.to_string())?,
    )
    .await?;
    Ok(())
}
