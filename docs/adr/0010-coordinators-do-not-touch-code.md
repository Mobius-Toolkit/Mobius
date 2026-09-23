# ADR-0010: Coordinators do not touch code

## Context

Project/org coordinators answer questions, plan work, and delegate. If their
sessions run in repository checkouts, an unattended coordinator could mutate
the user's working copy — and chat context would silently depend on
whichever checkout it happened to land in.

## Decision

Coordinator sessions run in `<data_dir>/memory/<org-slug>/` under
`PermissionPolicy::ReadOnly`; their prompts contain no repository local
paths (tested). `ReadOnly` allows `read`-kind tool calls plus `execute`
calls that invoke the `mobius` CLI (detected from the permission request's
`raw_input`/title; shell metacharacters past the `mobius` argv reject the
call) — the CLI is the coordinator's only action surface. Code facts come
from `mobius research start` (a separate read-only `Research` conversation
in a real checkout); physical change comes from `mobius task create` +
`task run`, which executes in a fresh git worktree (`mobius/task-<id8>`)
under `PermissionPolicy::Auto`. Worktree creation failure fails the task —
there is no fallback to the user's checkout.

## Consequences

Coordinators are safe to leave unattended and their context is stable and
auditable. The separation is **soft-enforced**: ReadOnly plus missing local
paths make abuse unlikely, but a determined agent could still request paths
through research findings or call the REST API directly. Hard isolation
(sandboxed coordinator processes) is a future concern. Implementer runs are
the opposite: unattended `Auto` in disposable worktrees, with the work
surfacing as branches for human review.
