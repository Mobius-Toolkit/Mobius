# 03 — System architecture

Single self-hosted binary (`mobius-server`) + `mobius` CLI + Dioxus SPA.
SQLite is the source of truth for all state; markdown memory dumps are
derived artifacts.

## Crate dependency graph

```mermaid
graph TD
    core[mobius-core<br/>domain, ports — wasm-safe]
    api[mobius-api<br/>wire DTOs — wasm-safe]
    store[mobius-store<br/>sqlite, migrations, seeds, dumper]
    harness[mobius-harness<br/>ACP sessions — native only]
    ingest[mobius-ingest<br/>SignalSource, registry, manual]
    gh[mobius-ingest-github<br/>gh CLI source]
    orch[mobius-orchestrator<br/>resolver, provisioner, dispatcher,<br/>session-manager, research, worktrees]
    server[mobius-server<br/>axum REST + SSE binary]
    cli[mobius-cli<br/>mobius binary — REST client]
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
    cli --> api
    cli --> core
    ui --> api
    ui --> core
```

`mobius-core` defines async **port traits** (`ports` module) so orchestration
never depends on sqlx. `mobius-store` and the in-memory store implement them;
`Arc<T>` delegates so shared stores satisfy the same bounds.

## Roles and where code runs

```mermaid
flowchart LR
    subgraph Coord["Coordinators (ReadOnly, org memory dir)"]
        OA[org agent] --> PChats[org chats]
        PA[project agents] --> Chats[project chats]
    end
    subgraph Exec["Executors (Auto, worktrees)"]
        Task --> RunConv[Run conversation]
        Q --> ResConv[Research conversation]
    end
    Chats -->|mobius research start| Q[Research]
    Chats -->|mobius task create| Task
    RunConv --> WT[git worktree<br/>mobius/task-*]
    ResConv --> RO[repo checkout, read-only]
```

Coordinators never touch repository working copies (ADR-0010). They act
through the `mobius` CLI (ADR-0009): harness processes get `MOBIUS_URL` plus
`MOBIUS_ORG`/`MOBIUS_PROJECT`/`MOBIUS_CONVERSATION`/`MOBIUS_TASK`/`MOBIUS_RUN`/
`MOBIUS_RESEARCH` in `extra_env`, and the directory containing the `mobius`
binary (`[cli] bin_dir`, default: `current_exe()`'s directory) is prepended to
`PATH`.

## Signal → work flow

```mermaid
flowchart LR
    S[SignalSource<br/>gh poll / manual] --> R[SourceRegistry<br/>cursor + dedupe]
    R -->|insert + SignalIngested| DB[(SQLite)]
    R --> E[EventBus]
    E --> D[Dispatcher]
    D -->|route: project_id /<br/>repository_ids.contains /<br/>keywords+labels| P[Project]
    D -->|TaskCreated| T[Task: Proposed]
    T --> Q[approve → queue → run]
    Q --> WT[WorktreeManager<br/>git worktree add]
    Q --> PB[PromptBuilder<br/>preamble + memory + task]
    PB --> HS[HarnessSession<br/>ACP]
    HS -->|updates| EV[DomainEvents → SSE → UI]
    HS -->|outcome| RUN[Run status + summary]
```

- `Dispatcher::create_task` validates project/repository/parent and creates
  `Proposed` tasks (rejects `Triage`).
- `Dispatcher::start_task` legal-transitions to `Running`, creates a worktree
  (failure → task `Failed`, never falls back to the user's checkout), opens a
  `Run` conversation (`PermissionPolicy::Auto`), and drives it with
  `SessionManager::run_turn` in the background.
- `ResearchService::start` validates repos + local checkouts, defaults an
  empty repository list to project hints or all org repos with `local_path`,
  opens a read-only `Research` conversation (single repo → its checkout, else
  org memory dir), and records findings.

## Request surfaces

- `GET/POST/PUT/DELETE /api/v1/{organizations,repositories,projects,agents,
  model_profiles,harnesses}` — domain config CRUD, 422 on invalid refs.
- `GET/POST /api/v1/tasks`, `GET/PUT /api/v1/tasks/{id}`,
  `POST /api/v1/tasks/{id}/{run,cancel}`, `GET /api/v1/tasks/{id}` →
  `TaskView` (task + runs + children), `GET /api/v1/runs`.
- `GET/POST /api/v1/research`, `GET /api/v1/research/{id}`,
  `POST /api/v1/research/{id}/cancel`.
- `GET/POST /api/v1/memory`, `DELETE /api/v1/memory/{id}`; scope filter
  `organization:<id> | repository:<id> | project:<id>`.
- `GET/POST /api/v1/conversations` (+ `project_id`/`kind` filters),
  `/conversations/{id}/messages|config|cancel`, `/permissions/{id}`.
- `GET /api/v1/signals`, `POST /api/v1/signals/manual`.
- `GET /api/v1/events` — SSE of `EventEnvelope` (`id:` + `Last-Event-ID`
  replay from a 1024-entry ring buffer; `?conversation_id=` filter).

## Process model

- One tokio runtime; axum server.
- Manual source polls at 500 ms; GitHub sources at `[github]
  poll_interval_secs`.
- Each live conversation (chat/run/research) owns a `HarnessSession` — one
  spawned CLI subprocess, one ACP connection, one ACP session.
- `MemoryDumper` runs on startup, debounced 2 s after memory-relevant events,
  and every 5 min.
- Shutdown (`SIGINT`) closes all sessions.
