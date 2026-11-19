CREATE TABLE workstreams_new (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    number INTEGER,
    autopilot INTEGER NOT NULL DEFAULT 0,
    auto_merge INTEGER NOT NULL DEFAULT 0,
    evergreen INTEGER NOT NULL DEFAULT 0,
    maintenance INTEGER NOT NULL DEFAULT 0,
    skills INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed')),
    created_at TEXT NOT NULL DEFAULT '',
    close_pending INTEGER NOT NULL DEFAULT 0
);

INSERT INTO workstreams_new (id, repository, number, autopilot, auto_merge, evergreen, maintenance, skills, title, body, state, created_at, close_pending)
SELECT id, repository, number, autopilot, auto_merge, evergreen, maintenance, skills, title, body, state, created_at, close_pending FROM workstreams;

DROP TABLE workstreams;

ALTER TABLE workstreams_new RENAME TO workstreams;

CREATE UNIQUE INDEX workstreams_repository_number ON workstreams (repository, number);

CREATE TABLE outgoing_changes_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    integration TEXT NOT NULL,
    repository TEXT NOT NULL,
    number INTEGER,
    workstream INTEGER,
    reply_to INTEGER,
    field TEXT NOT NULL CHECK (field IN ('comment', 'reply', 'reply_resolve', 'review_comment', 'review_comment_edit', 'approval_comment', 'autopilot', 'auto_merge', 'evergreen', 'maintenance', 'skills', 'body', 'state', 'workstream', 'title', 'create')),
    text TEXT,
    status TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'refused')),
    created_at TEXT NOT NULL
);

INSERT INTO outgoing_changes_new (id, integration, repository, number, reply_to, field, text, status, created_at)
SELECT id, integration, repository, number, reply_to, field, text, status, created_at FROM outgoing_changes;

DELETE FROM sqlite_sequence WHERE name = 'outgoing_changes_new';

INSERT INTO sqlite_sequence (name, seq)
SELECT 'outgoing_changes_new', seq FROM sqlite_sequence WHERE name = 'outgoing_changes';

DROP TABLE outgoing_changes;

ALTER TABLE outgoing_changes_new RENAME TO outgoing_changes;

CREATE INDEX outgoing_changes_pending ON outgoing_changes (repository) WHERE status = 'pending';

ALTER TABLE pending_actions ADD COLUMN created_workstream INTEGER;
ALTER TABLE pending_actions DROP COLUMN created_issue;
