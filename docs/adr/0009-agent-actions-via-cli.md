# ADR-0009: Agent actions via the `mobius` CLI

## Context

Coordinators need to start research, create/run tasks, and write memory.
Options: bespoke ACP tool calls (extra protocol surface per harness), an MCP
server (another moving part, per-harness config), or a CLI agents shell out
to like humans do.

## Decision

Ship a `mobius` binary (`mobius-cli` crate, thin REST client) and put it on
the `PATH` of every harness process with `MOBIUS_*` ambient scope env vars.
Coordinator preambles document the CLI verbatim (`cli_section`), so agent
documentation is generated next to the code that ships it.

## Consequences

One surface to test and version; agents, humans, and scripts share it.
Provenance (`origin_conversation_id`, `source_conversation_id`) rides on
`MOBIUS_CONVERSATION` automatically. The CLI must sit next to the server
binary (`scripts/mobius.sh` builds both) or be configured via `[cli]
bin_dir`. Agents that ignore the preamble can still hit the REST API
directly — the CLI is convenience, not a security boundary (the real
boundary is ADR-0010's worktree + permission policy).
