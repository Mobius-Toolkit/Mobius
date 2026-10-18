CREATE TABLE stop_notices (
    id INTEGER PRIMARY KEY,
    task INTEGER NOT NULL,
    title TEXT NOT NULL,
    reason TEXT NOT NULL,
    text TEXT NOT NULL,
    output TEXT NOT NULL,
    next TEXT NOT NULL,
    lead_text TEXT NOT NULL,
    created_at TEXT NOT NULL
);
