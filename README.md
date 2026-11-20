# Mobius

Mobius is a self-hosted agent factory. This repository contains a rewrite of Mobius-Toolkit/Mobius-rust in Go. The rewrite has a Go server and a Foldkit frontend in one binary. The project is in an early phase.

To install Mobius, read [docs/install.md](docs/install.md).

## Development

To install Mobius, to write the config file and to start the server, read [docs/install.md](docs/install.md).

- `make dev-api` starts the Go server. Caution: with no `MOBIUS_CONFIG`, the server uses `~/.mobius`. For development, set `MOBIUS_CONFIG` to a config file with another `data_dir`.
- `make dev-web` starts the Vite dev server. Vite sends `/api` to the Go server.
- `make build` builds the UI and then `bin/mobius`.

## Layout

```
cmd/mobius/            the mobius command: serve (the default) and init. With the name gh, it is the gh of the agent environment
internal/api/          the Gork routes and handlers, and the device login check of each route
internal/auth/         the access password and the device logins
internal/config/       reads config.toml
internal/engine/       the poll of the repositories, the Mobius labels, the checkup, the trust rules, the Workstreams with their local copy, the Autopilot switch and the close, the dispatch, the Lead chat with the Lead events, the Coordinator, the Triager, the Inbox, the Tasks tab, the Implementer with the local check and the pull request, the Reviewer with the review rounds, the Judge, the fix rounds and the conflict rounds, the end of a task, the Researcher, the Curator with the memory file of each repository, the agent sessions with their Mobius tools and Transcripts, the Worker slots and the queue, the usage-limit pauses, the Worker restarts, the Housekeeper, the recovery after a restart, the drain and the upgrade
internal/github/       the GitHub Apps: the App setup, the user tokens, the installation tokens and the repositories
internal/mcp/          the Mobius MCP server and the gh token of each session key
internal/runner/       starts an agent session of a Harness through ACP, holds the gh of the agent environment, fetches the bare clones of the repositories with their worktrees, and runs the local check and the setup script of a worktree
internal/setup/        mobius init: asks for the values of a new config.toml and writes it
internal/store/        the SQLite database: goose migrations and sqlc queries
internal/testkit/      the fake GitHub with a git repository for each repository, the fake agent as a Harness command, the fake df, and the waits of the tests
internal/testkit/fakeagent/  the fake ACP agent that plays a TOML script (the format is in its package comment)
internal/testkit/fakedf/     the program df of the tests, with the free space of SetFreeSpace
internal/testkit/testserver/ starts the Mobius server for a test
api/openapi.json       the generated OpenAPI spec
web/                   the web client (Vite, TypeScript, Tailwind CSS): Foldkit single-page application
web/src/app/           the Foldkit application: model, messages, update, subscriptions, router, frame and view
web/src/api/           the generated API client and types
web/src/chat/          the chat screen as a Foldkit Submodel: model, messages, update, commands, subscriptions, view and composer. It also holds the rows of Inbox items and the answer panel
web/src/inbox/         the Inbox screen as a Foldkit Submodel: it uses the model, the messages, the update and the item Subscriptions of web/src/chat/, with its own view and tests
web/src/workstreams/   the Workstreams screen as a Foldkit Submodel, with its own subscriptions and view
web/src/tasks/         the Tasks screen as a Foldkit Submodel: the board, the read on each live event of Workstreams and of the Inbox, and its tests
web/src/agents/        the Agents screen as a Foldkit Submodel: the list of the open agents and the Transcript of one agent
web/src/checkup/       the Checkup screens as one Foldkit Submodel with one view for each route: the overview, the tools with the upgrade dialog, the permissions, the labels and the Workstreams
web/src/panel/         the panel of a Workstream as a Foldkit Submodel, with the tabs Tasks, Agents, Details, Brief and Recurring, each with its own model, update, subscriptions and view
web/src/transcript/    the Transcript of one agent as a Foldkit Submodel: the lines, the stream, the older pages and the start of a check. The Agents screen and the Agents tab use it
web/src/settings/      the settings screens as eight Foldkit Submodels, each in its own folder: overview (/settings), switches, voice, devices, github (the page and the first setup screen), memory (three routes in one Submodel), roles and mcp-servers (the MCP servers with their sheet and OAuth)
web/src/usage/         the Usage screen as a Foldkit Submodel: the tabs Cost, Tokens, Times, Skills and Quality, the filters, the groups, the tables and the charts. lib/usage.ts validates the search params for the router
web/src/setting/       the sheet of the Harness, the model and the effort as a Foldkit Submodel. The Tasks tab and the Details tab use it
web/src/ui/            the Foldkit parts that many screens use: the buttons, the badges, the dialog, the icons, the page header, the menus, the tabs, the SVG bar chart and line chart, the router link and the Markdown view
web/src/live/          the Subscription of /api/events with the reconnect rules of the shell, and the Subscription of a plain store
web/src/lib/events.ts  the typed listener of server-sent events (onEvent)
web/public/            the files of the PWA: the web app manifest, the service worker and the icons
web/e2e/               the Playwright tests of the UI, and TestServer, the server that they test
web/screenshots/       the screenshots of the Playwright tests (not in the repository)
web/embed.go           embeds web/dist into the binary and serves it
docs/install.md        the install guide
.github/workflows/     ci.yml checks each pull request with the jobs check, engine, e2e and passed. release.yml checks each push to main and releases it with the next patch tag. screenshots.yml shows the screenshots of each pull request in a section of its description
```
