CREATE TABLE lead_events (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    kind TEXT NOT NULL,
    payload TEXT NOT NULL,
    time TEXT NOT NULL,
    delivered_at TEXT
);
