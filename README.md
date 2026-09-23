# Mobius

A self-hosted **agent swarm** for solopreneurs and tiny teams. Mobius
orchestrates existing coding-agent harnesses (Devin CLI, OpenCode,
and Antigravity `agy`, Claude Code, Codex via ACP adapters) through the
[Agent Client Protocol](https://agentclientprotocol.com) instead of building
custom LLM agents.

Humans define **Projects** — a feature/domain scope inside an
**Organization** (with repository routing hints) — each coordinated by a
long-lived **Project Agent**. Coordinators answer questions in chat, run
read-only **Research** investigations over repo checkouts, and delegate
physical change as **Tasks** executed as **Runs** in dedicated git
worktrees. A three-level **Memory** (Organization → Repository → Project)
lives in SQLite and is dumped as markdown on startup, on events, and every
five minutes. Humans talk to coordinators through a built-in chat UI with
streaming, tool-call cards, permission approvals, model/effort switching,
and Memory/Research/Tasks side panels.

## Motivation

The goal is an **affordable autonomous software factory** — the kind of
agent-swarm setup that usually needs a platform team, scaled down to what one
person can run and pay for:

- **Reuse what you already pay for.** Mobius drives the harnesses you already
  have (Devin, Claude Code, Codex, OpenCode, Antigravity) over ACP instead of
  billing a second set of API keys or reimplementing agents on top of a model
  router. Model and effort are configurable per activity, so planning can use
  the strongest model while research and housekeeping use the cheapest.
- **Built for one.** Solo developers and tiny teams ship several projects at
  once and lose most of their time to context switching — triage, review,
  small fixes, chores. Mobius gives each project a long-lived agent that holds
  that context and keeps working between your sessions.
- **Own the loop.** Self-hosted, SQLite as the source of truth, memory dumped
  as markdown you can read and grep, and every signal source (GitHub first)
  pluggable behind a trait.

Mobius is being built for my own projects first — [makeadir.com](https://makeadir.com)
is the first production tenant — and it develops itself: with the M1 scoped
foundation, work on Mobius happens through Mobius.

## Quick start

```bash
# Prerequisites: rust 1.98.1 (see rust-toolchain.toml), devin CLI, gh CLI,
# and the dioxus CLI (cargo install dioxus-cli --version 0.7.10; make sure
# ~/.cargo/bin is on PATH — with a Homebrew rustup it isn't by default).

# 1. Seed the dogfooding fixture (org/repo/project/profiles/agent for Mobius itself)
scripts/mobius.sh seed

# 2. Build the UI (if needed) and serve — creates mobius.toml from
#    mobius.example.toml on first run; MOBIUS_CONFIG overrides the path.
scripts/mobius.sh            # add --build-ui to force a UI rebuild

# 3. Open the UI at http://127.0.0.1:8787 (`dx serve` works for UI dev with
#    hot reload; the API base is same-origin /api/v1.)
#
# The `ui` step runs `dx build --release --debug-symbols false` —
# `--debug-symbols false` is required: rust 1.98 emits DWARF that dx's
# bundled wasm-opt (binaryen) aborts on; without the flag dx silently ships
# the unoptimized 2x-larger wasm.
```

ACP smoke test (verifies your harnesses without the server):

```bash
cargo run -p mobius-harness --example smoke -- devin --model swe --effort max \
    "Reply with exactly the word PONG"
```

## `mobius` CLI

`scripts/mobius.sh` builds `target/release/mobius` next to
`mobius-server` — the server prepends that directory to every harness's
`PATH`, so agents inside sessions can call back into Mobius (and you can
too). `--url`/`MOBIUS_URL` point it at a server; `--json` prints raw
responses; `MOBIUS_ORG`/`MOBIUS_PROJECT`/`MOBIUS_CONVERSATION` supply
scope defaults (the server sets them for harness processes).

```bash
mobius project list | mobius repo list
mobius task create --title "…" --brief "…" --repo owner/name --project SLUG
mobius task run ID | mobius task list [--project SLUG] [--status S]
mobius research start --question "…" [--repo owner/name]… [--wait]
mobius memory add --kind fact|decision|convention|gotcha "…" [--scope project|org|repo:owner/name]
mobius memory list [--scope …]
```

## Crate map

| Crate | Purpose |
|---|---|
| `mobius-core` | Pure domain: typed ids, entities, state machines, events, port traits. Wasm-safe, no IO. |
| `mobius-api` | Wire DTOs shared by server and UI. Wasm-safe. |
| `mobius-store` | SQLite via sqlx, migrations, `MemoryDumper`, seed fixtures. |
| `mobius-harness` | ACP session layer (`HarnessSession`), config-option matching, permission routing. |
| `mobius-ingest` | `SignalSource` contract, polling `SourceRegistry`, `ManualSource`. |
| `mobius-ingest-github` | GitHub polling via the `gh` CLI. |
| `mobius-orchestrator` | `ProfileResolver`, `AgentProvisioner`, `Dispatcher` (signal→task→run), `SessionManager` (chats + `run_turn`), `ResearchService`, `WorktreeManager`. |
| `mobius-server` | axum REST + SSE binary, `mobius.toml` infra config, `seed-dev`. |
| `mobius-cli` | `mobius` binary — agent/human CLI talking to the REST API. |
| `mobius-ui` | Dioxus 0.7 web SPA. |

## Documentation

- `docs/architecture/` — vision, domain model, system architecture, memory,
  harnesses/ACP, ingestion, GitHub workflow, human chat, model profiles,
  delegation CLI.
- `docs/adr/` — architecture decision records.
- `docs/ROADMAP.md` — milestone plan.

## License

MIT OR Apache-2.0.
