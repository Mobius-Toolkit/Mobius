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
- Use oxfmt to format the frontend (`pnpm format`). Do not add Prettier or Biome.
- Run `make fmt` before each commit. CI only checks the format. It never writes it.
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

`internal/store/migrations/` holds the migrations. The files up to `20261003000000_workstream_copy.sql` are copies of the migration files of the Rust version (`crates/mobius-store/migrations` in Mobius-Toolkit/Mobius-rust, which is archived). Do not change a migration file after it merges. To change the schema, add a new file in the same format.

## Tests

- Use the fakes of `internal/testkit` for GitHub and for the agents. To add an endpoint to the fake GitHub, add its route in `routes`, its handler next to the handlers of the same area, and its state as a field of `FakeGitHub`.
- A test package that installs a fake Harness (`testkit.InstallFakeHarness` or `testkit.InstallFakeAgent`) calls `testkit.Main(m)` from its `TestMain`.
- The gh of the agent environment is a link to the test binary. Thus a test package whose agents run gh calls `runner.GH` from its `TestMain` when the program name is gh, as `internal/engine` does.
- A test of `internal/engine` calls `t.Parallel()` as its first line. A test stays serial in two cases: it changes a package variable or the environment (for example with `t.Setenv`), or it needs an action to finish before a short timer fires (for example a restart delay, a quiet period or an idle timeout). A short timer that only delays an action is safe in parallel. A serial test with a comment "Serial:" has a known hazard: keep it serial. Go runs the serial tests first, so they never run at the same time as a parallel test.
- Before a push, run these checks for each kind of change.

  | Change | Checks |
  |---|---|
  | `*.go` in package `P` | `go test ./P -run <names>`. Do not run the full `internal/engine` package. |
  | An MCP tool of a role | Run the tool list tests. The command is below the table. |
  | `internal/store/migrations/`, `internal/store/queries.sql` | `make generate`, then `go test ./internal/store`. Search the store tests for a fixed migration version. |
  | `internal/api/` | `make generate`. Commit `api/openapi.json` and `web/src/api/api.gen.ts`. |
  | `web/src/` | In `web`, `pnpm e2e e2e/<changed>.spec.ts e2e/screenshots.spec.ts`. Then read the `-desktop` and `-phone` image of each changed screen in `web/screenshots`. |
  | A new test | It must fail on the base commit and pass on the commit of the change. |
  | Each change | `make fmt` before each commit. Mobius runs `.mobius/check` after each turn. |

  The command of the tool list tests: `go test ./internal/engine -run 'TestTheLeadGetsTheMobiusURLAndOnlyTheLeadTools|TestEachRoleGetsOnlyItsOwnTools'`

- For a small package, `go test ./<package>` is also correct. Do not run `go test ./...` on the machine of Mobius. Do not run a measurement run (for example `go test -json ./internal/engine`). Do not write a loop that waits for a low load and then starts such a run. Take measurements from the CI logs.
- Run the tests of a change one time. Run them again only after a code change. The tests use the CPU of the machine of the Owner, so each extra run makes the local checks of other tasks slow or gives them a timeout.
- Do not run a test or a command in a loop to check a behavior. If one run does not prove the behavior, write a test that proves it, or write the doubt in the result.
- Do not run load tests or stress tests. Do not use `-count` with a value above 1, a `-race` loop, a shell loop, a `-cpu` list, parallel `go test` processes or a load program. CI finds a flaky test. Find its cause from the CI logs and the code. Do not ask for such a run in an issue body or a Done list.
- If a test fails one time and passes in the next run, write its name in the result. Do not run it more times.
- Do not start a background process that stays after the command (for example `yes >/dev/null &`). Each Bash call has a new shell, so a later `kill` cannot find the process.
- Wait for a condition with `testkit.WaitFor`. Do not sleep for a fixed time. Before a test changes an issue, wait for the first poll of its repository (`WaitForFirstPoll`).
- A test server runs only fake agents: it takes no directory with a Harness command from the PATH of the test. To check that something does not happen, wait for more polls (`waitForPolls` in `internal/engine`).
- Reach the start state and each change that the Done list names through the engine and the fake GitHub. Do not set them with SQL, because a test that sets the result with SQL passes also when the engine path is wrong. When a test must insert a row with SQL, give the row each field and each fake GitHub item of the cause of its state, and give a `lead_events` row the kind and the payload that the engine writes. For a held Lead event, let the fake agent call `hold_event` (the script entry `holdEvent` in `internal/engine/lead_chat_test.go`).
- The Playwright tests in `web/e2e` test the UI. The command `pnpm e2e` builds the UI, and Playwright starts `TestServer` of `web/e2e` with a fake GitHub, on a free port or on the port in `MOBIUS_E2E_PORT`. CI sets port 6464. The tests write a desktop and a phone screenshot of each screen to `web/screenshots`.
- Before the first run of the Playwright tests, install the browser: `pnpm exec playwright install chromium`.
- `web/screenshots` is not in the repository (`.gitignore`). The job `e2e` of `ci.yml` uploads it as the artifact `screenshots`. The workflow `screenshots` puts the files on the branch `screenshots` and shows them in a section of the pull request description, with the image of `main` next to the new image. No workflow pushes to the branch of a pull request.
- When the job `e2e` fails, CI uploads `web/test-results` as the artifact `test-results`. It has a `trace.zip` file for each failed test. To open the file, run `pnpm exec playwright show-trace <file>`.
- Each run must give the same screenshots, so a pull request with no UI change shows no changed screen. `TestServer` gives a fixed time to each time that the UI shows (`fixTimes`). Before each screenshot, `web/e2e/screenshots.spec.ts` waits for the data that the page loads (`frame` and `ready`). When the UI shows a new time or new data, add it there.

## Before a push

Before the first run of `make fmt`, install golangci-lint. Use the golangci-lint version of the `version` input of `golangci-lint-action` in `.github/workflows/ci.yml`:

```
curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.14.0
```

Only CI runs all Go tests. Mobius starts a CI fix round when a Go test fails in CI.

## Layout

```
cmd/mobius/            the mobius command: serve (the default) and init. With the name gh, it is the gh of the agent environment
internal/api/          the Gork routes and handlers, and the device login check of each route
internal/auth/         the access password and the device logins
internal/config/       reads config.toml
internal/engine/       the poll of the repositories, the Mobius labels, the checkup, the trust rules, the Workstreams with their local copy, the Autopilot switch and the close, the dispatch, the Lead chat with the Lead events, the Coordinator, the Triager, the Inbox, the Tasks tab, the Implementer with the local check and the pull request, the Reviewer with the review rounds, the Judge, the fix rounds and the conflict rounds, the end of a task, the Researcher, the Curator with the memory file of each repository, the agent sessions with their Mobius tools and Transcripts, the Worker slots and the queue, the usage-limit pauses, the Worker restarts, the Housekeeper, the recovery after a restart, the drain and the upgrade
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
web/screenshots/       the screenshots of the Playwright tests (not in the repository)
web/embed.go           embeds web/dist into the binary and serves it
docs/install.md        the install guide
.github/workflows/     ci.yml checks each pull request. release.yml checks each push to main and releases it with the next patch tag. screenshots.yml shows the screenshots of each pull request in a section of its description
```

## Commands

- `mobius init` writes a new config file. `mobius` or `mobius serve` starts the server.
- `MOBIUS_CONFIG` gives the path of the config file (default `~/.mobius/config.toml`). `IP` (`127.0.0.1` or `0.0.0.0`) and `PORT` (default `6363`) give the address. The server uses `mobius.db` in the `data_dir` of the config (default `~/.mobius`).
- `make dev-api` starts the Go server. Caution: with no `MOBIUS_CONFIG`, the server uses `~/.mobius`. For development, set `MOBIUS_CONFIG` to a config file with another `data_dir`.
- `make dev-web` starts the Vite dev server. Vite sends `/api` to the Go server.
- `pnpm format` in `web` formats the frontend files with oxfmt. `pnpm format:check` fails on an unformatted file.
- `make generate` writes the sqlc code, the spec and the TypeScript client.
- `make fmt` formats the Go code with golangci-lint and the frontend files with oxfmt.
- `make build` builds the UI and then `bin/mobius`.
