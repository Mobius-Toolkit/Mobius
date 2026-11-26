-- The Coordinator has the repository '' and the workstream 0.
CREATE TABLE drain_refusals (
    id INTEGER PRIMARY KEY,
    organization TEXT NOT NULL,
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    tool TEXT NOT NULL,
    arguments TEXT NOT NULL,
    created_at TEXT NOT NULL
);
