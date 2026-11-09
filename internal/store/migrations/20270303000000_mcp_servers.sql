CREATE TABLE mcp_servers (
    id INTEGER PRIMARY KEY,
    organization TEXT NOT NULL COLLATE NOCASE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('http', 'stdio')),
    url TEXT NOT NULL,
    headers TEXT NOT NULL,
    command TEXT NOT NULL,
    args TEXT NOT NULL,
    env TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX mcp_servers_name ON mcp_servers (organization, name);

CREATE TABLE workstream_mcp_servers (
    repository TEXT NOT NULL COLLATE NOCASE,
    workstream INTEGER NOT NULL,
    server INTEGER NOT NULL REFERENCES mcp_servers (id) ON DELETE CASCADE,
    PRIMARY KEY (repository, workstream, server)
);
