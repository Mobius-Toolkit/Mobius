CREATE TABLE sessions (
    id INTEGER PRIMARY KEY,
    role TEXT NOT NULL,
    harness TEXT NOT NULL,
    model TEXT NOT NULL,
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    acp_session_id TEXT,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    end_reason TEXT
);
