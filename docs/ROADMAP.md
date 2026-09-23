# Roadmap

## M0 — Foundation (this milestone)
- Cargo workspace: core domain, api DTOs, sqlite store, ACP harness layer,
  ingestion (manual + `gh` polling), orchestrator, REST/SSE server, Dioxus UI.
- Human chat end-to-end: streaming, tool calls, AskHuman permissions,
  config-option selectors.
- CRUD for all domain config with 422 validation; `seed-dev` dogfood fixture.

## M1 — Devin vertical slice
GitHub issue → routed signal → task → run in a worktree → branch → PR opened
via `gh`. Housekeeping agent reconciles task states.

## M2 — Reviewer agent
PR review requests spawn review runs; results posted back as PR comments.

## M3 — Memory extraction
Runs write `MemoryEntry` rows (facts/decisions/gotchas) at the right scope;
markdown dumps feed future prompts; supersession supported.

## M4 — More signal sources
Sentry, chat platforms, webhooks; the `SignalSource` contract stays the same.

## M5 — Human approval UI hardening
Richer permission payloads, run-level approvals surfaced in the UI, audit log.

## Later
- WebSocket transport if an interactive terminal is needed (see ADR-0005).
- `.mobius/memory/` mirroring into repos (see 04-memory).
- Session resume across server restarts (persisted ACP session ids).
