# ADR-0006: Long-lived sessions + human-in-the-loop permissions

## Context

Two execution modes — unattended runs and interactive chat — plus a
permission policy axis ending at `ask_human`.

## Decision

`HarnessSession` owns one spawned CLI process, one ACP connection, one ACP
session. `SessionManager` keeps sessions alive per conversation (and the
Dispatcher per run). Under `ask_human`, `session/request_permission` emits
`PermissionRequested` and parks the ACP request on a `oneshot` until the UI
answers via `POST /api/v1/permissions/{id}` — the agent simply waits.

## Consequences

Multi-turn context is free (the agent remembers the conversation), and human
approval is a first-class, persisted entity (`PermissionRequest` row). Cost:
sessions die with the server process (M0 accepts this; `acp_session_id` is
persisted toward resume). Parked permissions block the turn indefinitely —
cancel/close resolves them as cancelled.
