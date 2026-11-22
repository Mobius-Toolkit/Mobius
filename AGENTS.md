# Rules for agents

## Writing

- Write all prose in Simplified Technical English (ASD-STE100). This rule applies to docs, code comments, commit messages, pull requests, UI text and error messages.
- Use Conventional Commits for the commit subject.
- Write times in UTC. Mobius, its database and GitHub use UTC. The shell clock and the `ls` times of the machine of Mobius use Central European time (CET or CEST). Use `date -u` or the GitHub times.

## Code

- Write only the code that the task needs (YAGNI). Keep the code flat and simple (KISS).
- Do not write a comment that tells what the code does. Write a comment only for a durable fact that the code cannot show: an invariant, an external contract, a unit or a hazard.
- Do not suppress a linter. Do not use `//nolint`, `oxlint-disable`, `eslint-disable`, `@ts-ignore` or `@ts-expect-error`. Fix the finding.
- Use Bun for the frontend. Do not use npm, yarn, pnpm or Node.
- Use Oxlint to lint the frontend (`bun run lint`). Do not add ESLint.
- Use `bun fmt` to format the frontend (`bun run format`). Do not add Prettier, Biome or oxfmt.
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

After a change to the API or to the queries, run `make generate` and commit the result. Also run it after a merge of `origin/main` that touches the migrations, `internal/store/queries.sql` or `internal/api`. A clean text merge can still leave a stale generated file.

orval does not read `contentSchema` and `itemSchema` of OpenAPI 3.2. Thus `web/orval.config.ts` changes the input of orval before it generates the client. `api/openapi.json` does not change.

- It replaces each schema that has `contentMediaType: application/json` with its `contentSchema`. Thus the generated event types have the decoded data type.
- It removes each operation that has only `text/event-stream` success responses, because the generated function waits for the full body. The components stay, so the event types stay.

## Foldkit application

The web client is one Foldkit application (`Runtime.makeApplication` in `web/src/main.ts`) with its router, frame, shell and Submodels in `web/src/app/`.

- Foldkit renders in the next animation frame. A Playwright step that comes right after another step can see the old view. Use a web-first assertion (`expect(...).toHaveText`) before the next action.
- The application reads the plain stores of `web/src/lib` (unread counts, Workstream list, drafts, voice) in Subscriptions, with `storeValues` of `web/src/live/stores.ts`. The application changes a draft only through the draft store, with a Command that calls `updateDraft`, and the Subscription gives the new draft back to the Model.
- Submodels fold into the parent Model and lift their Subscriptions with `Subscription.lift`. When a Submodel needs to communicate with the application, it returns a Message that `update` handles.
- A Mount runs when its element enters the DOM. A Mount does not run again when its arguments change, so give the element a new `h.Key` to run it again.
- A change of the dependencies of a Subscription stops the old stream and starts the new one. A stream that registers a voice input must not restart when an unrelated part of the Model changes, because the recording stops when no input has its draft key.
- To listen to `/api/events`, the application uses `liveEvents` and `liveListener` of `web/src/live/events.ts` with a Schema for the data of each event. Submodels do not open an `EventSource`. The only exception is the stream of the lines of one agent (`web/src/transcript/stream.ts`), because `/api/events` does not carry the lines.
- Write each Foldkit screen with the rules of the Brief of the move: real Schemas from `web/src/api/schemas.gen.ts`, no type cast, no `unknown` and no `any`, `Effect.tryPromise` with a `Schema.TaggedError` for each Promise that can fail (`web/src/lib/effects.ts`).

## Migrations

`internal/store/migrations/` holds the migrations. Do not change a migration file after it merges. To change the schema, add a new file in the same format. A migration file is plain SQL with only an up step. Its only goose annotation is an optional first line `-- +goose NO TRANSACTION`. Do not write a down step. A store test that needs the state before a migration opens the database with `openBefore` in `internal/store/store_test.go`.

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
  | `web/src/` | In `web`, `bun run e2e e2e/<changed>.spec.ts e2e/screenshots.spec.ts`. Then read the `-desktop` and `-phone` image of each changed screen in `web/screenshots`. |
  | A new test | It must fail on the base commit and pass on the commit of the change. |
  | Each change | `make fmt` before each commit. Mobius runs `.mobius/check` after each turn. |

  The command of the tool list tests: `go test ./internal/engine -run 'TestTheLeadGetsTheMobiusURLAndOnlyTheLeadTools|TestEachRoleGetsOnlyItsOwnTools'`

  Follow these steps for the row "A new test":

  1. Commit the fix and the new tests first.
  2. Take as `<base>` the commit before the fix: `$(git merge-base HEAD origin/main)` in the first round, and `origin/$(git branch --show-current)` in a fix round. In a fix round, the branch base also removes the changes of the earlier rounds, and the test can then fail for another cause.
  3. Run `git diff <base> HEAD -- <files> | git apply -R`, where `<files>` are the source files of the fix with no `_test.go` file. Then run only the new tests with `-run`. Then restore with `git checkout HEAD -- <files>`, and check that `git status --short` shows no file of `<files>`.
  4. Do not use `git checkout <base> -- <files>` for this step, because it keeps each new file of the fix. Do not use `git stash`, because all worktrees share one stash.
  5. A compile error of the new test on the base also counts as a failure.

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
- The Playwright tests in `web/e2e` test the UI. The command `bun run e2e` builds the UI, and Playwright starts `TestServer` of `web/e2e` with a fake GitHub, on a free port or on the port in `MOBIUS_E2E_PORT`. The tests write a desktop and a phone screenshot of each screen to `web/screenshots`.
- With no `MOBIUS_E2E_GROUP`, `bun run e2e` starts four servers. With `MOBIUS_E2E_GROUP` set to `screenshots`, `a`, `b` or `c`, it runs only the projects of that group and starts only the server of that group. The project `screenshots` uses the server that has no GitHub Apps, because that test creates the Apps in the UI. Each group `a`, `b` and `c` has its own server. It runs with `MOBIUS_E2E_APPS=1`, so it has both Apps from its start, on a free port or on the port in `MOBIUS_E2E_PORT_A`, `MOBIUS_E2E_PORT_B` or `MOBIUS_E2E_PORT_C`. Playwright waits for `GET /e2e/ready` of each of these servers, which answers 200 only after the full fixture exists. Two groups do not share a server, because a spec changes the global state of its server (the Start switch, the role limits, the Lead sessions that wait).
- The projects form four groups: `screenshots`, `a`, `b` and `c`. `web/playwright.config.ts` gives the group of each spec. Each group runs alone: `MOBIUS_E2E_GROUP=<group> bun run e2e`. A link between two groups is not allowed, and the group of a spec gives its server.
- Put a new spec in one group. It must not need data that another group or the test `screenshots` changes. A link (`dependencies`) between two projects of one group has a comment with its data order.
- Each server of a group starts with a drain, as the server of `screenshots` does, so the poll starts no task and the Leads do not run. A spec that needs a running task or Lead calls `cancelDrain` of `web/e2e/upgrade.ts` after its login.
- Before the first run of the Playwright tests, install the browser: `bunx --bun playwright install chromium`.
- When a job `e2e (<group>)` fails, CI uploads `web/test-results` as the artifact `test-results-<group>`. It has a `trace.zip` file for each failed test. To open the file, run `bunx --bun playwright show-trace <file>`.
- Each run must give the same screenshots, so a pull request with no UI change shows no changed screen. `TestServer` gives a fixed time to each time that the UI shows (`fixTimes`). Before each screenshot, `web/e2e/screenshots.spec.ts` waits for the data that the page loads (`frame` and `ready`). When the UI shows a new time or new data, add it there.
- `TestServer` of `web/e2e` has fixtures with fixed past dates, so a new step that compares a stored time with `e.timeNow()` also acts on them. Fix the time with `SetClock`, as `web/e2e/server_test.go` does.
- A fake program of a test (for example the `gh` of the agent environment) is a shell script that runs the system program by its full path, as `stubGH` in `internal/engine/lead_writes_test.go` does. Do not make it a link to the system program. The coreutils of the CI runner are multi-call binaries that refuse to run under another name. To change a fake program, write a new file and move it with `mv`, because a write in place can fail with `ETXTBSY`.
- A test does not read the real network or a port of the running Mobius (whisper-server on `6365` and `6366`). When a change adds a call to an external service, give each test that reaches the call a fake server or an address of `testkit.FreeAddress`. This includes the existing tests of the package and `TestServer` of `web/e2e`.
- Before an e2e test types into the chat composer, wait for `toBeEditable()`. The composer (`web/src/chat/composer.ts`) is read-only until it loads the draft (`draftLoaded`).

## Before a push

Before the first run of `make fmt`, install golangci-lint. Use the golangci-lint version of the `version` input of `golangci-lint-action` in `.github/workflows/ci.yml`:

```
curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.14.0
```

- Mobius takes the end of a turn as the end of the work. Before the local check, Mobius waits for the background tasks of the session. Then it pushes only the commits.
- Commit all work before the turn ends. Wait for the result of each test run in the same turn.
- Mobius runs `.mobius/init` one time in a new worktree of an Implementer, before the first prompt. It runs the file in the same way as `.mobius/check`, with the time limit `check_timeout`. The other roles do not run it. When it fails, the first prompt starts with the section "Setup failed". Then do the setup yourself before you run tests or lint.
- Give each long command (`bun run e2e`, a Go test of `internal/engine`) the Bash `timeout` 600000. With the default limit, the Bash tool moves the command to the background.
- A script or a command of an agent session that needs a free port of the machine gets it from `mobius-port` (`mobius-port 3` gives three ports). Mobius reserves each port for the script or the agent session, until it ends. `mobius-port` fails outside Mobius, so a Go test keeps `testkit.FreeAddress`.
- Mobius runs git with `core.hooksPath=/dev/null` (`internal/runner/git.go`), so a git hook never runs for agent work. Do not put a check or a format step in a git hook.

## Commands

- `bun run format` in `web` formats the frontend files with `bun fmt`. `bun run format:check` fails on an unformatted file.
- `bun run test` in `web` runs the Vitest tests of the Foldkit code: the Story tests of each `update` and the tests of the Markdown view.
- `make generate` writes the sqlc code, the spec and the TypeScript client.
- `make fmt` formats the Go code with golangci-lint and the frontend files with `bun fmt`.
