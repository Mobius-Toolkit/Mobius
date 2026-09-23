# 08 — Human chat

Chat is first-class: humans talk to any agent through a long-lived ACP
session, with streaming, tool calls, permission approvals, and live
model/effort switching.

```mermaid
flowchart LR
    H[Human / SPA] -->|POST /conversations| SM[SessionManager]
    SM -->|resolve Chat profile| PR[ProfileResolver]
    PR --> HS[HarnessSession<br/>devin acp …]
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

1. `POST /api/v1/conversations {agent_id, workdir, title?}` — resolves the
   agent's `Chat` profile, spawns the harness session in `workdir`, persists
   the conversation with the advertised `config_options`.
2. `POST …/messages {text}` — persists the human message, returns it; the
   turn runs in the background.
3. `session/update` notifications accumulate into an agent `Message`'s
   `ContentBlock`s; each `AgentText` chunk emits `MessageDelta` (SSE), each
   completed block persists and emits `EntityChanged`, `TurnFinished` emits
   `MessageAppended`.
4. Permission requests (ask_human policy) park the ACP request on a oneshot,
   emit `PermissionRequested`, and unblock on `POST /api/v1/permissions/{id}
   {option_id}`.
5. `GET/POST …/config` reads/sets live `session/set_config_option` values —
   this is how the UI exposes model and effort pickers mid-conversation.
6. `DELETE /conversations/{id}` closes the session and removes the row;
   server shutdown closes all sessions.

Sessions are process-lifetime in M0 — no resume after restart (the
`acp_session_id` is persisted for that future).
