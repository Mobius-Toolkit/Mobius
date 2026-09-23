# ADR-0007: Model profiles per activity via ACP config options

## Context

Different activities deserve different models/effort (planning wants max,
research wants cheap) without duplicating agents.

## Decision

`ModelProfile{name, harness_id, model?, effort?, config}` +
`Agent.profiles: ActivityProfiles{default, overrides}` resolved per activity
by `ProfileResolver`. Profiles apply at session start through:

1. `model_arg_template` launch args (`devin acp --model swe`),
2. fuzzy matching of `model`/`thought_level` `configOptions` (names/values,
   ordinal effort mapping),
3. verbatim `session/set_config_option` for raw `config` entries.

Misses warn, never fail — harness vocabularies differ and evolve.

## Consequences

One agent definition serves all activities; harness-specific knobs remain
reachable through raw config without schema churn in Mobius. The fuzzy
matching is heuristic — documented in `config_options.rs` tests.
