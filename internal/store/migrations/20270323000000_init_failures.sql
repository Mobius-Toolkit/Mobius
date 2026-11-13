CREATE TABLE init_failures (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    task_issue INTEGER NOT NULL,
    session INTEGER NOT NULL,
    commit_sha TEXT NOT NULL,
    result TEXT NOT NULL CHECK (result IN ('fail', 'timeout')),
    output TEXT NOT NULL,
    issue INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    ended_at TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX init_failures_open ON init_failures (repository) WHERE ended_at = '';
