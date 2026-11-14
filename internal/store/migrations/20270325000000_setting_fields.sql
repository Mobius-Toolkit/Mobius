CREATE TABLE outgoing_changes_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    integration TEXT NOT NULL,
    repository TEXT NOT NULL,
    number INTEGER NOT NULL,
    reply_to INTEGER,
    field TEXT NOT NULL CHECK (field IN ('comment', 'reply', 'reply_resolve', 'review_comment', 'review_comment_edit', 'approval_comment', 'autopilot', 'auto_merge', 'evergreen', 'maintenance', 'skills')),
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
