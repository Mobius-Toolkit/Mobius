# ADR-0003: SQLite memory + derived markdown dumps

## Context

Agent memory must be scoped (org/repo/project), queryable, superseded-aware —
and also grep-able by harness agents working in a checkout.

## Decision

SQLite `memory_entries` is the source of truth. `MemoryDumper` periodically
renders markdown files under `<data_dir>/memory/` grouped by kind, skipping
superseded entries. Future option: mirror into `.mobius/memory/` inside repos.

## Consequences

Structured queries (by scope, unsuperseded, by run) are trivial; markdown is
regenerable at any time and safe to lose. Slight staleness between dump ticks
is acceptable — prompts re-read the DB rows anyway.
