# Agents reach GitHub only through the Mobius MCP server

Only Mobius reads and writes GitHub. An agent gets no GitHub token. It acts on GitHub only through the tools of one HTTP MCP server inside the Mobius server process. Mobius injects this server in `session/new` with the URL `http://127.0.0.1:<port>/mcp/<session-key>`. The key is random for each session and stops when the session ends. From the key, Mobius knows the Role, the Workstream, and the task of the caller. So Mobius shows only the tools of that Role, checks the scope of each call, and applies the trusted-author filter of ADR 0004 to all reads. Agents keep all other tools of their Harness.

## Considered Options

- **`gh` with a short-lived App token in each session:** rejected. Mobius cannot filter what the agent reads, so ADR 0004 breaks. A token that leaks from a session stays valid for up to one hour.
- **A CLI (`mobius <verb>`) that the agent calls through bash:** rejected. The session identity must come from an environment variable, but one Harness process can host many sessions. Review bodies and inline comments need shell quotes for long text with newlines.
- **A stdio MCP server that forwards to the Mobius server:** rejected. It adds one process for each session, and some Harnesses filter the environment of a stdio server.
- **One token in the config for all agents:** rejected. It does not tell Mobius which session calls a tool.

## Consequences

- Each Harness process gets `GH_CONFIG_DIR` set to an empty directory, `GIT_CONFIG_GLOBAL` set to a Mobius file with no credential helper, and `GIT_TERMINAL_PROMPT=0`. A stub `gh` comes first on `PATH` and tells the agent to use the Mobius tools. This guard stops mistakes but is not a security boundary. An agent can still read public pages, for example with `curl`.
- The Implementer commits with the bot identity in the config of its worktree. Only Mobius pushes, with the App token, after the local check.
- Claude Code hides MCP tools behind a search step by default, and Antigravity always does. Mobius marks each tool "always load" for Claude Code.
- Only some Harnesses check the arguments against the JSON Schema of a tool. Mobius validates all arguments on the server.
- Each tool returns at once. A Harness can stop a tool call after a few minutes with no answer.
- The Judge answers through a tool with a JSON Schema, so ACP needs no structured output.
