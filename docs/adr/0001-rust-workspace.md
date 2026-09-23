# ADR-0001: Cargo workspace, edition 2024, pinned stable

## Context

Mobius is a single self-hosted binary plus a wasm UI — multiple crates with a
strict layering (pure domain → infra → binary/UI).

## Decision

Cargo workspace under `crates/mobius-*`, edition 2024, `rust-toolchain.toml`
pinning channel `1.98.1` with rustfmt + clippy components and the
`wasm32-unknown-unknown` target. Workspace-level `[workspace.package]`,
`[workspace.dependencies]`, and `[workspace.lints]` (`clippy::unwrap_used =
"warn"`).

## Consequences

One lockfile, one CI matrix, consistent linting. `mobius-core`/`mobius-api`
stay wasm-safe so the UI shares types with the server; native-only concerns
(ACP, sqlx, process spawning) live outside them.
