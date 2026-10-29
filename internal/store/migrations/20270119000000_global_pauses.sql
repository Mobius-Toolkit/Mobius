CREATE TABLE global_pauses (
    kind TEXT PRIMARY KEY CHECK (kind IN ('start', 'merge', 'repairs')),
    paused_at TEXT NOT NULL
);

CREATE TABLE held_work (
    kind TEXT NOT NULL CHECK (kind IN ('start', 'merge', 'repairs')),
    repository TEXT NOT NULL,
    issue INTEGER NOT NULL,
    held_at TEXT NOT NULL,
    PRIMARY KEY (kind, repository, issue)
);
