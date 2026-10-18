CREATE TABLE stop_notices (
    id INTEGER PRIMARY KEY,
    task INTEGER NOT NULL,
    title TEXT NOT NULL,
    reason TEXT NOT NULL,
    text TEXT NOT NULL,
    output TEXT NOT NULL,
    next TEXT NOT NULL,
    lead_text TEXT NOT NULL,
    check_title TEXT NOT NULL,
    check_summary TEXT NOT NULL,
    check_create BOOLEAN NOT NULL,
    created_at TEXT NOT NULL
);
