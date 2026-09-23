# 09 — Model profiles

A **ModelProfile** decouples "which model/effort" from "which agent":

```text
Agent.profiles = ActivityProfiles {
    default:  ModelProfileId,
    overrides: { Activity → ModelProfileId },   // e.g. plan→swe2-max, research→swe2-low
}
```

`Activity = plan | implement | research | review | triage | housekeeping |
chat` (mapped from `TaskKind`). `ProfileResolver::resolve(store, agent,
activity)` returns the concrete `(ModelProfile, Harness)`; a dangling profile
id or a disabled harness is an error, not a silent fallback.

## Application order in `HarnessSession::spawn`

1. `model_arg_template` — launch args (e.g. `devin acp --model swe`) when
   `profile.model` is set.
2. `session/new` → capture advertised `configOptions`.
3. `model` category → fuzzy match: case-insensitive substring of option
   name or value.
4. Effort → `thought_level` category option, else any option whose id or name
   contains "effort" (the agy adapter's `reasoningEffort`). `max` prefers
   names containing `max`/`xhigh`/`ultra`; other efforts pick the nearest
   ordinal position among the option list.
5. `profile.config` entries → `session/set_config_option` verbatim.

Every miss logs a warning and continues — profiles degrade gracefully when a
harness doesn't advertise what was asked.

## What `devin acp` actually advertises

From a live smoke run (`devin 3000.11.1`, `--model swe --effort max`):

| Option id | Category | Values observed |
|---|---|---|
| `mode` | `mode` | `accept-edits`, `smart`, `ask`, `plan`, `bypass` |
| `model` | `model` | `swe-1-6`, `swe-1-7-medium`, `swe-1-7-lightning-medium`, `swe-2-high`, `claude-*`, `gpt-*`, `gemini-*`, `glm-*`, `deepseek-*`, `fusion-*`, … (~60 options) |
| `thought_level` | `thought_level` | `medium`, `max` only |

Consequences for devin:

- "Effort" is split between two places: the `thought_level` option (only
  `medium`/`max`) and the *model value suffix* (`…-medium`, `…-high`,
  `…-low`, `…-none`). Our `--effort max` set `thought_level=max`; an
  effort-encoded model like `swe-2-high` requires naming it in
  `profile.model` (fuzzy substring match, e.g. `swe-2` → `swe-2-high`).
- `--model swe` matched `swe-1-7-lightning-medium` — the *first* value
  containing "swe" in devin's list order, not the newest swe-2. Name the
  desired model more precisely in `profile.model` when that matters. The
  `seed-dev` fixture therefore pins `swe-2-high` (for `swe2-max`/`swe2-high`)
  and `swe-2-medium` (for `swe2-low`); with only `medium`/`max` thought
  levels, `swe2-high` and `swe2-max` differ in name only until devin adds
  more levels.
- `mode` is how devin expresses permission posture; devin auto-decides
  permission internally (`Permission decision … auto-decided Allow`) and in
  the modes observed never emits `session/request_permission`. The AskHuman
  park/resume path exists but needs a harness that actually requests.

The smoke example demonstrates two turns on one session to prove sessions are
long-lived and profile application happens once at `session/new`.
