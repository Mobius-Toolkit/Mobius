CREATE TABLE tasks_new (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    issue INTEGER NOT NULL,
    workstream INTEGER NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('dispatched', 'queued', 'working', 'reviewed', 'needs_human', 'ready_for_review', 'stopped', 'ended', 'checks', 'approval')),
    dispatched_at TEXT NOT NULL,
    branch TEXT,
    queued_at TEXT,
    fix_rounds INTEGER NOT NULL DEFAULT 0,
    pull_request INTEGER,
    judged_at TEXT,
    worker_restarts INTEGER NOT NULL DEFAULT 0,
    worker TEXT,
    worker_input TEXT,
    review_rounds INTEGER NOT NULL DEFAULT 0,
    review_comment INTEGER,
    check_head TEXT
);

INSERT INTO tasks_new (id, repository, issue, workstream, state, dispatched_at, branch, queued_at, fix_rounds, pull_request, judged_at, worker_restarts, worker, worker_input, review_rounds, review_comment, check_head)
SELECT id, repository, issue, workstream, state, dispatched_at, branch, queued_at, fix_rounds, pull_request, judged_at, worker_restarts, worker, worker_input, review_rounds, review_comment, check_head FROM tasks;

DROP TABLE tasks;

ALTER TABLE tasks_new RENAME TO tasks;
