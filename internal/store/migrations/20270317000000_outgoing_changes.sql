CREATE TABLE outgoing_changes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    integration TEXT NOT NULL,
    repository TEXT NOT NULL,
    number INTEGER NOT NULL,
    reply_to INTEGER,
    field TEXT NOT NULL CHECK (field IN ('comment', 'reply', 'review_comment', 'review_comment_edit')),
    text TEXT,
    status TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'refused')),
    created_at TEXT NOT NULL
);
CREATE INDEX outgoing_changes_pending ON outgoing_changes (repository) WHERE status = 'pending';
DROP TABLE comment_writes;
