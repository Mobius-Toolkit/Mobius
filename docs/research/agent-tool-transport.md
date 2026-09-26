# Transport for Mobius actions: CLI, stdio MCP, or HTTP MCP

Date of research: 2026-09-26.

Versions examined:

- Claude Code through `claude-agent-acp` v0.81.2 (commit `5dbb453`).
- Codex through `codex-acp` v1.13.1 (commit `b1b8490`), which bundles Codex `rust-v0.156.1` (commit `b412ff3`).
- Gemini CLI v0.61.0 (commit `bb52374`).
- Devin CLI 3000.11.x. Devin is closed source, so only its docs apply.
- `rmcp` 3.4.1 (tag `rmcp-v3.4.1`, commit `9427a92`).

Legend: **(V)** = verified in the source code or docs at the cited link. **(U)** = unverified. A claim without a first-party source is (U).

## Summary

HTTP MCP inside the Mobius server is the best transport for all four Harnesses. It carries the session identity in a per-session header, so one Harness process can host many sessions (V [8][13][19]). An env var for a CLI is process-wide in Codex and Gemini, so a CLI needs one Harness process for each session (V [11][22]). The stdio path has filters: Codex gives a stdio MCP server only a small base env (V [4]), and Gemini removes inherited names with TOKEN, KEY, or SECRET (V [20][21]). Gemini also refuses stdio MCP servers in an untrusted folder (V [20]). The main MCP risk is deferred tools: Claude Code and Codex hide MCP tool schemas behind a search step by default (V [7][16]). Mobius can stop this with `_meta["anthropic/alwaysLoad"]` for Claude Code (V [16]) and `code_mode.direct_only_tool_namespaces` for Codex (V [7], U for the effect through `codex-acp`). The Rust effort is small, because `rmcp` 3.4.1 gives an axum-ready Streamable HTTP service and exposes the HTTP headers to each tool handler (V [29]).

## Comparison table

| Criterion | CLI (`mobius tool ...` through bash) | stdio MCP (`mobius mcp` subprocess) | HTTP MCP (in the Mobius server) |
| --- | --- | --- | --- |
| Session identity | Env var on the Harness process. **Claude Code**: per session through `_meta.claudeCode.options.env`, because the adapter builds one Claude Code process for each session (V [13]). **Codex**: one `app-server` for all sessions, so the env is process-wide (V [11]). Codex adds `CODEX_THREAD_ID` to each shell, but it is not a secret (V [2]). **Gemini**: one process, so the env is process-wide (V [22]). **Devin**: (U). | ACP `env` field for each server, so identity is per session in all four. **Claude Code**: passes `env` (V [13]). **Codex**: passes `env` (V [4][8]). **Gemini**: passes `env`, but the value goes through `$VAR` expansion (V [20]). **Devin**: config accepts `env` (V [28]); the ACP path is (U). | ACP `headers` for each server, so identity is per session in all four. **Claude Code**: passes headers (V [13]). **Codex**: maps to `http_headers` (V [8]). **Gemini**: passes headers, but expands `$VAR` in the value (V [20]). **Devin**: (U). |
| Env var reaches bash | **Claude Code**: yes, unless `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1` removes names that it recognizes as credentials (V [17]). **Codex**: yes. The default policy is `inherit = all` with `ignore_default_excludes = true`, so the KEY, SECRET, and TOKEN filter is **off** by default (V [1][2][3]). **Gemini**: yes. Redaction is off by default, but it turns on when `GITHUB_SHA` is set or `SURFACE=Github` (V [21][22]). **Devin**: (U). | Not applicable. | Not applicable. |
| Harness env reaches a stdio MCP server | Not applicable. | **Claude Code**: yes, with the same optional scrub (V [17]). **Codex**: no. Only a base list (`HOME`, `PATH`, `USER`, and similar) plus the explicit `env` field (V [4]). **Gemini**: inherited env loses all names that match TOKEN, SECRET, KEY, AUTH, and similar; explicit `env` entries stay (V [20][21]). **Devin**: (U). | Not applicable. |
| Permission prompts in a normal mode | Bash rules apply. The command text is `mobius tool ...`, so an allow rule for that prefix is possible in each Harness (U for exact rule syntax per Harness). | MCP tools prompt. **Codex**: a tool without annotations needs approval in mode `auto` (V [6]). **Gemini**: prompts unless the server has `trust`; ACP servers have no `trust` (V [19][24]). **Claude Code** and **Devin**: prompt unless an allow rule matches (V [18][28]). Mobius, as the ACP client, can answer each `session/request_permission` with "allow" (V, prior note [31]). | Same as stdio MCP. |
| Permission prompts in full auto | **Claude Code** `bypassPermissions`: no prompt (V [18]). The adapter refuses bypass for root unless `IS_SANDBOX` is set (V [14]). **Codex** `agent-full-access`: approval `never`, no sandbox (V [9]). **Gemini** YOLO: allows all tools, but only in a trusted folder (V [23][26]). **Devin** `dangerous`/bypass: auto-approves all (V [28]). | **Claude Code**: bypass skips prompts, except for tools marked `requiresUserInteraction` (V [18]). **Codex**: `never` plus full disk write auto-approves MCP calls (V [6]). **Gemini**: YOLO rule `toolName = "*"` allows (V [26]). **Devin**: bypass allows all (V [28]). | Same as stdio MCP. |
| Sandbox effect | **Codex** default mode `agent` uses `workspace-write` with `networkAccess: false` (V [9]). Then a CLI cannot reach localhost, and a Unix socket needs `network.allow_unix_sockets` (V [12]). Full-access mode has no sandbox (V [9]). | The Harness starts the server outside the shell sandbox (U). | The Harness process makes the HTTP call, not the sandboxed shell (U). |
| Tool deferral | None. The agent learns the CLI from the prompt or `--help`. | **Claude Code**: all MCP tools are deferred by default (tool search). `ENABLE_TOOL_SEARCH=auto` loads them upfront below 10% of the context window. `alwaysLoad` or `_meta["anthropic/alwaysLoad"]` keeps them loaded (V [16]). **Codex**: all non-app MCP tools are deferred when the model supports tool search. All built-in models support it. There is no threshold (V [7]). `code_mode.direct_only_tool_namespaces` makes a namespace direct (V [7]). **Gemini**: no deferral found (U). **Devin**: (U). | Same as stdio MCP. |
| Startup timeout and failure | No startup. A failed call returns an exit code and stderr to the agent. | **Claude Code**: `MCP_TIMEOUT` default 30 s. Connection does not block the first prompt. With tool search, Claude Code tells the model which server failed (V [16][17]). **Codex**: default 30 s in code (docs say 10 s). A failure gives a warning but does not stop the session, unless `required = true`. `codex-acp` does not set `required` (V [3][5][8]). **Gemini**: `session/new` waits for all MCP servers in ACP mode. The connect timeout is 10 minutes. A failure is logged and the session continues (V [27]). **Devin**: (U). | Same limits as stdio MCP. The server lives in the Mobius process, so it is up when the session exists. |
| Tool call timeout | Bash tool timeout of each Harness (U for exact values). | **Claude Code**: idle timeout 30 min for stdio (V [16]). **Codex**: 300 s in code, 60 s in docs (V [3][5]). **Gemini**: 10 min (V [27]). | **Claude Code**: idle timeout 5 min for HTTP (V [16]). Others: same as stdio. |
| ACP `tool_call` event | **Claude Code**: `kind: "execute"`, `title` = the command text, `rawInput` = `{command, ...}` (V [15]). **Codex**: `kind: "execute"`, `title` = command, `rawInput` = `{command, cwd}` (V [10]). **Gemini**: `title` = command, `kind: "execute"` (U for exact fields). The UI must parse the command text to show the action. | **Claude Code**: `title` = `mcp__mobius__create_issue`, `kind: "other"`, `name` and `rawInput` = the arguments (V [15]). **Codex**: `title` = `mcp.mobius.create_issue`, `kind: "execute"`, `rawInput` = `{server, tool, arguments}`, `_meta.is_mcp_tool_call` (V [10]). A failed start shows as a `tool_call` with title `mcp__<server>__startup` (V [10]). **Gemini**: `title` = tool name, `kind: "other"`, no `rawInput`; the arguments are text content, cut near 500 characters (V [24][26]). **Devin**: adds the tool name to `tool_call` metadata (V [28]); other fields (U). | Same as stdio MCP. |
| Schema validation before the call | Argument parser in `mobius` (for example `clap`). A parse error prints usage to stderr, and the agent retries. Long text with newlines is a known problem for shell arguments (V [34]). | **Gemini**: validates against the JSON Schema before the call (V [25]). **Codex**: no client-side validator found in the source (U). **Claude Code**: (U). **Devin**: (U). Mobius must validate on the server in all cases. | Same as stdio MCP. |
| Structured result (Judge `submit_verdicts`) | Stdout text. The agent reads JSON from stdout. | MCP `structuredContent` and text content (V [29]). | Same as stdio MCP. |
| Extra processes | One short process for each call. | One proxy process for each session. | None. |
| Rust effort | A `clap` subcommand for each action, a local API, and help text. The action schema exists twice: CLI arguments and server API (U, estimate). | An `rmcp` stdio server (`transport-io`) that forwards to the Mobius server, plus the server API (V [29] for features; U for effort). | One `StreamableHttpService` in the axum router. Tool handlers read the bearer token from `Extension<http::request::Parts>` (V [29]). |

## Findings per Harness

### Claude Code (`claude-agent-acp` v0.81.2)

- The adapter maps ACP `mcpServers` to SDK `mcpServers`. It keeps `type`, `url`, and `headers` for HTTP and SSE, and `command`, `args`, and `env` for stdio (V [13]).
- The adapter builds `env` from `process.env` plus `_meta.claudeCode.options.env` for each session (V [13]). A CLI token for each session is therefore possible for Claude Code only.
- The adapter merges `_meta.claudeCode.options.mcpServers` with the ACP servers (V [13]). Mobius can use this path to send `alwaysLoad: true` (U, not tested).
- Tool search is on by default. Only tool names and server instructions load at session start (V [16]).
- Tool search needs Claude Sonnet 4.5, Haiku 4.5, Opus 4.5, or later (V [16]). A custom `ANTHROPIC_BASE_URL` turns tool search off (V [16]).
- A server can mark one tool with `"anthropic/alwaysLoad": true` in the tool `_meta` (V [16]). Mobius owns the server, so this works for stdio and HTTP.
- `alwaysLoad` makes startup wait up to 5 s for that server (V [16]).
- MCP tool output is capped at 25,000 tokens by default (V [16]).
- In remote `headers`, Claude Code reads credential variables as empty when it expands `${VAR}` (V [16]). A literal token has no expansion, so this rule does not apply.
- The Zechner benchmark shows a large Haiku token cost for the CLI arm, because Claude Code checks each bash command (V [33]).

### Codex (`codex-acp` v1.13.1, Codex `rust-v0.156.1`)

- **Correction to the brief:** the KEY, SECRET, and TOKEN filter is **off** by default. The default is `ignore_default_excludes = true` (V [1][3]). The filter code runs only when a user sets `ignore_default_excludes = false` (V [2]).
- Codex removes a fixed list of its own launch variables from every child, for example `CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN` (V [2]).
- A user `config.toml` can still change the shell policy. Mobius does not control that file unless it sets `CODEX_HOME` (U).
- A stdio MCP server gets only `HOME`, `LOGNAME`, `PATH`, `SHELL`, `USER`, `LANG`, `LC_ALL`, `TERM`, `TMPDIR`, `TZ`, and `__CF_USER_TEXT_ENCODING`, plus the explicit `env` field (V [4]).
- `codex-acp` sends no `startup_timeout_sec`, `default_tools_approval_mode`, or `required` for injected servers (V [8]).
- `codex-acp` skips an injected server when the user config already has a server with the same name. `DISABLE_MCP_CONFIG_FILTERING=true` turns this check off (V [8]).
- MCP approval: with approval policy `never` and full disk write, Codex auto-approves all MCP calls (V [6]).
  - In other modes, mode `auto` asks unless the tool has `readOnlyHint: true`, or `destructiveHint: false` with `openWorldHint: false` (V [6]).
- Tool deferral: `search_tool_enabled` is true when the model supports tool search and the provider supports namespace tools (V [7]).
  - Then every non-app MCP tool gets `ToolExposure::Deferred` (V [7]). All eleven models in `models.json` have `supports_search_tool: true` (V [7]).
  - A namespace in `code_mode.direct_only_tool_namespaces` loses the deferred exposure and stays direct (V [7]).
- The docs and the code disagree on MCP timeouts. The docs say 10 s startup and 60 s per tool (V [3]). The code at `rust-v0.156.1` says 30 s and 300 s (V [5]).

### Gemini CLI v0.61.0

- In ACP mode, `session/new` waits for MCP startup (V [27]). A slow stdio server delays `session/new` up to the 10-minute connect timeout (V [27]).
- A stdio MCP server needs a trusted folder, else Gemini throws an error for that server (V [20]).
- Folder trust is on by default (V [23]). `GEMINI_CLI_TRUST_WORKSPACE=true` marks the folder as trusted (V [23]).
- Gemini also resets the approval mode to `default` in an untrusted folder, so YOLO needs trust too (V [23]). Mobius must set `GEMINI_CLI_TRUST_WORKSPACE=true` for all transports.
- The stdio MCP env redaction is always on for inherited variables (V [20]). Explicit `env` values stay, except for dangerous names such as `NODE_OPTIONS` (V [20][21]).
- Gemini expands `$VAR` in explicit `env` values and in HTTP header values (V [20]). A Mobius token must not contain `$`.
- The bash env redaction is off by default (V [21][22]).
- Gemini validates tool arguments against the JSON Schema in `build()` before the call (V [25]).
- The ACP `tool_call` for an MCP tool has no `rawInput`. The arguments go into text content as JSON, cut when longer than 500 characters (V [24][26]).

### Devin CLI

- `devin acp` accepts MCP servers from `session/new` and `session/load`, with HTTP and SSE (V [28]).
- The `dangerous` permission mode (aliases `yolo`, `bypass`) auto-approves all tool calls (V [28]).
- Permission rules can allow `mcp__<server>__*` (V [28]).
- In the `smart` mode, a fast model judges each MCP call and each shell command (V [28]).
- Devin adds the tool name to ACP `tool_call` metadata (V [28]).
- Env inheritance, schema validation, deferral, and timeouts are (U). The Devin spike must check them.

## Prior art

| Product | Transport for its own actions | Identity | Source |
| --- | --- | --- | --- |
| Vibe Kanban | stdio MCP (`rmcp`) | Agent cwd (worktree path), no token | (V) [35] |
| Terragon | stdio MCP in the sandbox; the host reads the tool call from the transcript | Sandbox | (V) [36] |
| Sculptor | `sculpt` CLI for plain actions; in-process SDK MCP for UI actions that wait for the user | Env vars for port and IDs, no token | (V) [37] |
| Conductor | REST or `conductor` CLI from bash | Workspace-scoped `CONDUCTOR_API_TOKEN` env var | (V) [38] |
| OpenAI Symphony | Codex app-server `dynamicTools` (Harness-native, experimental) | Host process; "the child receives tool results, not a raw token" | (V) [39] |
| GitHub Copilot coding agent | MCP (stdio, HTTP, SSE), tools only | Repo config | (V) [40] |
| GitHub Agentic Workflows | MCP "safe outputs"; a separate job with write rights does the action | Job | (V) [41] |
| Claude Code agent teams | Native tools (`SendMessage`, `TaskCreate`); cross-session messages over a Unix socket with a token env var in bash | Env var | (V) [42] |
| Devin | MCP (HTTP recommended) | Headers or OAuth | (V) [43] |
| Amp | "Toolboxes": executables with `describe` and `execute` actions | Process | (V from a search snippet only) [44] |

Pattern: local orchestrators use stdio MCP or a CLI. HTTP MCP with a bearer token appears in these products only for external clients. Symphony uses the Codex app-server API directly, but that path does not exist for ACP.

## Evidence: CLI through bash versus MCP tools

- Zechner benchmark (2025-08): both arms had 100% success. MCP was 23% faster and 2.5% cheaper. The CLI arm had a large extra cost from the Claude Code bash check (V [33]).
- Ronacher (2025-08): models are "unsure how to feed newlines or control characters via shell arguments" (V [34]). A Reviewer body with inline comments is exactly this kind of input.
- Anthropic, "Code execution with MCP" (2025-11): tool definitions that load upfront and large intermediate results fill the context window (V [32]).
- Anthropic, "Advanced tool use" (2025-11): tool search raised accuracy from 49% to 74% on Opus 4 with many tools (V [45]).
- Anthropic, "Writing tools for agents" (2025-09): use namespaces, merge steps into one tool, and give errors that the agent can act on (V [46]).
- GitHub (2026-05): a change from some MCP calls to `gh` cut token use by up to 62%. The cause is schema size of a 40-tool server (V [47]). Mobius has 5 to 12 tools for each Role, so its schema cost is small (U, estimate).
- Scalekit (2026-03): CLI 25/25 and MCP 18/25. The MCP failures were TCP timeouts to a remote server (V [48]). A loopback server in the Mobius process does not have this failure mode (U).
- No source shows that models call a small, well-described MCP tool set less reliably than a CLI. The main CLI risks are shell quotes and discovery. The main MCP risks are schema size and deferral (U, synthesis).

## Rust side

- `rmcp` 3.4.1 has the features `server` and `server-side-http`, and an axum example: `axum::Router::new().nest_service("/mcp", StreamableHttpService::new(...))` (V [29]).
- The service injects `http::request::Parts` into the request context. A tool handler reads `Authorization` through `Extension<http::request::Parts>` (V [29]).
- By default the service accepts only loopback `Host` values, as protection against DNS rebinding (V [29]).
- MCP protocol version `2026-07-28` removes sessions (SEP-2567). `rmcp` serves that version stateless and keeps a legacy session mode for older clients (V [29]). A per-request bearer token fits the stateless model.
- `#[tool]` macros derive the JSON Schema from Rust types with `schemars`. Serde gives the server-side validation (V [29] for the macros; U for exact error text).
- A stdio proxy needs the same tool handlers or a byte bridge, plus a second binary mode and a way to find the server. `rmcp` has `transport-io` for stdio and `transport-streamable-http-client-unix-socket` for a Unix socket client (V [29]).
- A CLI needs one `clap` definition for each action, a local API, and a result format. The schema exists in two places (U, estimate).

## Recommendation

Use **HTTP MCP inside the Mobius server**, with one bearer token for each session in ACP `headers`. Do not build the CLI or the stdio proxy now.

Reasons:

1. The token in `headers` binds each call to one session in all four Harnesses. An env var does this only for Claude Code (V [11][13][22]).
2. It avoids all env filters: Codex stdio base env, Gemini stdio redaction, and the optional Claude Code scrub (V [4][17][20]).
3. It avoids the Gemini stdio trust check for the server itself (V [20]). Mobius still needs `GEMINI_CLI_TRUST_WORKSPACE=true` for YOLO (V [23]).
4. MCP gives a JSON Schema, structured results for `submit_verdicts`, and a clear tool name in the ACP `tool_call` for Claude Code and Codex (V [10][15]).
5. Large text such as a review body goes as JSON, not as shell arguments (V [34]).
6. The server is in the Mobius process, so there is no startup race and no extra process (V [29]).

Actions to make it work:

1. Set `_meta["anthropic/alwaysLoad"] = true` on each Mobius tool, so Claude Code loads the schemas at start (V [16]).
2. Put `code_mode.direct_only_tool_namespaces = ["mcp__mobius"]` in the Codex config that Mobius controls (V [7]). A spike must confirm the namespace name and that `codex-acp` passes it (U).
3. Give tools `readOnlyHint` where true, and answer each `session/request_permission` for a Mobius tool with "allow" (V [6], prior note [31]).
4. Run each Harness in its full-auto mode: `bypassPermissions`, `agent-full-access`, YOLO, `dangerous` (V [9][14][26][28]).
5. Return fast from each tool. Claude Code aborts an HTTP call after 5 minutes without a response or progress (V [16]). Codex allows 300 s (V [5]).
6. Validate all arguments on the server. Only Gemini validates on the client (V [25]).
7. Use a token without `$`, for example hex, because Gemini expands `$VAR` in header values (V [20]).
8. Do not use the name of a server in the user config, because `codex-acp` then skips the injected server (V [8]).

Main trade-off: HTTP MCP gives per-session identity, structured input, and no env problems. The cost is the deferral step in Claude Code and Codex, and a dependency on each Harness MCP client. A CLI is simpler to test by hand and has no deferral, but it needs one Harness process for each session and shell quotes for long text. If the deferral fix fails in a spike, a CLI stays a fallback for Claude Code only, where per-session env works (V [13]).

## Open questions for a spike

- Devin: env inheritance, deferral, timeouts, and `tool_call` fields over ACP.
- Codex: does `codex-acp` pass `code_mode.direct_only_tool_namespaces` to the app-server? What is the exact namespace name of an injected server?
- Claude Code: does the adapter pass `alwaysLoad` from `_meta.claudeCode.options.mcpServers`, or does only the tool `_meta` work?
- All: the exact `rawInput` and `title` for each Mobius tool in a real transcript.

## Sources

1. Codex shell policy defaults: [shell_environment_policy.rs L133-L136](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/config/src/shell_environment_policy.rs#L133-L136), [config_types.rs L261-L272](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/protocol/src/config_types.rs#L261-L272).
2. Codex env build, default excludes, and non-inheritable list: [shell_environment.rs L13-L160](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/protocol/src/shell_environment.rs#L13-L160).
3. Codex config reference (`ignore_default_excludes` default `true`; MCP timeouts; `default_tools_approval_mode`; `required`; `code_mode.direct_only_tool_namespaces`): [learn.chatgpt.com config reference](https://learn.chatgpt.com/docs/config-file/config-reference).
4. Codex stdio MCP env: [rmcp-client/src/utils.rs L16-L58, L162-L175](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/rmcp-client/src/utils.rs#L16-L175).
5. Codex MCP timeouts in code: [rmcp_client.rs L103-L104](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/codex-mcp/src/rmcp_client.rs#L103-L104), [startup.rs L116-L129](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/codex-mcp/src/connection_manager/startup.rs#L116-L129).
6. Codex MCP approval: [codex-mcp/src/mcp/mod.rs L91-L110](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/codex-mcp/src/mcp/mod.rs#L91-L110), [mcp_tool_call.rs L1489-L1503, L2420-L2451](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/core/src/mcp_tool_call.rs#L2420-L2451).
7. Codex tool deferral: [mcp_tool_exposure.rs L90-L94](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/core/src/mcp_tool_exposure.rs#L90-L94), [spec_plan.rs L236-L252, L624-L626](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/core/src/tools/spec_plan.rs#L236-L252), [models.json](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/models-manager/models.json), [config/mod.rs L1141](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/core/src/config/mod.rs#L1141).
8. `codex-acp` MCP config: [CodexAcpClient.ts L725-L772, L821-L840, L1216-L1219](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/src/CodexAcpClient.ts#L725-L840).
9. `codex-acp` modes: [AgentMode.ts L38-L81](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/src/AgentMode.ts#L38-L81).
10. `codex-acp` tool call events: [CodexToolCallMapper.ts L95-L108, L152-L163, L284-L295](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/src/CodexToolCallMapper.ts#L95-L163), [CodexEventHandler.ts L1093-L1122](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/src/CodexEventHandler.ts#L1093-L1122).
11. `codex-acp` starts one `app-server` with the adapter env: [CodexJsonRpcConnection.ts L15-L26](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/src/CodexJsonRpcConnection.ts#L15-L26).
12. Codex seatbelt Unix socket allowlist: [seatbelt.rs L160-L180](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/sandboxing/src/seatbelt.rs#L160-L180).
13. Claude adapter MCP map, env, and options: [acp-agent.ts L8282-L8305, L8452-L8461, L8486-L8491](https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/acp-agent.ts#L8282-L8491).
14. Claude adapter bypass rule: [permissions/modes.ts L8-L9](https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/permissions/modes.ts#L8-L9), [acp-agent.ts L8333-L8344](https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/acp-agent.ts#L8333-L8344).
15. Claude adapter tool call events: [acp-agent.ts L9876-L9903](https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/acp-agent.ts#L9876-L9903), [tools.ts L176-L193, L506-L511](https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/tools.ts#L176-L511).
16. Claude Code MCP docs (tool search, `ENABLE_TOOL_SEARCH`, `alwaysLoad`, timeouts, idle timeout, output limits, header expansion): [code.claude.com/docs/en/mcp](https://code.claude.com/docs/en/mcp).
17. Claude Code env vars (`MCP_TIMEOUT`, `MCP_CONNECTION_NONBLOCKING`, `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB`): [code.claude.com/docs/en/env-vars](https://code.claude.com/docs/en/env-vars).
18. Claude Code permission modes ("Actions no mode auto-approves"): [code.claude.com/docs/en/permission-modes](https://code.claude.com/docs/en/permission-modes), [permissions](https://code.claude.com/docs/en/permissions).
19. Gemini ACP MCP map (no `trust`, no `timeout`): [acpSessionManager.ts L285-L325](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/cli/src/acp/acpSessionManager.ts#L285-L325).
20. Gemini stdio MCP env, trust check, and header expansion: [mcp-client.ts L2335-L2385](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/core/src/tools/mcp-client.ts#L2335-L2385), [L988-L1010](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/core/src/tools/mcp-client.ts#L988-L1010).
21. Gemini env sanitizer: [environmentSanitization.ts](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/core/src/services/environmentSanitization.ts).
22. Gemini shell env: [shellExecutionService.ts L465-L490](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/core/src/services/shellExecutionService.ts#L465-L490).
23. Gemini folder trust: [trust.ts L47-L83](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/core/src/utils/trust.ts#L47-L83), [cli config.ts L613-L617, L758-L764](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/cli/src/config/config.ts#L613-L764), [settingsSchema.ts L1914-L1924](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/cli/src/config/settingsSchema.ts#L1914-L1924).
24. Gemini MCP confirmation and title: [mcp-tool.ts L201-L240, L333-L349](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/core/src/tools/mcp-tool.ts#L201-L349).
25. Gemini schema validation: [tools.ts L687-L713](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/core/src/tools/tools.ts#L687-L713).
26. Gemini ACP tool calls, modes, and YOLO policy: [acpSession.ts L712-L828](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/cli/src/acp/acpSession.ts#L712-L828), [acpUtils.ts L224-L250](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/cli/src/acp/acpUtils.ts#L224-L250), [yolo.toml](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/core/src/policy/policies/yolo.toml).
27. Gemini MCP startup in ACP mode and timeout: [core config.ts L1519-L1533](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/core/src/config/config.ts#L1519-L1533), [mcp-client.ts L96](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/core/src/tools/mcp-client.ts#L96).
28. Devin docs: [commands](https://docs.devin.ai/cli/reference/commands), [permissions](https://docs.devin.ai/cli/reference/permissions), [MCP configuration](https://docs.devin.ai/cli/extensibility/mcp/configuration), [stable changelog](https://docs.devin.ai/cli/changelog/stable).
29. `rmcp` 3.4.1: [Cargo.toml features](https://github.com/modelcontextprotocol/rust-sdk/blob/rmcp-v3.4.1/crates/rmcp/Cargo.toml), [counter_streamhttp.rs](https://github.com/modelcontextprotocol/rust-sdk/blob/rmcp-v3.4.1/examples/servers/src/counter_streamhttp.rs), [tower.rs L78-L110, L985-L1050](https://github.com/modelcontextprotocol/rust-sdk/blob/rmcp-v3.4.1/crates/rmcp/src/transport/streamable_http_server/tower.rs#L78-L1050), [crates.io](https://crates.io/crates/rmcp).
30. ACP spec, MCP servers in `session/new`: [session setup](https://agentclientprotocol.com/protocol/v1/session-setup#mcp-servers).
31. Prior note: `git show origin/research/acp-support-per-harness:docs/research/acp-support-per-harness.md` (permission requests per Harness, MCP transports per Harness).
32. Anthropic, code execution with MCP: [anthropic.com/engineering/code-execution-with-mcp](https://www.anthropic.com/engineering/code-execution-with-mcp).
33. Zechner, MCP vs CLI benchmark: [mariozechner.at](https://mariozechner.at/posts/2025-08-15-mcp-vs-cli/).
34. Ronacher, code MCPs: [lucumr.pocoo.org](https://lucumr.pocoo.org/2025/8/18/code-mcps/).
35. Vibe Kanban MCP: [vibe_kanban_mcp.rs](https://github.com/BloopAI/vibe-kanban/blob/d5cbb5380fa0b32e98ef9b8d987f63decce4be3a/crates/mcp/src/bin/vibe_kanban_mcp.rs#L42), [task_server/mod.rs L106-L119](https://github.com/BloopAI/vibe-kanban/blob/d5cbb5380fa0b32e98ef9b8d987f63decce4be3a/crates/mcp/src/task_server/mod.rs#L106-L119).
36. Terragon MCP server: [index.ts](https://github.com/terragon-labs/terragon-oss/blob/83142a17f3970df3e14d1879234a603db6e4f615/packages/mcp-server/src/index.ts#L36), [claude.ts L236-L238](https://github.com/terragon-labs/terragon-oss/blob/83142a17f3970df3e14d1879234a603db6e4f615/packages/daemon/src/claude.ts#L236-L238).
37. Sculptor: [build.py L103-L108](https://github.com/imbue-ai/sculptor/blob/f847102ada1d6896bf6a3a5eccde996760526d9e/sculptor/sculptor/utils/build.py#L103-L108), [mcp_server.py](https://github.com/imbue-ai/sculptor/blob/f847102ada1d6896bf6a3a5eccde996760526d9e/sculptor/sculptor/agents/default/claude_code_sdk/mcp_server.py#L1-L8).
38. Conductor API: [conductor.build/docs/api](https://www.conductor.build/docs/api).
39. OpenAI Symphony: [app_server.ex L320-L328](https://github.com/openai/symphony/blob/be10a1b79df723d6d7612b5651c8522704dafb2e/elixir/lib/symphony_elixir/codex/app_server.ex#L320-L328), [SPEC.md L1091-L1127](https://github.com/openai/symphony/blob/be10a1b79df723d6d7612b5651c8522704dafb2e/SPEC.md#L1091-L1127).
40. Copilot coding agent MCP: [docs.github.com](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/extend-coding-agent-with-mcp).
41. GitHub Agentic Workflows safe outputs: [gh-aw safe outputs](https://github.github.com/gh-aw/reference/safe-outputs/).
42. Claude Code agent teams and cross-session messages: [agent-teams](https://code.claude.com/docs/en/agent-teams), [cross-session-messaging](https://code.claude.com/docs/en/cross-session-messaging).
43. Devin MCP: [docs.devin.ai/work-with-devin/mcp](https://docs.devin.ai/work-with-devin/mcp).
44. Amp toolboxes: [ampcode.com/news/toolboxes](https://ampcode.com/news/toolboxes).
45. Anthropic, advanced tool use: [anthropic.com/engineering/advanced-tool-use](https://www.anthropic.com/engineering/advanced-tool-use).
46. Anthropic, writing tools for agents: [anthropic.com/engineering/writing-tools-for-agents](https://www.anthropic.com/engineering/writing-tools-for-agents).
47. GitHub, token efficiency in agentic workflows: [github.blog](https://github.blog/ai-and-ml/github-copilot/improving-token-efficiency-in-github-agentic-workflows/).
48. Scalekit, MCP vs CLI: [scalekit.com](https://www.scalekit.com/blog/mcp-vs-cli-use).
