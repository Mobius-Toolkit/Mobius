# ADR-0005: Separate SPA + REST/SSE, not server-rendered or WebSocket

## Context

The UI needs live updates (message deltas, tool calls, permissions) plus CRUD.

## Decision

Dioxus web SPA (separate crate, wasm) talking REST for commands and SSE for
the event stream. SSE carries `EventEnvelope`s with monotonic `id:`; clients
resume with `Last-Event-ID` against a 1024-entry ring buffer.

Why not WebSocket: traffic is asymmetric — low-frequency client→server calls
(REST handles them) and a high-frequency server→client firehose. SSE gives
browser auto-reconnect, `Last-Event-ID` replay, and is `curl -N`-testable.
The trigger to revisit is an interactive terminal in the UI (binary,
bidirectional, low-latency keystrokes) — not before.

## Consequences

Simple mental model, testable with curl, graceful reconnect for free. Chat
streams and permission prompts are one domain-event firehose filtered by
`?conversation_id=`.
