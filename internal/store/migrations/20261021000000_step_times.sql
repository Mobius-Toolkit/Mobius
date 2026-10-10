CREATE TABLE step_times (
    id INTEGER PRIMARY KEY,
    kind TEXT NOT NULL,
    session INTEGER REFERENCES sessions (id),
    task INTEGER,
    issue INTEGER,
    workstream INTEGER NOT NULL,
    organization TEXT NOT NULL,
    repository TEXT NOT NULL,
    role TEXT NOT NULL,
    harness TEXT NOT NULL,
    model TEXT NOT NULL,
    effort TEXT,
    started_at TEXT NOT NULL,
    ended_at TEXT NOT NULL,
    attempt INTEGER,
    result TEXT
);
