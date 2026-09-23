# 10 — Delegation via the `mobius` CLI

Agents don't get bespoke RPC or tool schemas — they act on Mobius the same
way a human operator does: by shelling out to the `mobius` CLI
(ADR-0009). The same binary serves humans in a terminal, coordinators in
chats, implementers in runs, and researchers in research conversations.

## How agents reach it

`SessionManager` injects into every harness process:

- `MOBIUS_URL` — the API base (`0.0.0.0` binds normalize to `127.0.0.1`).
- `MOBIUS_ORG`, `MOBIUS_PROJECT` — ambient scope slugs.
- `MOBIUS_CONVERSATION` — the current conversation id; becomes
  `origin_conversation_id` on tasks/research and `source_conversation_id` on
  memory entries, so provenance is automatic.
- `MOBIUS_TASK` / `MOBIUS_RUN` / `MOBIUS_RESEARCH` — set on the matching
  conversation kinds.
- `PATH` is prefixed with the directory containing the `mobius` binary —
  `[cli] bin_dir` in `mobius.toml`, defaulting to the directory of the
  running `mobius-server` executable (which is why `scripts/mobius.sh`
  builds `mobius` next to it).

Coordinators see the CLI surface documented in their preamble
(`prompt.rs::cli_section`), so the tool documentation and the shipped flags
can never drift apart silently — the section is generated next to the code
and updated with it.

## Command surface

```text
mobius project list
mobius repo list

mobius research start --question .. [--repo owner/name]... [--project SLUG] [--wait [--timeout 600]]
mobius research show|list [--mine]|cancel

mobius task create --title T (--brief TEXT | --brief-file F)
                   --repo owner/name [--project SLUG] [--parent ID]
                   [--kind feature|fix|spec|refactor|review|housekeeping]
                   [--priority low|normal|high|urgent]
mobius task run|show|list [--project] [--status] [--mine]|cancel

mobius run show ID

mobius memory add --kind fact|decision|convention|gotcha CONTENT
                  [--scope project|org|repo:owner/name]
mobius memory list [--scope ...]
```

Global flags: `--url` / `MOBIUS_URL` (default `http://127.0.0.1:8787`),
`--json` for raw responses. Errors exit non-zero with the API `message`;
`research start --wait` times out with exit 2.

Defaults chosen for agents:

- `research start` with no `--repo` uses the project's hinted repositories,
  or all org repositories with a local checkout for org-level research.
- `memory add` scope defaults to the ambient project, else the org;
  `repo:owner/name` is a single token (no split arguments to fumble).
- `--mine` filters lists to entities that originated from the current
  conversation.

## What agents deliberately cannot do through it

There is no CLI command to mutate a working copy, force-push, delete memory
without an id, or bypass task validation — physical change happens only via
`task create` + `task run`, which always executes in a fresh worktree.
