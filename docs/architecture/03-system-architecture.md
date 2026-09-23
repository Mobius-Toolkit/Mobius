# 03 — System architecture

Single self-hosted binary (`mobius-server`) + Dioxus SPA. SQLite is the source
of truth for all state; markdown memory dumps are derived artifacts.

## Crate dependency graph

```mermaid
graph TD
    core[mobius-core<br/>domain, ports — wasm-safe]
    api[mobius-api<br/>wire DTOs — wasm-safe]
    store[mobius-store<br/>sqlite, migrations, seeds, dumper]
    harness[mobius-harness<br/>ACP sessions — native only]
    ingest[mobius-ingest<br/>SignalSource, registry, manual]
    gh[mobius-ingest-github<br/>gh CLI source]
    orch[mobius-orchestrator<br/>resolver, dispatcher, session-manager, worktrees]
    server[mobius-server<br/>axum REST + SSE binary]
    ui[mobius-ui<br/>Dioxus SPA — wasm]

    api --> core
    store --> core
    harness --> core
    ingest --> core
    gh --> ingest
    gh --> core
    orch --> core
    orch --> harness
    server --> api
    server --> store
    server --> orch
    server --> ingest
    server --> gh
    ui --> api
    ui --> core
```

`mobius-core` defines async **port traits** (`ports` module) so orchestration
never depends on sqlx. `mobius-store` and the in-memory store implement them;
`Arc<T>` delegates so shared stores satisfy the same bounds.

## Signal → work flow

```mermaid
flowchart LR
    S[SignalSource<br/>gh poll / manual] --> R[SourceRegistry<br/>cursor + dedupe]
    R -->|insert + SignalIngested| DB[(SQLite)]
    R --> E[EventBus]
    E --> D[Dispatcher]
    D -->|route by scope keywords/labels| P[Project]
    D -->|TaskCreated| T[Task: Proposed]
    T --> Q[approve → queue → run_task]
    Q --> WT[WorktreeManager<br/>git worktree add]
    Q --> PB[PromptBuilder<br/>instructions + memory + task]
    PB --> HS[HarnessSession<br/>ACP]
    HS -->|updates| EV[DomainEvents → SSE → UI]
    HS -->|outcome| RUN[Run status]
    RUN --> MEM[MemoryEntry rows<br/>→ markdown dumps]
```

## Request surfaces

- `GET/POST/PUT/DELETE /api/v1/{organizations,repositories,projects,agents,
  model_profiles,harnesses}` — domain config CRUD, 422 on invalid refs.
- `GET /api/v1/{tasks,runs,signals,memory,permissions}` — read-only.
- `POST /api/v1/signals/manual` — human input into ingestion.
- `GET /api/v1/events` — SSE of `EventEnvelope` (`id:` + `Last-Event-ID`
  replay from a 1024-entry ring buffer; `?conversation_id=` filter).
- Chat routes under `/api/v1/conversations/…` and `/api/v1/permissions/…`.

## Process model

- One tokio runtime; axum server.
- Manual source polls at 500 ms; GitHub sources at `[github]
  poll_interval_secs`.
- Each live conversation/run owns a `HarnessSession` (one spawned CLI
  subprocess, one ACP connection, one ACP session).
- Shutdown (`SIGINT`) closes all sessions.
