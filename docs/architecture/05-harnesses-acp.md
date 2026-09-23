# 05 — Harnesses and ACP

## Why ACP

Mobius orchestrates existing agent CLIs rather than embedding LLM clients.
The [Agent Client Protocol](https://agentclientprotocol.com) (JSON-RPC over
stdio, the same protocol Zed uses) gives one integration surface for all of
them: `initialize`, `session/new`, `session/prompt`, streamed
`session/update` notifications (text, thoughts, tool calls, plans), and
`session/request_permission` for human-in-the-loop approval.

## Harnesses are config, not code

A `Harness` row is `{ name, command, args, env, default_permission_policy,
model_arg_template, enabled }`. Defaults seeded on startup:

| Name | Command | Notes |
|---|---|---|
| `devin` | `devin acp` | Native ACP. First-class. `model_arg_template = ["--model", "{model}"]`. |
| `agy` | `npx -y agy-acp` | Adapter — Antigravity CLI has no native ACP (google-antigravity/antigravity-cli#31). Exposes `model` and `reasoningEffort` config options. |
| `opencode` | `opencode acp` | Native ACP. |
| `claude` | `npx -y @zed-industries/claude-code-acp` | Adapter — Claude Code has no native ACP. |
| `codex` | `codex-acp` | Adapter package required. |

## HarnessSession lifecycle

```
spawn(harness, cwd, policy, profile)
  → apply model_arg_template (launch args) when profile.model set
  → spawn process, initialize, session/new
  → capture advertised configOptions
  → apply_profile: model → fuzzy match on `model` category options,
    effort → `thought_level` options (max→"max/xhigh/ultra", else nearest
    ordinal), raw config entries verbatim
  → unmatched options: warn, never fail
```

API: `prompt`, `cancel`, `updates() -> broadcast::Receiver<SessionUpdate>`,
`respond_permission(request_id, option_id)`, `set_config_option`,
`config_options`, `close`.

## Permission policy mapping

| Policy | Behavior on `session/request_permission` |
|---|---|
| `auto`, `workspace_edits` | First allow option, immediately. |
| `read_only` | Allow only read-kind tool calls; reject otherwise. |
| `ask_human` | Emit `PermissionRequested`, park the ACP request on a oneshot until `respond_permission` / `cancel` / `close`. |

## Known gaps

- Claude Code and Codex reach ACP only through community adapters
  (`@zed-industries/claude-code-acp`, `codex-acp`); quality/features are
  adapter-bound.
- `configOptions` vocabulary is per-harness; the fuzzy matchers handle the
  `model` / `thought_level` categories and degrade gracefully elsewhere.
- Sessions are process-lifetime: server restart drops live sessions (persisted
  `acp_session_id` is stored for a future resume).
