# 06 — Ingestion

## The contract

```rust
pub trait SignalSource: Send + Sync {
    fn name(&self) -> &str;
    fn poll(&mut self, cursor: Option<SourceCursor>) -> PollFuture<'_>;
    // → Result<(Vec<Signal>, SourceCursor), IngestError>
}
```

A source polls with its previous cursor, returns new signals and the next
cursor. `SourceRegistry` drives the loop: for each source, poll → skip signals
whose `dedupe_key` already exists → insert → emit `SignalIngested` +
`EntityChanged`. Everything downstream (dispatch, SSE, UI) hangs off those
events, so adding a source never touches the dispatcher.

## Why polling (and why `gh`)

Polling keeps ingestion stateless and firewall-friendly — no inbound webhooks,
no public endpoint. And per project convention, Mobius drives CLIs instead of
hand-rolled REST clients: GitHub polling shells out to `gh` (authenticated by
the user's own `gh auth login` — Mobius never stores a token). The `GhRunner`
trait wraps `tokio::process::Command` so tests inject fixture JSON.

The GitHub source runs:

- `gh issue list -R o/r --state all --limit 200 --json … --search "updated:>=<cursor>"`
- `gh pr list -R o/r --state all --limit 200 --json … --search "updated:>=<cursor>"`
- `gh issue view N -R o/r --json comments` for each updated issue

Dedupe keys: `github:{o}/{r}:issue:{n}`, `…:issue:{n}:comment:{id}`,
`…:pr:{n}`, `…:pr:{n}:review:{login}`, `…:issue:{n}:labeled:{label}`.
Cursor = max `updatedAt` seen.

Sources are built at startup for repositories with provider `github` and
`owner != "local"`, and only when `gh --version` succeeds — otherwise the
source is skipped with a warning. The source set is rebuilt when repositories
change (`EntityChanged{repository}`).

## Manual source

`ManualSource` drains a `tokio::mpsc` channel; `POST
/api/v1/signals/manual` pushes into it (fast 500 ms loop so UI input dispatches
immediately).

## Future sources

Sentry webhooks, chat bots, RSS — each gets its own `mobius-ingest-<source>`
crate (ADR-0004) implementing `SignalSource`; nothing else changes.
