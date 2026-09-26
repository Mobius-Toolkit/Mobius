# Harness rate limits and process model: facts

Date of research: 2026-09-26.

Marks:

- **V**: I verified the fact in a primary source (source code at a pinned tag, the shipped binary, official docs, or an official changelog).
- **U**: I did not verify the fact. It is an inference, or the source is not primary.

Versions that I examined:

- Claude Code: `claude-agent-acp` tag `v0.81.2` (commit `5dbb453`). It pins `@anthropic-ai/claude-agent-sdk` 0.3.280 and `@agentclientprotocol/sdk` 1.5.0.
- Antigravity: `agy_acp_server` 1.2.1 (darwin-arm64 archive from the ACP registry). The `.par` file contains the Python source as plain text. I compared each extracted `.py` file byte for byte with the binary.
- Devin: `devin` 3000.11.3 (aarch64-apple-darwin archive from the ACP registry). The source is closed. I read the docs, the changelog, and the strings in the binary.

## Summary table

| Question | Claude Code (`claude-agent-acp`) | Antigravity (`agy_acp_server`) | Devin (`devin acp`) |
|---|---|---|---|
| A. What the client sees on a usage limit | First, an `agent_message_chunk` with the CLI text, for example `You've hit your session limit · resets 3:45pm`. Then a JSON-RPC error on `session/prompt`: code `-32603`, message `Internal error: <same text>`, `data` = `{"errorKind":"rate_limit"}` or absent. (V) | Path 1: an `agent_message_chunk` with `Usage Limit Reached` and `Your limit will reset in 4 days, 23 hours.`, then `stopReason: "end_turn"`. Path 2: `stopReason: "refusal"`. No JSON-RPC error. (V) | A JSON-RPC error. The binary example is code `-32011`, message `Quota exhausted.`, `data` = `{"cognition.ai/errorKind":"resource_exhausted","cognition.ai/retryable":true}`. The key `cognition.ai/retryAfterSeconds` also exists. (V for the example, U for the live path) |
| A. Reset time machine-readable? | Not in the error. Yes in the `usage_update` `_meta["_claude/rateLimit"].resetsAt` field, when the adapter sends it. (V) | No. The client gets prose only. (V) | Maybe through `cognition.ai/retryAfterSeconds`. Not documented. (U) |
| B. Harness retries before it reports? | Yes for 429 throttles and 529 overload: up to 10 retries with exponential backoff. No retry for a plan usage limit, unless `CLAUDE_CODE_RETRY_WATCHDOG=1`. (V) | Yes. The harness has "built-in interactive defaults" with exponential backoff. The numbers are not published. The ACP server does not change them. (V for the existence, numbers not found) | Yes, some retries exist (strings such as `Exhausted inference retries; stopping turn`). The count and the time are not published. (U) |
| C. ACP method to read the allowance that is left | No ACP method. The `/usage` prompt returns Markdown with reset times. `rate_limit_event` data goes to `usage_update._meta`. Raw SDK messages are available through `_claude/sdkMessage` when the client asks for them. (V) | No ACP method and no extension. (V) | No documented ACP method. The CLI `/usage` command shows daily and weekly quota. (V for `/usage`, U for ACP) |
| D. Process model | One adapter process hosts many sessions. Each session spawns one Claude Code CLI child process. (V) | One server process hosts many sessions. Each session spawns one `localharness_external` child process. (V) | One `devin acp` process hosts many sessions. (V) No child process per session is documented. (U) |
| D. Memory | Not documented. Claude Code shows a warning when the heap of one session process passes 2.5 GB. (V) | Not documented. (V) | Not documented. (V) |

## Background: ACP v1 has no rate-limit primitive

- **V** The ACP schema in `@agentclientprotocol/sdk` 1.5.0 has five `StopReason` values: `end_turn`, `max_tokens`, `max_turn_requests`, `refusal`, `cancelled`. No value is for a rate limit or a quota. [7]
- **V** The ACP `usage_update` session update has `used`, `size`, optional `cost`, and `_meta`. It describes the context window and the cost. It has no field for a plan allowance. [7]
- **V** In the TypeScript ACP SDK, `RequestError.internalError(data, additionalMessage)` makes code `-32603` and the message `Internal error: <additionalMessage>`. `RequestError.authRequired()` makes code `-32000`. [8]

## 1. Claude Code through `claude-agent-acp` v0.81.2

### A. What the client sees

The adapter has two output modes. A client that advertises the JetBrains "AIR" `sessionFailure` capability in `clientCapabilities._meta.jetbrains.air` gets typed failures. Every other client, and Mobius is one of them, gets the "legacy" mode. [1d]

Legacy mode (a client without the AIR capability):

1. **V** The Claude Code CLI makes a synthetic assistant message (`model: "<synthetic>"`) for a usage limit. The adapter forwards its text to the client as an `agent_message_chunk`. The adapter skips this text only for AIR clients. [1b L6162-L6175]
2. **V** The SDK then sends a `result` message with `is_error: true`. The adapter rejects the `session/prompt` request with `RequestError.internalError(errorKindData(lastAssistantError), message.result)`. [1b L5579-L5597], [1b L3591-L3594], [1b L4037-L4040]
3. **V** So the JSON-RPC error has code `-32603` and the message `Internal error: <result text>`. [8]
4. **V** The `data` field is `{"errorKind": "<SDKAssistantMessageError>"}` when the last assistant message had an `error` field. Otherwise `data` is absent. [1b L9222-L9226]
5. **V** An adapter unit test uses an assistant message with `error: "rate_limit"` and the result text `You've hit your limit · resets 8pm`. The test expects `data` to equal `{"errorKind":"rate_limit"}`. [1c L8149-L8201]
6. **U** The test builds the message by hand. I did not capture a live CLI message. So I cannot confirm that the live synthetic usage-limit message always carries `error: "rate_limit"`.
7. **V** The possible `errorKind` values are: `authentication_failed`, `oauth_org_not_allowed`, `account_on_hold`, `verification_required`, `billing_error`, `rate_limit`, `overloaded`, `invalid_request`, `model_not_found`, `server_error`, `unknown`, `max_output_tokens`, `cloud_credential_error`. [2 L3535]
8. **V** The result is not a normal `stopReason`. The prompt request fails with an error. [1b L5592-L5597]
9. **V** A sign-out is different. The adapter rejects with `RequestError.authRequired()`, code `-32000`. [1b L5580-L5586]

AIR mode (a client that advertises the `sessionFailure` capability):

- **V** The adapter does not reject the prompt. It returns `stopReason: "end_turn"` and puts the failure in `_meta.jetbrains.air.sessionFailure`. It also sends a `session_info_update` with the same `_meta`. [1b L4041-L4057], [1a L168-L179], [1a L291-L305]
- **V** The failure object has `id`, `revision`, `category`, `severity`, `title`, optional `details` and `reason`, and `actions`. The internal `kind` is not in the object. [1a L168-L179]
- **V** A usage limit maps to `category: "limit"` and `actions: []` (internal kind `quota_exhausted`). A rate limit maps to `category: "limit"` and `actions: ["retry"]` (internal kind `rate_limited`). Overload maps to `category: "service"` and `actions: ["retry"]`. [1a L136-L155], [1a L449-L473]
- **V** The `title` is the text of the synthetic usage-limit message. So the reset time is prose in this mode too. [1b L6049-L6052], [1b L4048]

Text of the message:

- **V** The Claude Code docs list these usage-limit texts: `You've hit your session limit · resets 3:45pm`, `You've hit your weekly limit · resets Mon 12:00am`, `You've hit your Opus limit · resets 3:45pm`, `You've hit your Sonnet limit · resets 3:45pm`. [4]
- **V** The Agent SDK exports `USAGE_LIMIT_ERROR_PREFIXES`. The list has 12 prefixes, for example `You've hit your`, `You've reached your`, `You're out of usage credits`, `You're out of extra usage`. The adapter uses these prefixes to detect a usage-limit message. [2 L9423], [1a L205-L212]
- **V** The old format `Claude AI usage limit reached|<epoch>` is not in the prefix list of SDK 0.3.280. [2 L9423]
- **V** A server throttle that is not a plan limit has a different text: `API Error: Server is temporarily limiting requests (not your usage limit)`. [4]
- **V** A 429 on an API key or a cloud project has the text `API Error: Request rejected (429) · ...`. [4]
- **V** Repeated overload has the text `API Error: Repeated 529 Overloaded errors. ...`. [4]

Reset time:

- **V** The text form of the reset time (`resets 3:45pm`) is local time with no date and no time zone. It is not machine-readable.
- **V** The SDK sends a `rate_limit_event` message with `rate_limit_info`. Its fields include `status` (`allowed`, `allowed_warning`, `rejected`), `resetsAt` (number), `rateLimitType` (`five_hour`, `seven_day`, `seven_day_opus`, `seven_day_sonnet`, `seven_day_overage_included`, `overage`), `utilization`, and overage fields. [2 L5410-L5447]
- **V** The adapter copies `rate_limit_info` into a `usage_update` session notification as `_meta["_claude/rateLimit"]`. It sends this update only when the turn already has a real model response with token usage. [1b L6359-L6371]
- **U** The unit of `resetsAt` is not written in the SDK types. The Claude Code status line docs give `resets_at` in Unix epoch seconds. `resetsAt` probably uses the same unit. [2 L5425], [5]
- **U** When the limit blocks the first request of a turn, no real model response exists. Then the adapter probably does not send the `usage_update`, and the client sees no machine-readable reset time. This follows from the code condition `lastAssistantTotalUsage !== null`. I did not test it. [1b L6360]

### B. Retries in the harness

- **V** Claude Code retries transient failures up to 10 times with exponential backoff before it shows an error. [4]
- **V** The retried failures include server errors, overload (529), request timeouts, dropped connections, and "temporary 429 throttles". On a claude.ai subscription, this includes 429 throttles without the plan quota headers (v2.1.199 and later). [4]
- **V** `CLAUDE_CODE_MAX_RETRIES` changes the count. The default is 10. The cap is 15. [3]
- **V** `CLAUDE_CODE_RETRY_WATCHDOG=1` retries 429 and 529 capacity errors with no limit. The backoff is up to 5 minutes between attempts. When the response carries a rate-limit reset time, the watchdog waits until the reset. The docs say: "a session that hits a usage limit waits out the remaining window". The watchdog does not wait on a 429 for a spend limit or for exhausted usage credits (v2.1.239 and later). [3]
- **V** Without the watchdog, a plan usage limit is not in the list of retried failures. The docs say "Claude Code blocks further requests until the reset time shown in the message". [4]
- **V** An interactive session can wait for the reset and continue ("Usage limit reached · continuing automatically at 3:45pm"). The docs describe this for "an interactive session" only. [4]
- **U** The SDK path that the adapter uses is not interactive. So this automatic wait probably does not apply to ACP sessions.
- **V** For each retry, the SDK sends an `api_retry` system message with `attempt`, `max_retries`, `retry_delay_ms`, `error_status`, and `error`. [2 L3433-L3447]
- **V** The adapter shows `api_retry` only to AIR clients, as a warning ("Retrying Claude, attempt N of M."). A legacy client gets no ACP notification during the retries. [1b L4936-L4962], [1a L357-L361]
- **V** A client can still receive the raw `api_retry` message. See the `_claude/sdkMessage` extension in section C.

### C. Read the allowance that is left

- **V** The adapter has no ACP method and no extension method that returns the plan allowance.
- **V** Each `PromptResponse` has `_meta.quota` with `token_count` and `model_usage`. The name is misleading: these values are the tokens that the turn used, in the codex-acp shape. They do not show the allowance. [1b L9036-L9074]
- **V** A `session/prompt` with the single text `/usage` is a special case. The adapter calls the experimental SDK method `usage_EXPERIMENTAL_MAY_CHANGE_DO_NOT_RELY_ON_THIS_API_YET()` with a 5 second timeout. It sends the result as Markdown in an `agent_message_chunk`. The Markdown has lines such as `5-hour limit — N% · Resets <time>`. [1b L347-L368], [1b L2910-L2913], [1e]
- **V** The SDK control request `get_usage` returns `five_hour`, `seven_day`, and other windows, each with `utilization` and `resets_at` (ISO 8601 string). The SDK marks it "Experimental — the shape may change". [2 L4052-L4146]
- **V** A client can set `_meta.claudeCode.emitRawSDKMessages` on `session/new`. Then the adapter also sends each raw SDK message as the extension notification `_claude/sdkMessage`, with `{sessionId, message}`. This includes `rate_limit_event` and `api_retry`. The value can be `true` or a filter list. [1b L1339], [1b L1376-L1382], [1b L4250-L4258]
- **V** The Claude Code status line receives `rate_limits.five_hour.used_percentage`, `rate_limits.five_hour.resets_at` (Unix epoch seconds), and the same for `seven_day`. This is a CLI feature, not an ACP feature. [5]

### D. Process model and memory

- **V** One adapter process keeps all sessions in one map, `sessions: Record<string, Session>`. [1b L1962]
- **V** For each new session, the adapter calls the SDK `query()`. A code comment says: "`query()` spawns the CLI at once". So each session has one Claude Code CLI child process. [1b L8636-L8644]
- **V** The docs show a critical memory warning when the heap of a session passes 2.5 GB. [6]
- The docs give no typical memory value for one session or one process.

## 2. Antigravity through `agy_acp_server` 1.2.1

Source: the Python files inside `agy_acp_server.par` from the registry archive [9]. Paths below are relative to `google3/cloud/developer_experience/antigravity_extensions/acp_server/` (server) and `google3/third_party/py/google/antigravity/` (SDK). The file content is identical to the bytes in the binary.

### A. What the client sees

The server has two paths for an exhausted quota.

Path 1, an exception from the harness:

- **V** The `prompt` handler catches `AntigravityExecutionError`, `AntigravityConnectionError`, and `AntigravityValidationError`. It sends one `agent_message_chunk`. Then it returns a normal `PromptResponse`. It does not send a JSON-RPC error. [9 server.py L4816-L4850, L4905-L4910]
- **V** For a quota error, `quota_errors.quota_exhausted_message()` makes the text. It matches `reason:QUOTA_EXHAUSTED` in the Go-formatted `google.rpc.ErrorInfo` text of the error. [9 ccpa_connection/quota_errors.py L20-L31, L69-L85]
- **V** The text is `Usage Limit Reached\n\nYou have reached your current quota for this period.` and, if known, one more sentence. The sentence is `Your limit will reset in 4 days, 23 hours.` (from `quotaResetDelay`) or `Your limit will reset on Sep 30, 2026 14:00 UTC.` (from `quotaResetTimeStamp`). [9 quota_errors.py L62-L100, L114-L147]
- **V** Other execution errors give the text `Agent execution error: <error>`. [9 server.py L4838-L4843]
- **V** If the exception comes before `agent.chat()` returns, the `stopReason` is `end_turn`. If `agent.chat()` returned first, the server maps the stop reason of that response. [9 server.py L4905-L4907]

Path 2, a stop reason from the harness:

- **V** The harness protocol has `STOP_REASON_QUOTA_EXHAUSTED`. The SDK maps it to `StopReason.QUOTA_EXHAUSTED`, "Turn halted because backend model API quota was exhausted". [9 SDK connections/local/proto_converters.py L36-L37], [9 SDK types.py L1093, L1102]
- **V** The server maps `QUOTA_EXHAUSTED` to the ACP `stopReason: "refusal"`. [9 server.py L1366-L1381]
- **U** I cannot say which path a real subscription limit takes. I did not trigger a real limit.

Reset time:

- **V** The client gets the reset time only as English prose. The raw `quotaResetDelay` and `quotaResetTimeStamp` values stay in the server log. [9 quota_errors.py L1-L11, L88-L100]
- **V** The binary of the harness (`localharness_external`) contains the text `You have exhausted your quota on this model.`. [9]
- **U** The harness can maybe emit that text in path 2 as a model message. I did not find where it goes.

### B. Retries in the harness

- **V** The SDK type `RetryConfig` has `api_retry` (`max_retries`, `initial_sleep_duration_ms`, `exponential_multiplier`, `jitter_range`) and `model_output_retry`. [9 SDK types.py L620-L663]
- **V** The docstring says: when `retry_config` is omitted, "the backend automatically applies built-in interactive defaults (e.g., standard API retry counts, exponential backoff, and model output validation attempts)". [9 SDK types.py L648-L663]
- **V** `RetryConfig.benchmark()` retries "transient API errors (429 rate limits, 503 service throttling)" up to 4,294,967,295 times with 1000 ms initial sleep. So the harness treats 429 as retryable. [9 SDK types.py L665-L680]
- **V** The ACP server does not set `retry_config`. It only passes the default `None` through. So the built-in defaults apply. [9 proxy_agent_config.py L132], [9 SDK connections/connection.py L103]
- The default count and the default delay are not in the Python source. I did not find them in the Go harness binary. I did not find them in the docs.
- **V** The server itself retries a turn once, but only when the websocket to the harness closes. It rebuilds the agent and sends the turn again. This retry is not for rate limits. [9 server.py L4749-L4796]
- **V** The Antigravity CLI changelog (entry 1.2.2) says that transient `genai.APIError` failures (502, 503, 504, per-minute 429 rate limits, and mid-stream interruptions) "automatically retry in-process with exponential backoff". [18]
- **U** This entry is about the Antigravity CLI. I cannot confirm that the ACP server 1.2.1 uses the same code.

### C. Read the allowance that is left

- **V** The server has no extension method, no extension notification, and no `usage_update` for quota. A search of all server files for `ext_method`, `ext_notification`, and quota reads finds nothing. [9]
- **V** The Antigravity CLI has a `/usage` (alias `/quota`) command that shows model quota. This is a CLI command, not an ACP method. [10]
- **V** The plans page says that Google AI Pro and Ultra quota refreshes every five hours until a weekly limit. Other users get a weekly refresh. [11]

### D. Process model and memory

- **V** One server process keeps all sessions in `self._sessions: dict[str, SessionInfo]`. [9 server.py L1404]
- **V** `new_session` makes one SDK `Agent` for each session and enters its context. [9 server.py L3037-L3052]
- **V** The `Agent` context opens a `Conversation`. The conversation enters the connection strategy. The local connection starts `localharness_external` with `subprocess.Popen`. So each session has one harness child process. [9 SDK conversation/conversation.py L84-L88], [9 SDK connections/local/local_connection.py L1380-L1410]
- **V** The harness binary is about 120 MB on disk (darwin-arm64). The server `.par` is about 277 MB. [9]
- The docs give no memory value for one session or one process.

### Terms of service fact

- **V** The Antigravity FAQ says: "Using third party software, tools, or services to access Antigravity is a violation of our Terms of Service". It names Claude Code, OpenClaw, and OpenCode as examples. [12]
- **U** The FAQ does not say whether a third-party ACP client of the official Google ACP server is in this group.

## 3. Devin CLI through `devin acp` 3000.11.3

The Devin CLI is closed source. The GitHub repository `CognitionAI/devin-cli` has only a README and scripts. [15]

### A. What the client sees

- **V** Changelog v2026.4.17-0: errors from upstream servers, "quota exhaustion, 5xx responses, connection drops, etc.", "reach ACP clients with a typed cause so they can render them with the right severity". [13]
- **V** Changelog v3000.10.21: "server rate limits are no longer labeled 'Quota exhausted'". So a rate limit and a quota limit are two different errors. [13]
- **V** The binary contains help text for the hidden command `/debug-echo`. It says a body with an `"error"` key "returns it as the native JSON-RPC error response to this prompt". The example is: [14]

  ```json
  {"error":{"code":-32011,"message":"Quota exhausted.","data":{"cognition.ai/errorKind":"resource_exhausted","cognition.ai/retryable":true}}}
  ```

- **V** The binary contains the keys `cognition.ai/errorKind`, `cognition.ai/retryable`, and `cognition.ai/retryAfterSeconds`. Next to them are the values `unavailable`, `resource_exhausted`, `deadline_exceeded`, `content_filter`, `unknown`. [14]
- **V** The internal error type has the variants `RateLimited` (with `message` and `retry_after`), `QuotaExhausted`, `UsageLimitReached`, `ServerError`, `Refusal`, `ContextTooLong`, and others. [14]
- **V** User-facing texts in the binary include `Quota exhausted` + `Purchase on-demand usage or turn on auto-reload, or wait for your quota to reset.`, `Usage limit reached`, `Organization usage limit reached`, and `Usage paused`. [14]
- **U** From the strings, a real quota limit probably gives a JSON-RPC error with code `-32011` and `data["cognition.ai/errorKind"] = "resource_exhausted"`. A rate limit probably adds `cognition.ai/retryAfterSeconds`. No doc confirms this. I did not trigger a real limit.
- **V** Changelog v3000.11.1: "ACP reports a structured, retryable error when an agent's communication channel closes". [13]

### B. Retries in the harness

- **V** The binary contains `Transient inference error; retrying on next iteration`, `Exhausted inference retries; stopping turn`, `Backend stream creation failed (attempt /), retrying in s`, and `Connection failed (attempt ), retrying...`. [14]
- **V** Changelog v2026.4.24-1 shows the text "Connection lost, retrying..." for a mid-stream failure. [13]
- **V** The troubleshooting page tells users to "Wait a few minutes before retrying" after a rate limit or a quota limit. [16]
- The retry count, the backoff, and the retry behavior for a 429 are not documented. I did not find them.

### C. Read the allowance that is left

- **V** The CLI `/usage` command shows "daily/weekly quota progress bars with reset times" (v3000.5.20) and "quota % remaining and overage balance" (v2026.4.30-4). [13]
- **V** The backend `PlanStatus` message in the binary has `daily_quota_remaining_percent`, `weekly_quota_remaining_percent`, `daily_quota_reset_at_unix`, `weekly_quota_reset_at_unix`, `overage_balance_micros`, `acu_consumed`, and `acu_limit`. [14]
- **V** The docs list no ACP method for this data. The list of ACP extension method names in the binary (`cognition.ai/...`) has no usage or quota method. [14], [17]
- **U** `/usage` is maybe available as a prompt text over ACP. The changelog does not list `/usage` among the slash commands for ACP clients. [13 v3000.3.22]
- **U** Session metadata keys `cognition.ai/acuUsed`, `cognition.ai/totalCreditCost`, and `cognition.ai/responseDimensions` exist in the binary. They seem to describe consumption, not the allowance that is left. [14]

### D. Process model and memory

- **V** Changelog v3000.3.22: "`devin acp --model <name>` ... sets the default model for every session the ACP server creates". So one `devin acp` process hosts many sessions. [13]
- **V** Changelog v3000.11.1: "Session-lock errors identify the process holding the lock, including over ACP". A session has a lock that one process holds. [13]
- **V** The binary contains an extension notification name `cognition.ai/processMemory`. [14]
- **U** This notification probably reports the memory of the process to the client. It is not documented.
- **V** The `devin` binary is about 163 MB on disk (aarch64-apple-darwin). [14]
- The docs give no memory value for one session or one process.
- **U** The interactive TUI of Devin starts its own ACP agent child (`spawned ACP agent child`). This is about the TUI, not about a client of `devin acp`.

## Gaps

- No Harness documents a memory value for each session.
- No Harness documents an ACP method that returns the allowance that is left.
- Antigravity and Devin do not publish their default retry counts or delays.
- I did not trigger a real limit on any Harness. All wire shapes come from source code, binary strings, tests, and docs.

## Sources

1. `agentclientprotocol/claude-agent-acp`, tag `v0.81.2`:
   - 1a. https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/session-failure-extension.ts (L19-L32 kinds, L136-L155 policies, L168-L179 failure `_meta`, L291-L305 `session_info_update`, L205-L212 usage-limit detection, L357-L361 `prepare`, L449-L486 error mapping)
   - 1b. https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/acp-agent.ts (L347-L368, L1339, L1376-L1382, L1962, L2910-L2913, L3591-L3594, L4037-L4057, L4250-L4258, L4936-L4962, L5579-L5597, L6045-L6052, L6162-L6175, L6359-L6371, L8636-L8644, L9036-L9074, L9222-L9226)
   - 1c. https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/tests/acp-agent.test.ts#L8149-L8201
   - 1d. https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/air-extension.ts
   - 1e. https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/usage-markdown.ts
2. `@anthropic-ai/claude-agent-sdk` 0.3.280, file `sdk.d.ts` (npm tarball): https://www.npmjs.com/package/@anthropic-ai/claude-agent-sdk/v/0.3.280 (L3433-L3447 `SDKAPIRetryMessage`, L3535 `SDKAssistantMessageError`, L4052-L4146 `get_usage`, L5410-L5447 `SDKRateLimitInfo`, L9423 `USAGE_LIMIT_ERROR_PREFIXES`)
3. Claude Code docs, environment variables: https://code.claude.com/docs/en/env-vars.md (`CLAUDE_CODE_MAX_RETRIES`, `CLAUDE_CODE_RETRY_WATCHDOG`)
4. Claude Code docs, error reference: https://code.claude.com/docs/en/errors.md (sections "Automatic retries", "You've hit your session limit", "Server is temporarily limiting requests", "Request rejected (429)", "API Error: Repeated 529 Overloaded errors")
5. Claude Code docs, status line: https://code.claude.com/docs/en/statusline.md (`rate_limits` fields)
6. Claude Code docs, troubleshooting: https://code.claude.com/docs/en/troubleshooting.md ("High CPU or memory usage")
7. `@agentclientprotocol/sdk` 1.5.0, file `schema/schema.json` (`StopReason`, `UsageUpdate`): https://www.npmjs.com/package/@agentclientprotocol/sdk/v/1.5.0
8. `@agentclientprotocol/sdk` 1.5.0, file `dist/jsonrpc.js` L1020-L1034 (`internalError`, `authRequired`)
9. `agy_acp_server` 1.2.1, ACP registry entry https://github.com/agentclientprotocol/registry/blob/main/antigravity-acp/agent.json and archive https://dl.google.com/agy-extensions/releases/macos/agy-acp-server-1.2.1-darwin-arm64.zip. Files inside `agy_acp_server.par`: `acp_server/server.py`, `acp_server/ccpa_connection/quota_errors.py`, `acp_server/proxy_agent_config.py`, and SDK files `google/antigravity/types.py`, `conversation/conversation.py`, `connections/connection.py`, `connections/local/local_connection.py`, `connections/local/proto_converters.py`. Also the binary `localharness_external`.
10. Antigravity docs index: https://antigravity.google/llms.txt ("Model Quotas (/usage)")
11. Antigravity docs, plans: https://antigravity.google/docs/plans
12. Antigravity docs, FAQ: https://antigravity.google/docs/faq
13. Devin CLI stable changelog: https://docs.devin.ai/cli/changelog/stable.md (entries v3000.11.1, v3000.10.21, v3000.5.20, v3000.3.22, v2026.4.30-4, v2026.4.24-1, v2026.4.17-0)
14. Devin CLI binary 3000.11.3: https://static.devin.ai/cli/3000.11.3/devin-3000.11.3-aarch64-apple-darwin.tar.gz (registry entry https://github.com/agentclientprotocol/registry/blob/main/devin/agent.json). Facts from `strings` of `bin/devin`.
15. Devin CLI repository: https://github.com/CognitionAI/devin-cli
16. Devin CLI troubleshooting: https://docs.devin.ai/cli/troubleshooting.md ("Rate limiting or quota exceeded")
17. Devin docs index: https://docs.devin.ai/llms.txt (ACP pages for JetBrains, Zed, Xcode)
18. Antigravity changelog: https://antigravity.google/changelog (entry 1.2.2, "Improved model API error resilience and diagnostics")
