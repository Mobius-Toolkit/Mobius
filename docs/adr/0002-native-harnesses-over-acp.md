# ADR-0002: Native harnesses over ACP, not custom LLM agents

## Context

Mobius needs to run coding work. Options: build an in-house agent loop with
LLM SDKs, or orchestrate existing agent CLIs.

## Decision

Orchestrate through the Agent Client Protocol (`agent-client-protocol` 2.2,
stdio JSON-RPC). Native ACP servers are preferred: `devin acp`,
`opencode acp`. agy (Google Antigravity CLI), Claude and Codex are wired
through community adapter processes
(`agy-acp`, `@zed-industries/claude-code-acp`, `codex-acp`).

## Consequences

Auth, tool ecosystems, and model routing stay upstream; harness upgrades come
free. Sessions are long-lived with streamed updates and a permission request
channel — exactly what chat and unattended runs both need. Cost: ACP is young;
option vocabularies (`configOptions`) are per-harness and handled by fuzzy
matching + verbatim config passthrough (ADR-0007).
