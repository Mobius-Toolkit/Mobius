CREATE TABLE inbox_items (
    id INTEGER PRIMARY KEY,
    kind TEXT NOT NULL,
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    issue INTEGER NOT NULL,
    text TEXT NOT NULL,
    link TEXT NOT NULL,
    time TEXT NOT NULL,
    dismissed_at TEXT
);
