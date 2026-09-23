# ADR-0008: Domain config in DB, infrastructure in TOML

## Context

Early sketches put orgs/repos/agents/harnesses in `mobius.toml`. Editing TOML
by hand is hostile for data the UI manages and the API mutates.

## Decision

`mobius.toml` is **infrastructure only**: server bind, data dir, UI dir,
logging, GitHub polling interval + `gh` binary. Everything a user manages —
organizations, repositories, projects, agents, model profiles, harnesses —
lives in SQLite behind CRUD REST with 422 field-level validation, edited
through the UI.

`seed-dev` exists to bootstrap a dogfood fixture idempotently; it is not
config.

## Consequences

Runtime mutation without restarts, uniqueness enforced in SQL, one
authoritative store for both API and UI. The TOML file shrinks to what a
deployer actually needs to touch.
