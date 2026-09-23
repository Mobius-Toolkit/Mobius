# ADR-0004: Per-source ingestion crates, CLIs over REST clients

## Context

Signals arrive from GitHub today; Sentry/chat later. Webhooks would require a
public endpoint — wrong for a self-hosted tool.

## Decision

- Polling `SignalSource` contract in `mobius-ingest`; one crate per source
  (`mobius-ingest-github`, future `mobius-ingest-sentry`, …).
- Sources drive CLIs, not REST SDKs: GitHub polling runs `gh issue list`,
  `gh pr list`, `gh issue view` through a `GhRunner` trait
  (`tokio::process::Command` impl + test fake). Auth = the user's own
  `gh auth login`; Mobius never stores tokens.
- Cursors are opaque `serde_json` blobs persisted by the caller; dedupe via
  `Signal.dedupe_key`.

## Consequences

Zero inbound surface area, no token handling, `gh` semantics (search,
pagination) come for free. Per-source crates keep a heavy optional dependency
(or CLI) out of the core graph. Trade-off: polling latency and `gh` CLI
availability become runtime requirements, both handled with a warning + skip.
