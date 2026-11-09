CREATE TABLE mcp_server_oauth (
    server INTEGER PRIMARY KEY REFERENCES mcp_servers (id) ON DELETE CASCADE,
    client_id TEXT NOT NULL,
    client_secret TEXT NOT NULL,
    issuer TEXT NOT NULL,
    token_endpoint TEXT NOT NULL,
    resource TEXT NOT NULL,
    scope TEXT NOT NULL,
    state TEXT NOT NULL,
    code_verifier TEXT NOT NULL,
    flow_started_at TEXT,
    access_token TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    expires_at TEXT,
    failure TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
