# ACP support per Harness

Research for [#13](https://github.com/Mobius-Toolkit/Mobius/issues/13). Date of research: 2026-09-26.

## Question

- Does each Harness (Claude Code, Codex, Devin, Gemini CLI) speak the Agent Client Protocol (ACP)? Is the support native, or does it need an adapter?
- Which ACP features work for each Harness?
- Is there a maintained Rust crate for the ACP client side?
- What is the current ACP spec version, and how stable is it?

## Short answer

All four Harnesses speak ACP protocol version 1 over stdio. Devin and Gemini CLI have native ACP modes. Claude Code and Codex need an adapter. The ACP project maintains both adapters. All four support new session, `session/load`, MCP server injection, permission requests, cancel, and streamed updates. The official Rust crate `agent-client-protocol` (2.2.0) supports the client side. ACP v1 is stable. ACP v2 is a draft.

## Native or adapter

| Harness | Kind | Launch command | Version (release date) | Source |
| --- | --- | --- | --- | --- |
| Claude Code | Adapter `claude-agent-acp`. It wraps the Claude Agent SDK, which runs Claude Code. | `npx @agentclientprotocol/claude-agent-acp` | 0.81.2 (2026-09-24) | [registry entry](https://github.com/agentclientprotocol/registry/blob/main/claude-acp/agent.json), [repo](https://github.com/agentclientprotocol/claude-agent-acp/tree/v0.81.2) |
| Codex | Adapter `codex-acp`. It starts the Codex App Server and translates ACP to Codex operations. | `npx @agentclientprotocol/codex-acp` | 1.13.1 (2026-09-23) | [registry entry](https://github.com/agentclientprotocol/registry/blob/main/codex-acp/agent.json), [README](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/README.md) |
| Devin | Native: `devin acp` subcommand (Devin CLI, closed source) | `devin acp` | 3000.11.3 (2026-09-22) | [registry entry](https://github.com/agentclientprotocol/registry/blob/main/devin/agent.json), [command reference](https://docs.devin.ai/cli/reference/commands#devin-acp) |
| Gemini CLI | Native: `--acp` flag | `gemini --acp` | 0.61.0 (2026-09-23) | [registry entry](https://github.com/agentclientprotocol/registry/blob/main/gemini/agent.json), [ACP mode doc](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/docs/cli/acp-mode.md) |

Notes:

- Claude Code has no native ACP mode. Anthropic closed the feature request as "not planned" on 2026-02-09 ([anthropics/claude-code#6686](https://github.com/anthropics/claude-code/issues/6686)). The adapter lists Anthropic, Zed Industries, and JetBrains as authors ([registry entry](https://github.com/agentclientprotocol/registry/blob/main/claude-acp/agent.json)). It pins `@anthropic-ai/claude-agent-sdk` 0.3.280 ([package.json](https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/package.json)).
- Codex has no native ACP mode. The request for built-in support is open ([openai/codex#30052](https://github.com/openai/codex/issues/30052)). The adapter lists OpenAI, JetBrains, and Zed Industries as authors. It bundles `@openai/codex` ^0.156.1 ([package.json](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/package.json)). The `CODEX_PATH` variable selects a different Codex binary ([README](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/README.md)).
- Devin enabled `devin acp` on the stable channel in release 2026.4.9-0 ([changelog](https://docs.devin.ai/cli/changelog/stable)).
- The ACP registry probe of 2026-09-25 shows that all four agents answer `initialize` with `protocolVersion: 1` ([protocol matrix](https://github.com/agentclientprotocol/registry/blob/main/.protocol-matrix/latest.md), [raw JSON](https://github.com/agentclientprotocol/registry/blob/main/.protocol-matrix/latest.json)).

## Feature matrix

Legend: **yes** = verified in source code, docs, or the registry probe. **no** = not advertised, or the probe got "method not found". **unverified** = no primary source found.

| Feature | Claude Code (adapter 0.81.2) | Codex (adapter 1.13.1) | Devin (3000.11.3) | Gemini CLI (0.61.0) |
| --- | --- | --- | --- | --- |
| `session/new` | yes [1] | yes [2] | yes [3] | yes [4] |
| `session/load` (with history replay) | yes [1][5] | yes [2][5] | yes [5] | yes [4][5] |
| `session/resume` (no replay) | yes [1][5] | yes [2][5] | no [5] | no [5] |
| `session/list` | yes [1][5] | yes [2][5] | yes [5] | no [5] |
| `session/close` | yes [1] | yes [2] | unverified | no [4] |
| MCP server injection: stdio | yes (spec baseline) [6] | yes (spec baseline) [6] | yes [7] | yes [4] |
| MCP server injection: HTTP | yes [1] | yes [2] | yes [7] | yes [4] |
| MCP server injection: SSE | yes [1] | no [2] | yes [7] | yes [4] |
| Permission requests (`session/request_permission`) | yes [8] | yes [9] | yes [7] | yes [10] |
| Model selection | config option, category `model` [11] | config option, category `model` [12] | config options [7]; `--model` launch flag [3] | legacy `models` field and `unstable_setSessionModel` [4][13]; `--model` launch flag [14] |
| Session modes (`session/set_mode`) | yes [1] | yes [2] | yes [7] | yes [4][13] |
| Cancel (`session/cancel`) | yes [1] | yes [2] | unverified; spec baseline method [15] | yes [4][13] |
| Streamed updates (`session/update` chunks, tool calls) | yes [1] | yes [2] | yes [7] | yes [10] |
| Plan updates (`sessionUpdate: "plan"`) | yes, from `TodoWrite` [16] | yes [17] | unverified | no [10] |
| Subscription login | "Claude Subscription" terminal auth [18] | ChatGPT login or device code [19] | Devin account browser login, or `devin auth login` credentials [3][20] | "Log in with Google" [4] |

Sources:

1. Claude adapter `initialize` capabilities and method list: [src/acp-agent.ts](https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/acp-agent.ts). It advertises `loadSession`, `sessionCapabilities` `close`, `delete`, `fork`, `list`, `resume`, and `mcpCapabilities` `http` and `sse`. It implements `newSession`, `loadSession`, `resumeSession`, `listSessions`, `cancel`, `closeSession`, `setSessionMode`, and `setSessionConfigOption`. The README lists "Client MCP servers" ([README](https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/README.md)).
2. Codex adapter `initialize`: [src/CodexAcpServer.ts](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/src/CodexAcpServer.ts#L343-L412). It advertises `loadSession`, `sessionCapabilities` `resume`, `list`, `close`, `delete`, `fork`, `additionalDirectories`, and `mcpCapabilities` `http: true`, `sse: false`. The README states "Client-provided MCP servers over command-based stdio config and HTTP transport".
3. Devin command reference, section `devin acp`: [docs.devin.ai](https://docs.devin.ai/cli/reference/commands#devin-acp).
4. Gemini CLI ACP dispatcher: [acpRpcDispatcher.ts](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/cli/src/acp/acpRpcDispatcher.ts#L40-L230). It advertises `loadSession` and `mcpCapabilities` `http` and `sse`, with no `sessionCapabilities`. `session/new` and `session/load` accept `mcpServers` ([acpSessionManager.ts](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/cli/src/acp/acpSessionManager.ts#L58-L175)).
5. ACP registry protocol matrix, probe of 2026-09-25: [latest.md](https://github.com/agentclientprotocol/registry/blob/main/.protocol-matrix/latest.md), [latest.json](https://github.com/agentclientprotocol/registry/blob/main/.protocol-matrix/latest.json).
6. Spec: "All Agents MUST support connecting to MCP servers via stdio" ([session setup](https://agentclientprotocol.com/protocol/v1/session-setup#mcp-servers)).
7. Devin CLI stable changelog ([changelog](https://docs.devin.ai/cli/changelog/stable)). v3000.11.1 (2026-09-21) adds MCP servers from `session/new` and `session/load` "including HTTP and SSE servers". The same release adds model, effort, and speed selection "through session configuration controls". Earlier entries describe ACP permission prompts, session modes, and command output that streams back as session updates.
8. Claude adapter permission requests: [src/acp-agent.ts](https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/acp-agent.ts#L1902-L1910).
9. Codex adapter permission requests: [CodexApprovalHandler.ts](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/src/permissions/CodexApprovalHandler.ts#L51-L105).
10. Gemini CLI session: [acpSession.ts](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/cli/src/acp/acpSession.ts#L771). The ACP code sends only `agent_message_chunk`, `agent_thought_chunk`, `tool_call`, `tool_call_update`, `user_message_chunk`, and `available_commands_update`. It sends no `plan` update. "Plan" exists only as a session mode ([acpUtils.ts](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/cli/src/acp/acpUtils.ts#L224-L250)).
11. Claude adapter model option: [src/session-model.ts](https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/session-model.ts#L216).
12. Codex adapter model option: [src/ModelConfigOption.ts](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/src/ModelConfigOption.ts#L61).
13. Gemini CLI ACP mode doc lists `newSession`, `loadSession`, `prompt`, `cancel`, `setSessionMode`, and `unstable_setSessionModel` ([acp-mode.md](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/docs/cli/acp-mode.md)). Gemini CLI pins the older TypeScript SDK `@agentclientprotocol/sdk` 0.16.1 ([package.json](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/packages/cli/package.json)).
14. Gemini CLI `--model` flag: [cli-reference.md](https://github.com/google-gemini/gemini-cli/blob/v0.61.0/docs/cli/cli-reference.md).
15. Spec method list: [schema/v1/meta.json](https://github.com/agentclientprotocol/agent-client-protocol/blob/v1.9.1/schema/v1/meta.json).
16. Claude adapter plan updates: [src/acp-agent.ts](https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/acp-agent.ts#L10076-L10085).
17. Codex adapter plan updates: [src/CodexEventHandler.ts](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/src/CodexEventHandler.ts#L1176).
18. Claude adapter auth methods `claude-ai-login` ("Use Claude subscription") and `console-login`: [src/acp-agent.ts](https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/src/acp-agent.ts#L2240-L2300).
19. Codex adapter auth methods: [src/CodexAuthMethod.ts](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/src/CodexAuthMethod.ts#L28-L77).
20. Devin in Zed, "Log in with browser": [docs.devin.ai](https://docs.devin.ai/cli/acp/zed).

### Gaps and risks per Harness

- **Gemini CLI**: The model selection uses the older `models` field and the `session/set_model` method. The current v1 schema has neither method in the stable set nor in the unstable set ([meta.json](https://github.com/agentclientprotocol/agent-client-protocol/blob/v1.9.1/schema/v1/meta.json), [meta.unstable.json](https://github.com/agentclientprotocol/agent-client-protocol/blob/v1.9.1/schema/v1/meta.unstable.json)). Gemini CLI has no `session/resume`, `session/list`, `session/close`, or plan updates.
- **Devin**: The ACP server is closed source. Devin reports `agentInfo.version` as `0.0.0-dev` in the probe ([latest.json](https://github.com/agentclientprotocol/registry/blob/main/.protocol-matrix/latest.json)). The third-party client `acpx` sends `clientInfo.name: "windsurf"` to Devin "for compatibility with supported Devin versions" ([acpx Devin notes](https://github.com/openclaw/acpx/blob/main/agents/Devin.md)). This source is not first-party. A Mobius spike must confirm the behavior.
- **Devin credentials**: The command reference says that `devin acp` reads `WINDSURF_API_KEY` or the credentials from `devin auth login` ([command reference](https://docs.devin.ai/cli/reference/commands#devin-acp)). An older changelog entry says that ACP sessions "require host-provided credentials" ([changelog](https://docs.devin.ai/cli/changelog/stable)). A spike must confirm which rule applies to 3000.11.3.
- **Claude Code and Codex**: Each adapter is a separate Node.js package with its own release cycle. Both publish a `preview` channel from each push to `main` ([claude-agent-acp README](https://github.com/agentclientprotocol/claude-agent-acp/blob/v0.81.2/README.md), [codex-acp README](https://github.com/agentclientprotocol/codex-acp/blob/v1.13.1/README.md)).

## Rust crate for the client side

- Crate: [`agent-client-protocol`](https://crates.io/crates/agent-client-protocol). It is the official Rust SDK in [agentclientprotocol/rust-sdk](https://github.com/agentclientprotocol/rust-sdk/tree/v2.2.0). License: Apache-2.0. MSRV: Rust 1.88, edition 2024 ([Cargo.toml](https://github.com/agentclientprotocol/rust-sdk/blob/v2.2.0/Cargo.toml)).
- Latest version: 2.2.0, released 2026-09-18. crates.io reports 4.67 million total downloads ([crates.io API](https://crates.io/api/v1/crates/agent-client-protocol)).
- History: 1.0.0 on 2026-06-24 ([announcement](https://agentclientprotocol.com/announcements/sdk-1-0-releases)), 2.0.0 on 2026-07-23, 2.1.0 on 2026-09-04, 2.2.0 on 2026-09-18 ([crates.io](https://crates.io/crates/agent-client-protocol/versions)).
- 2.0.0 changed the Rust API (transport frames, handler types, router types, and MCP-over-ACP types). It did not change the v1 wire schema ([CHANGELOG](https://github.com/agentclientprotocol/rust-sdk/blob/v2.2.0/src/agent-client-protocol/CHANGELOG.md)).
- The crate covers clients, agents, and proxies. `Client.builder()` is the stable v1 entry point. `.v2()` selects the draft v2 API behind the `unstable_protocol_v2` feature ([README](https://github.com/agentclientprotocol/rust-sdk/blob/v2.2.0/README.md)).
- `AcpAgent::from_str("<command>")` spawns an agent subprocess and connects to it over stdio ([yolo_one_shot_client.rs](https://github.com/agentclientprotocol/rust-sdk/blob/v2.2.0/src/agent-client-protocol/examples/yolo_one_shot_client.rs)).
- Unstable protocol parts are behind Cargo features: `unstable_session_fork`, `unstable_mcp_over_acp`, `unstable_llm_providers`, `unstable_plan_operations`, `unstable_session_compaction`, `unstable_session_notices`, `unstable_end_turn_token_usage` ([Cargo.toml](https://github.com/agentclientprotocol/rust-sdk/blob/v2.2.0/src/agent-client-protocol/Cargo.toml)).
- Zed uses this crate for its external agents ([Rust library page](https://agentclientprotocol.com/libraries/rust)). The Rust library page still names the older `Agent` and `Client` traits. The README and examples at 2.2.0 use the builder API.

## ACP spec version and stability

- The wire protocol version is the integer `1` ([schema/v1/meta.json](https://github.com/agentclientprotocol/agent-client-protocol/blob/v1.9.1/schema/v1/meta.json)).
- The latest v1 JSON schema release is `schema-v1.23.0` (2026-09-18) ([release](https://github.com/agentclientprotocol/agent-client-protocol/releases/tag/schema-v1.23.0)). The v1 schema grows by additive, optional features. Each feature moves from an RFD to "stabilized" ([updates](https://agentclientprotocol.com/updates)).
- Stabilized in 2026: session config options (Feb 4), `session/list` (Mar 9), `session/resume` (Apr 22), `session/close` (Apr 23), `logout` (May 21), `session/delete` (Jun 5), `model_config` category (Jun 24), `$/cancel_request` (Jun 29), elicitation (Jul 22), tool call names (Sep 17) ([updates](https://agentclientprotocol.com/updates)).
- Still unstable in v1: `session/fork`, `providers/*`, MCP-over-ACP (`mcp/*`), next edit suggestions (`nes/*`), and `document/*` ([meta.unstable.json](https://github.com/agentclientprotocol/agent-client-protocol/blob/v1.9.1/schema/v1/meta.unstable.json)).
- ACP v2 is a draft since 2026-07-20. The latest draft schema is `schema-v2.0.0-alpha.5` (2026-09-18). The maintainers say: "gate your implementation behind the version negotiation AND feature flags" and "support both versions side by side". v2 changes the prompt lifecycle, message updates, diffs, and permission requests ([v2 draft announcement](https://agentclientprotocol.com/announcements/acp-v2-draft)).
- Governance: Zed Industries (Ben Brandt) and JetBrains (Sergey Ignatov) lead the project ([updates](https://agentclientprotocol.com/updates)).

## Implications for Mobius

- One ACP v1 client covers all four Harnesses. Use `agent-client-protocol` 2.x. Pin the exact version, because the crate had three major versions in three months.
- Do not use ACP v2 now. All four Harnesses answer with protocol version 1, and v2 is a draft.
- Store one launch command for each Harness: `npx @agentclientprotocol/claude-agent-acp`, `npx @agentclientprotocol/codex-acp`, `devin acp`, `gemini --acp`. The two adapters need Node.js on the Mobius server.
- Build the core flow on the common set: `session/new`, `session/load`, `session/prompt`, `session/cancel`, `session/request_permission`, streamed `session/update`, and stdio MCP servers.
- Use `session/load` to reattach an agent after a Mobius server restart. All four support it. Use `session/resume` only when the Harness advertises `sessionCapabilities.resume` (Claude Code and Codex).
- Set the model through the session config option with category `model` (Claude Code, Codex, Devin). For Gemini CLI, set the model with the `--model` launch flag.
- Inject Mobius MCP servers as stdio servers, or as HTTP servers. Do not use SSE, because Codex does not support it.
- Do not make the UI depend on plan updates. Gemini CLI sends none, and Devin support is unverified.
- The Owner logs in to each Harness with the subscription. Mobius calls `authenticate` only when `session/new` returns "auth required".
- Before the Devin integration, run a spike. Confirm the `clientInfo` requirement, the credential source, the plan updates, and `session/cancel`.
