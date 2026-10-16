CREATE TABLE check_timeouts (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    task_issue INTEGER NOT NULL,
    session INTEGER NOT NULL,
    timeout TEXT NOT NULL,
    output TEXT NOT NULL,
    issue INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    ended_at TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX check_timeouts_open ON check_timeouts (repository) WHERE ended_at = '';
