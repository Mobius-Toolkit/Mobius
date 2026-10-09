ALTER TABLE sessions ADD COLUMN effort TEXT;

CREATE TABLE turn_usage (
    id INTEGER PRIMARY KEY,
    session INTEGER NOT NULL REFERENCES sessions (id),
    task INTEGER,
    issue INTEGER,
    workstream INTEGER NOT NULL,
    organization TEXT NOT NULL,
    repository TEXT NOT NULL,
    role TEXT NOT NULL,
    harness TEXT NOT NULL,
    model TEXT NOT NULL,
    reported_model TEXT,
    effort TEXT,
    started_at TEXT NOT NULL,
    ended_at TEXT NOT NULL,
    input_tokens INTEGER,
    output_tokens INTEGER,
    cache_read_tokens INTEGER,
    cache_write_tokens INTEGER,
    cost_usd REAL
);
