# 08 — Human chat

Chat is first-class: humans talk to the organization or a project
coordinator through a long-lived ACP session, with streaming, tool calls,
permission approvals, and live model/effort switching.

```mermaid
flowchart LR
    H[Human / SPA] -->|POST /conversations| SM[SessionManager]
    SM -->|resolve org + coordinator| PR[AgentProvisioner]
    SM --> HS[HarnessSession<br/>devin acp …]
    H -->|POST …/messages| SM
    HS -->|session/update| SM
    SM -->|persist blocks| DB[(messages)]
    SM -->|MessageDelta / MessageAppended /<br/>PermissionRequested| SSE[SSE /api/v1/events?conversation_id]
    SSE --> H
    HS -->|request_permission parked| SM
    H -->|POST /permissions/{id}| SM
    SM -->|respond_permission| HS
```

## Flow

1. `POST /api/v1/conversations {organization_id?, project_id?, title?,
   model_profile_id?}` — `OpenConversation::chat` resolves the organization
   (given → from project → the single org, else `Config` error), provisions
   the org or project coordinator, and spawns the session in the org memory
   directory under `ReadOnly` (coordinators) — there is no workdir or agent
   picker.
2. Every chat opens a **fresh ACP session**. Context comes from the preamble
   (role + org + entity catalogue + grouped memory + `mobius` CLI docs +
   agent instructions), never from other chats' transcripts. On restart the
   harness is re-spawned with a resume preamble for re-attach.
3. `POST …/messages {text}` — persists the human message, returns it; the
   turn runs in the background. The first user message auto-titles the
   conversation.
4. `session/update` notifications accumulate into an agent `Message`'s
   `ContentBlock`s; each `AgentText` chunk emits `MessageDelta` (SSE), each
   completed block persists and emits `EntityChanged`, `TurnFinished` emits
   `MessageAppended`.
5. Permission requests (ask_human policy) park the ACP request on a oneshot,
   emit `PermissionRequested`, and unblock on `POST /api/v1/permissions/{id}
   {option_id}`.
6. `GET/POST …/config` reads/sets live `session/set_config_option` values —
   this is how the UI exposes model and effort pickers mid-conversation.
7. `DELETE /conversations/{id}` closes the session and removes the row;
   server shutdown closes all sessions.

## Conversation kinds

`Chat` conversations are the human surface. `Run` and `Research`
conversations carry executor transcripts — they are not in the sidebar; the
UI opens them from the Tasks/Research panels and the Work/Runs pages. The
composer stays live on `Run` conversations (the human can steer the
implementer); `Research` transcripts are read-only.

## UI layout

The chat page groups the sidebar into "Organization: <name>" plus one group
per project (chat-kind only, `updated_at` desc, status dot). The header is a
breadcrumb (`Org / project · title`) plus config selectors, Cancel, and
**Memory (n) / Research (n) / Tasks (n)** panel toggles that open a right
column scoped to the conversation's project (or organization).

Sessions are process-lifetime in M1 — no resume after restart (the
`acp_session_id` is persisted for that future).
