CREATE TABLE recurring_tasks (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    cron TEXT NOT NULL,
    text TEXT NOT NULL,
    paused INTEGER NOT NULL DEFAULT 0,
    checked_at TEXT NOT NULL,
    event INTEGER
);

CREATE INDEX recurring_tasks_workstream ON recurring_tasks (repository, workstream);

CREATE TABLE recurring_task_runs (
    id INTEGER PRIMARY KEY,
    recurring_task INTEGER NOT NULL,
    time TEXT NOT NULL,
    result TEXT NOT NULL,
    reason TEXT NOT NULL
);

CREATE INDEX recurring_task_runs_task ON recurring_task_runs (recurring_task, time);
