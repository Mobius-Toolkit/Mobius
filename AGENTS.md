# Rules for agents

## Writing

- Write all prose in Simplified Technical English (ASD-STE100). This rule applies to docs, code comments, commit messages, pull requests, UI text and error messages.
- Use Conventional Commits for the commit subject.

## Code

- Write only the code that the task needs (YAGNI). Keep the code flat and simple (KISS).
- Do not write a comment that tells what the code does. Write a comment only for a durable fact that the code cannot show: an invariant, an external contract, a unit or a hazard.
- Do not suppress a linter. Do not use `//nolint`, `oxlint-disable`, `eslint-disable`, `@ts-ignore` or `@ts-expect-error`. Fix the finding.
- Use pnpm for the frontend. Do not use npm or yarn.
- Use Oxlint to lint the frontend (`pnpm lint`). Do not add ESLint.
- Put the body of each success response in `Envelope[T]` (`{"data": …}`). Error responses keep the Gork format. Server-sent events have no envelope.

## Generated files

Do not edit these files by hand:

- `api/openapi.json` comes from the Gork handlers in `internal/api`.
- `web/src/api/api.gen.ts` comes from `api/openapi.json`. orval writes it with the settings in `web/orval.config.ts`.
- `internal/store/*.sql.go`, `internal/store/db.go` and `internal/store/models.go` come from `sqlc.yaml`, the migrations and `internal/store/queries.sql`.

After a change to the API or to the queries, run `make generate` and commit the result.

orval does not read `contentSchema` and `itemSchema` of OpenAPI 3.2. Thus `web/orval.config.ts` changes the input of orval before it generates the client. `api/openapi.json` does not change.

- It replaces each schema that has `contentMediaType: application/json` with its `contentSchema`. Thus the generated event types have the decoded data type.
- It removes each operation that has only `text/event-stream` success responses, because the generated function waits for the full body. The components stay, so the event types stay.

To listen to a server-sent event, use `onEvent` from `web/src/lib/events.ts` with the generated event type, for example `onEvent<LiveEvents, 'activity'>(source, 'activity', listener)`. A wrong event name or a wrong data field gives a type error.

## Migrations

`internal/store/migrations/` holds copies of the migration files of the Rust version (`crates/mobius-store/migrations` in Mobius-Toolkit/Mobius). Do not change these files. Add a new migration only as a copy of a new Rust migration.

## Before a push

Run `.mobius/check` (or `make check`). It must pass.

## Layout

```
cmd/mobius/            the server binary (flag -addr, default 127.0.0.1:6363; flag -db, default mobius.db)
cmd/mobius-session/    starts one Claude Code session with the Mobius MCP server (needs claude-agent-acp on PATH)
internal/api/          the Gork routes and handlers
internal/mcp/          the Mobius MCP server and its tools
internal/runner/       starts a Claude Code session through ACP
internal/store/        the SQLite database: goose migrations and sqlc queries
api/openapi.json       the generated OpenAPI spec
web/                   the React frontend (Vite, TypeScript, Tailwind CSS, shadcn/ui)
web/src/api/           the generated API client and types
web/src/components/ui/ the shadcn/ui components
web/src/lib/events.ts  the typed listener of server-sent events (onEvent)
web/embed.go           embeds web/dist into the binary and serves it
.mobius/check          the local check, also used in CI
```

## Commands

- `make dev-api` starts the Go server.
- `make dev-web` starts the Vite dev server. Vite sends `/api` to the Go server.
- `make generate` writes the sqlc code, the spec and the TypeScript client.
- `make build` builds the UI and then `bin/mobius`.
- `make check` runs `.mobius/check`.
