# Roadmap

## M1 — Scoped swarm foundation (this milestone)
- Org-scoped domain: organizations own projects (feature/domain-level with
  `repository_ids` routing hints) and agents (`Organization`/`Project`/
  `Reviewer`/`Housekeeping` roles).
- Coordinator chats: every chat is a fresh ACP session with a preamble
  (role + org + catalogue + grouped memory + CLI docs); coordinators run
  ReadOnly in the org memory dir and never see local paths (ADR-0010).
- `Research` entity: read-only investigations over repo checkouts, findings
  extracted from `## Findings`.
- Tasks run in fresh worktrees (`mobius/task-*`) as `Run` conversations;
  parent hierarchy; `Feature|Fix|Spec|Refactor|Review|Housekeeping|Triage`.
- `mobius` CLI: agent + human surface for project/repo/task/research/memory
  (ADR-0009); `MOBIUS_*` env + PATH injection into harness processes.
- Memory: explicit writes only (no extraction); per-org markdown layout
  (`organization.md`, `projects.md`, `repos/`, `projects/`) dumped on
  startup, on events (debounced), and every 5 min.
- REST API for tasks/runs/research/memory/conversations; UI with grouped
  sidebar, Memory/Research/Tasks panels, Work page, live SSE refresh.

## Next
- Memory extraction: runs/conversations distill learnings into
  `MemoryEntry` rows at the right scope (currently explicit-only).
- MCP wrapper exposing `mobius` actions as tools for harnesses that prefer
  MCP over a shell CLI.
- Organization agent acting beyond research (approve tasks, open PRs,
  reconcile states) once coordinator trust is proven.
- Session/load hardening: resume across restarts (persisted
  `acp_session_id`s), run queue limits, cancellation races.

## Later
- More signal sources (Sentry, chat platforms, webhooks) on the same
  `SignalSource` contract.
- Reviewer agent loop on GitHub PRs.
- `.mobius/memory/` mirroring into repos (see 04-memory).
- WebSocket transport if an interactive terminal is needed (ADR-0005).
