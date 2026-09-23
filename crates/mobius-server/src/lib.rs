//! `mobius-server` — the self-hosted Mobius binary: REST + SSE API, signal
//! ingestion loop, memory dumps, and static serving of the built UI.

pub mod config;
pub mod events;
pub mod routes;
pub mod state;
