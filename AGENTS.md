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
- A new Harness records the tokens, the cost and the reported model of each turn when the Harness gives them, in `internal/engine/usage.go`, with a Go test of the read.
- Give each `exec.CommandContext` command that reads its output a `cmd.WaitDelay`, as `git` in `internal/runner/git.go` does. A context cancel kills only the started process, and a child of that process can keep the pipe open for all time.
- A Go timer (`time.AfterFunc`, `time.After`, `time.NewTimer`) stops while the Mac of the server sleeps. For a wait that must end at a wall-clock time, store the end time and compare it with `e.timeNow()` at each poll.

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
- `web/src/api/schemas.gen.ts` comes from `api/openapi.json`. orval writes the Effect Schemas of the requests and the responses with the second output of `web/orval.config.ts`.
- `internal/store/*.sql.go`, `internal/store/db.go` and `internal/store/models.go` come from `sqlc.yaml`, the migrations and `internal/store/queries.sql`.

After a change to the API or to the queries, run `make generate` and commit the result.

orval does not read `contentSchema` and `itemSchema` of OpenAPI 3.2. Thus `web/orval.config.ts` changes the input of orval before it generates the client. `api/openapi.json` does not change.

- It replaces each schema that has `contentMediaType: application/json` with its `contentSchema`. Thus the generated event types have the decoded data type.
- It removes each operation that has only `text/event-stream` success responses, because the generated function waits for the full body. The components stay, so the event types stay.

## Foldkit screens

A Foldkit screen runs inside its React host (for example `web/src/Conversation.tsx` for `web/src/chat/`, `web/src/Inbox.tsx` for `web/src/inbox/`, `web/src/Workstreams.tsx` for `web/src/workstreams/`, `web/src/Tasks.tsx` for `web/src/tasks/`, `web/src/Agents.tsx` for `web/src/agents/`, `web/src/CheckupHost.tsx` for `web/src/checkup/`, `web/src/Panel.tsx` for `web/src/panel/`, `web/src/Roles.tsx` for `web/src/settings/roles/` and `web/src/Usage.tsx` for `web/src/usage/`) until the shell moves to Foldkit.

- Foldkit renders in the next animation frame. A Playwright step that comes right after another step can see the old view. Use a web-first assertion (`expect(...).toHaveText`) before the next action.
- The program reads the plain stores of `web/src/lib` (unread counts, Workstream list, drafts, voice) in a Subscription, with `storeValues` of `web/src/live/stores.ts`. Do not send them through a Port. The program changes a draft only through the draft store, with a Command that calls `updateDraft`, and the Subscription gives the new draft back to the Model.
- A Port holds only data that a Schema can encode. The host sends the data that only React has (for example the Brief and the Inbox items of a tray) through an inbound Port, and the program asks the host for the login and for a route through an outbound Port.
- A Mount runs when its element enters the DOM. A Mount does not run again when its arguments change, so give the element a new `h.Key` to run it again.
- A change of the dependencies of a Subscription stops the old stream and starts the new one. A stream that registers a voice input must not restart when an unrelated part of the Model changes, because the recording stops when no input has its draft key.
- To listen to `/api/events`, use `liveEvents` and `liveListener` of `web/src/live/events.ts` with a Schema for the data of each event. Do not open an `EventSource` in a screen. The only exception is the stream of the lines of one agent (`web/src/transcript/stream.ts`), because `/api/events` does not carry the lines.
- The router restores the scroll position before the program renders its first content. The host of a page that scrolls calls `useRestoredScroll` of `web/src/lib/restoreScroll.ts` (see `web/src/Workstreams.tsx`).
- A page that keeps its state in the search params gets the search as a Flag, and the program keeps it in its Model. The program gives each new search to the host through an outbound Port, and the host writes it with `navigate({ search, replace: true })`. The host embeds the program one time, because a new Flag disposes the program. A new route search reaches the program through an inbound Port (see `web/src/Usage.tsx`).
- A link to a page of the host router uses `defineRouterLink` of `web/src/ui/link.ts` with the Message that asks the host for the route. The host opens the route through the outbound Port `navigationRequested`.
- Write each new Foldkit screen with the rules of the Brief of the move: real Schemas from `web/src/api/schemas.gen.ts`, no type cast, no `unknown` and no `any`, `Effect.tryPromise` with a `Schema.TaggedError` for each Promise that can fail (`web/src/lib/effects.ts`).

To listen to a server-sent event in a React screen, use `onEvent` from `web/src/lib/events.ts` with the generated event type, for example `onEvent<LiveEvents, 'message'>(source, 'message', listener)`. A wrong event name or a wrong data field gives a type error.

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
  | `internal/api/` | `make generate`. Commit `api/openapi.json`, `web/src/api/api.gen.ts` and `web/src/api/schemas.gen.ts`. |
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
- Parallel sessions on the machine of Mobius share `/tmp`. Make a temporary file or directory with `mktemp`. Do not use a fixed name such as `/tmp/e2e.out`.
- Do not put a command with a pipe `|` in a Markdown table cell. The escape `\|` stays in a copied command, and `go test -run 'A\|B'` matches no test. Put the command below the table.
- Wait for a condition with `testkit.WaitFor`. Do not sleep for a fixed time. Before a test changes an issue, wait for the first poll of its repository (`WaitForFirstPoll`).
- A test server runs only fake agents: it takes no directory with a Harness command from the PATH of the test. To check that something does not happen, wait for more polls (`waitForPolls` in `internal/engine`).
- Reach the start state and each change that the Done list names through the engine and the fake GitHub. Do not set them with SQL, because a test that sets the result with SQL passes also when the engine path is wrong. When a test must insert a row with SQL, give the row each field and each fake GitHub item of the cause of its state, and give a `lead_events` row the kind and the payload that the engine writes. For a held Lead event, let the fake agent call `hold_event` (the script entry `holdEvent` in `internal/engine/lead_chat_test.go`).
- The Playwright tests in `web/e2e` test the UI. The command `pnpm e2e` builds the UI, and Playwright starts `TestServer` of `web/e2e` with a fake GitHub, on a free port or on the port in `MOBIUS_E2E_PORT`. CI sets port 6464. The tests write a desktop and a phone screenshot of each screen to `web/screenshots`.
- Before the first run of the Playwright tests, install the browser: `pnpm exec playwright install chromium`.
- `web/screenshots` is not in the repository (`.gitignore`). The job `e2e` of `ci.yml` uploads it as the artifact `screenshots`. The workflow `screenshots` puts the files on the branch `screenshots` and shows them in a section of the pull request description, with the image of `main` next to the new image. No workflow pushes to the branch of a pull request.
- When the job `e2e` fails, CI uploads `web/test-results` as the artifact `test-results`. It has a `trace.zip` file for each failed test. To open the file, run `pnpm exec playwright show-trace <file>`.
- Each run must give the same screenshots, so a pull request with no UI change shows no changed screen. `TestServer` gives a fixed time to each time that the UI shows (`fixTimes`). Before each screenshot, `web/e2e/screenshots.spec.ts` waits for the data that the page loads (`frame` and `ready`). When the UI shows a new time or new data, add it there.
- `TestServer` of `web/e2e` has fixtures with fixed past dates, so a new step that compares a stored time with `e.timeNow()` also acts on them. Fix the time with `SetClock`, as `web/e2e/server_test.go` does.

## Before a push

Before the first run of `make fmt`, install golangci-lint. Use the golangci-lint version of the `version` input of `golangci-lint-action` in `.github/workflows/ci.yml`:

```
curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.14.0
```

- Mobius takes the end of a turn as the end of the work. Before the local check, Mobius waits for the background tasks of the session. Then it pushes only the commits.
- Commit all work before the turn ends. Wait for the result of each test run in the same turn.
- Give each long command (`pnpm e2e`, a Go test of `internal/engine`) the Bash `timeout` 600000. With the default limit, the Bash tool moves the command to the background.

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
web/                   the web client (Vite, TypeScript, Tailwind CSS): React screens that move to Foldkit one by one
web/src/api/           the generated API client and types
web/src/chat/          the chat screen as a Foldkit program: model, messages, update, commands, subscriptions, view and composer. It also holds the rows of Inbox items and the answer panel
web/src/inbox/         the Inbox screen as a Foldkit program: it uses the model, the messages, the update and the item Subscriptions of web/src/chat/, with its own flags, Ports, view and tests
web/src/workstreams/   the Workstreams screen as a Foldkit program, with its own Ports and view. Its host is web/src/Workstreams.tsx
web/src/tasks/         the Tasks screen as a Foldkit program: the board, the read on each live event of Workstreams and of the Inbox, and its tests. Its host is web/src/Tasks.tsx
web/src/agents/        the Agents screen as a Foldkit program: the list of the open agents and the Transcript of one agent. Its host is web/src/Agents.tsx
web/src/checkup/       the Checkup screens as one Foldkit program with one view for each route: the overview, the tools with the upgrade dialog, the permissions, the labels and the Workstreams. Its host is web/src/CheckupHost.tsx, which keeps the top bar with the fix button of the labels
web/src/panel/         the panel of a Workstream as a Foldkit program, with the tabs Tasks, Agents, Details, Brief and Recurring, each with its own model, update and view. Its host is web/src/Panel.tsx, which Chat.tsx embeds in the side panel and in the phone sheet
web/src/transcript/    the Transcript of one agent as a Foldkit Submodel: the lines, the stream, the older pages and the start of a check. The Agents screen and the Agents tab use it
web/src/settings/      the settings screens as seven Foldkit programs, each in its own folder: overview (/settings), switches, voice, devices, github (the page and the first setup screen), memory (three routes in one program) and roles. The hosts are web/src/Settings.tsx, SwitchSettings.tsx, Voice.tsx, Devices.tsx, GitHub.tsx, Memory.tsx and Roles.tsx. The switches come from the shell through inbound Ports, and a change of a switch goes back through an outbound Port, because the shell keeps the state of the switches
web/src/usage/         the Usage screen as a Foldkit program: the tabs Cost, Tokens, Times, Skills and Quality, the filters, the groups, the tables and the charts. Its host is web/src/Usage.tsx, which keeps TopBar and writes each new search in the address. lib/usage.ts validates the search params for the router
web/src/setting/       the sheet of the Harness, the model and the effort as a Foldkit Submodel. The Tasks tab and the Details tab use it
web/src/ui/            the Foldkit parts that many screens use: the buttons, the badges, the dialog, the icons, the page header, the menus, the tabs, the SVG bar chart and line chart, the router link and the Markdown view
web/src/live/          the Subscription of /api/events with the reconnect rules of the shell, and the Subscription of a plain store
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
- `pnpm test` in `web` runs the Vitest tests of the Foldkit code: the Story tests of each `update` and the tests of the Markdown view.
- `make generate` writes the sqlc code, the spec and the TypeScript client.
- `make fmt` formats the Go code with golangci-lint and the frontend files with oxfmt.
- `make build` builds the UI and then `bin/mobius`.
