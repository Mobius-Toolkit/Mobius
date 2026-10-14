CREATE TABLE ci_breaks (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    workflow_id INTEGER NOT NULL,
    workflow_name TEXT NOT NULL,
    run_id INTEGER NOT NULL,
    run_url TEXT NOT NULL,
    run_attempt INTEGER NOT NULL,
    rerun_at TEXT NOT NULL DEFAULT '',
    result TEXT NOT NULL DEFAULT '' CHECK (result IN ('', 'flaky', 'hard')),
    ended_run_id INTEGER NOT NULL DEFAULT 0,
    ended_at TEXT NOT NULL DEFAULT '',
    issue INTEGER NOT NULL DEFAULT 0,
    started_at TEXT NOT NULL
);

CREATE UNIQUE INDEX ci_breaks_open ON ci_breaks (repository, workflow_id) WHERE ended_at = '';
