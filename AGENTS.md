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

## Gork

Gork (`github.com/gork-labs/gork`, the repository gork-labs/gork) has the same owner as this repository. Do not work around Gork here.

- If Gork has a defect, or does not have a feature that the task needs, stop the task.
- Fix Gork first, in a pull request to gork-labs/gork.
- Do not rename a field, move a route out of the spec, add a plain handler, or change a generated file because of Gork.
- Each merge into the main branch of Gork gets a new patch tag. After the merge, update Gork in `go.mod` to that tag. Then continue the task.

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

## Tests

- Use the fakes of `internal/testkit` for GitHub and for the agents. To add an endpoint to the fake GitHub, add its route in `routes`, its handler next to the handlers of the same area, and its state as a field of `FakeGitHub`.
- A test package that installs a fake Harness (`testkit.InstallFakeHarness` or `testkit.InstallFakeAgent`) calls `testkit.Main(m)` from its `TestMain`.
- The gh of the agent environment is a link to the test binary. Thus a test package whose agents run gh calls `runner.GH` from its `TestMain` when the program name is gh, as `internal/engine` does.
- Wait for a condition with `testkit.WaitFor`. Do not sleep for a fixed time. Before a test changes an issue, wait for the first poll of its repository (`WaitForFirstPoll`).
- A test server runs only fake agents: it takes no directory with a Harness command from the PATH of the test. To check that something does not happen, wait for more polls (`waitForPolls` in `internal/engine`).
- The Playwright tests in `web/e2e` test the UI. Run them with `pnpm e2e` in `web`. The command builds the UI, and Playwright starts `TestServer` of `web/e2e` on port 6464 with a fake GitHub. The tests write a desktop and a phone screenshot of each screen to `web/screenshots`. The CI job `e2e` runs the tests and keeps the screenshots as the artifact `screenshots`. `.mobius/check` does not run them.
- Before the first run of the Playwright tests, install the browser: `pnpm exec playwright install chromium`.

## Before a push

Run `.mobius/check` (or `make check`). It must pass.

## Layout

```
cmd/mobius/            the mobius command: serve (the default) and init. With the name gh, it is the gh of the agent environment
internal/api/          the Gork routes and handlers, and the device login check of each route
internal/auth/         the access password and the device logins
internal/config/       reads config.toml
internal/engine/       the poll of the repositories, the Mobius labels, the checkup, the trust rules, the Workstreams with their local copy, the Autopilot switch and the close, the dispatch, the Lead chat with the Lead events, the Triager, the Inbox, the Tasks tab, the Implementer with the local check and the pull request, the fix rounds and the conflict rounds, the end of a task, the Researcher, the agent sessions with their Mobius tools and Transcripts, the Worker slots and the queue, the usage-limit pauses, the Worker restarts, the Housekeeper, the recovery after a restart, the drain and the upgrade
internal/github/       the GitHub Apps: the App setup, the user tokens, the installation tokens and the repositories
internal/mcp/          the Mobius MCP server and the gh token of each session key
internal/runner/       starts an agent session of a Harness through ACP, holds the gh of the agent environment, fetches the bare clones of the repositories with their worktrees, and runs the local check
internal/setup/        mobius init: asks for the values of a new config.toml and writes it
internal/store/        the SQLite database: goose migrations and sqlc queries
internal/testkit/      the fake GitHub with a git repository for each repository, the fake agent as a Harness command, the fake df, and the waits of the tests
internal/testkit/fakeagent/  the fake ACP agent that plays a TOML script (the format is in its package comment)
internal/testkit/fakedf/     the program df of the tests, with the free space of SetFreeSpace
internal/testkit/testserver/ starts the Mobius server for a test
api/openapi.json       the generated OpenAPI spec
web/                   the React frontend (Vite, TypeScript, Tailwind CSS, shadcn/ui)
web/src/api/           the generated API client and types
web/src/components/ui/ the shadcn/ui components
web/src/lib/events.ts  the typed listener of server-sent events (onEvent)
web/public/            the files of the PWA: the web app manifest, the service worker and the icons
web/e2e/               the Playwright tests of the UI, and TestServer, the server that they test
web/embed.go           embeds web/dist into the binary and serves it
.mobius/check          the local check, also used in CI
docs/install.md        the install guide
.github/workflows/     ci.yml checks each pull request. release.yml checks each push to main and releases it with the next patch tag
```

## Commands

- `mobius init` writes a new config file. `mobius` or `mobius serve` starts the server.
- `MOBIUS_CONFIG` gives the path of the config file (default `~/.mobius/config.toml`). `IP` (`127.0.0.1` or `0.0.0.0`) and `PORT` (default `6363`) give the address. The server uses `mobius.db` in the `data_dir` of the config (default `~/.mobius`).
- `make dev-api` starts the Go server. Caution: with no `MOBIUS_CONFIG`, the server uses `~/.mobius`. For development, set `MOBIUS_CONFIG` to a config file with another `data_dir`.
- `make dev-web` starts the Vite dev server. Vite sends `/api` to the Go server.
- `make generate` writes the sqlc code, the spec and the TypeScript client.
- `make build` builds the UI and then `bin/mobius`.
- `make check` runs `.mobius/check`.
