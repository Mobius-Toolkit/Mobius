CREATE TABLE tasks (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    issue INTEGER NOT NULL,
    workstream INTEGER NOT NULL,
    state TEXT NOT NULL,
    dispatched_at TEXT NOT NULL
);
