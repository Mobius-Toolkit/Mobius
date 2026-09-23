# AGENTS.md — working conventions for Mobius

## Build / test / lint

```bash
cargo fmt --all --check
cargo clippy --workspace --all-targets -- -D warnings
cargo test --workspace
cargo check -p mobius-ui --target wasm32-unknown-unknown
scripts/mobius.sh ui   # or: cd crates/mobius-ui && dx build --release --debug-symbols false
# requires dioxus-cli 0.7.x; writes target/dx/mobius-ui/release/web/public
# (the ui_dir in mobius.example.toml / mobius.toml). `--debug-symbols false`
# avoids a wasm-opt SIGABRT on rust 1.98 DWARF output.
```

Toolchain: pinned by `rust-toolchain.toml` (channel `1.98.1`, edition 2024,
components rustfmt + clippy, target `wasm32-unknown-unknown`).

## Crate layout

`crates/mobius-{core,api,store,harness,ingest,ingest-github,orchestrator,server,cli,ui}`.
Dependency direction: `core → api/store → harness/ingest(-github) → orchestrator → server`, plus `cli` (REST client) and `ui` (wasm).
`mobius-core` and `mobius-api` must stay wasm-compatible: no tokio, no sqlx,
no `std::process`.

## Conventions

- **Edition 2024**, `cargo fmt`, `cargo clippy -D warnings` clean.
- **No `unwrap`/`expect` in library code** (workspace lint `clippy::unwrap_used = "warn"`); tests may use them.
- **No `todo!()`** — unfinished behavior returns `Err(..::NotImplemented)` or logs and no-ops.
- **Typed ids** everywhere (`OrganizationId`, `TaskId`, …) — newtypes over `Uuid`, serde as plain string, `FromStr`.
- Domain logic lives in `mobius-core` behind async **port traits** (`ports` module); infrastructure implements them.
- SQL migrations in `crates/mobius-store/migrations/` only; domain config is stored in SQLite — `mobius.toml` is infra-only (server bind, data dir, UI dir, logging, GitHub polling).
- **Prefer driving CLIs over hand-rolled REST clients**: GitHub via `gh`, git operations via `git`, agents via their CLIs through ACP. Wrap CLI calls behind a small trait (e.g. `GhRunner`) so tests inject a fake.
- SSE events carry `EventEnvelope { id, at, event }` with monotonic ids; emit `DomainEvent::EntityChanged` on every CRUD mutation.

## ACP smoke test

```bash
cargo run -p mobius-harness --example smoke -- devin --model swe --effort max \
    "Reply with exactly the word PONG"
```

Runs two turns on one session and prints the harness's advertised
`configOptions`. Run it manually — devin only; it is not part of CI.

## Server

```bash
scripts/mobius.sh seed   # dogfood fixture (idempotent)
scripts/mobius.sh        # serve; MOBIUS_CONFIG overrides the config path
```

## CLI smoke (no harness spawn)

Never point a test server at the real `.mobius-data/` — use a temp data dir
and a temp config on a different port:

```bash
DATA=/tmp/mobius-smoke && mkdir -p "$DATA"
sed -e 's|data_dir.*|data_dir = "'"$DATA"'"|' -e 's|127.0.0.1:8787|127.0.0.1:8791|' \
    mobius.example.toml > /tmp/mobius-smoke.toml
cargo build --release -p mobius-server -p mobius-cli
MOBIUS_CONFIG=/tmp/mobius-smoke.toml ./target/release/mobius-server &
./target/release/mobius-server --config /tmp/mobius-smoke.toml seed-dev --repo .
MOBIUS_URL=http://127.0.0.1:8791 ./target/release/mobius project list
MOBIUS_URL=http://127.0.0.1:8791 ./target/release/mobius memory add --kind fact "smoke" --scope org
```

Do not run `mobius task run` or `mobius research start` in the smoke — they
spawn a real Devin harness.
