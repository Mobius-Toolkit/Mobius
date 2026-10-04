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

## Generated files

Do not edit these files by hand:

- `api/openapi.json` comes from the Gork handlers in `internal/api`.
- `web/src/api/schema.d.ts` comes from `api/openapi.json`.

After a change to the API, run `make generate` and commit the result.

## Before a push

Run `.mobius/check` (or `make check`). It must pass.

## Layout

```
cmd/mobius/            the server binary (flag -addr, default 127.0.0.1:6363)
internal/api/          the Gork routes and handlers
api/openapi.json       the generated OpenAPI spec
web/                   the React frontend (Vite, TypeScript, Tailwind CSS, shadcn/ui)
web/src/api/           the typed API client and the generated types
web/src/components/ui/ the shadcn/ui components
web/embed.go           embeds web/dist into the binary and serves it
.mobius/check          the local check, also used in CI
```

## Commands

- `make dev-api` starts the Go server.
- `make dev-web` starts the Vite dev server. Vite sends `/api` to the Go server.
- `make generate` writes the spec and the TypeScript types.
- `make build` builds the UI and then `bin/mobius`.
- `make check` runs `.mobius/check`.
