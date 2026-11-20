ALTER TABLE issues ADD COLUMN triage INTEGER NOT NULL DEFAULT 0;

UPDATE issues SET triage = 1
WHERE EXISTS (SELECT 1 FROM json_each(issues.metadata, '$.github.labels') l WHERE l.value = 'mobius:triage');

CREATE TABLE incoming_changes_new (
    id INTEGER PRIMARY KEY,
    integration TEXT NOT NULL,
    external_id TEXT NOT NULL,
    repository TEXT NOT NULL,
    number INTEGER NOT NULL,
    field TEXT NOT NULL CHECK (field IN ('autopilot', 'auto_merge', 'evergreen', 'maintenance', 'skills', 'workstream', 'state', 'title', 'body', 'parent', 'blockers', 'kind', 'triage')),
    change TEXT NOT NULL CHECK (change IN ('labeled', 'unlabeled', 'closed', 'reopened', 'renamed', 'edited', 'added', 'removed')),
    actor TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL CHECK (status IN ('pending', 'done')),
    created_at TEXT NOT NULL
);

INSERT INTO incoming_changes_new (id, integration, external_id, repository, number, field, change, actor, metadata, status, created_at)
SELECT id, integration, external_id, repository, number, field, change, actor, metadata, status, created_at FROM incoming_changes;

DROP TABLE incoming_changes;

ALTER TABLE incoming_changes_new RENAME TO incoming_changes;

CREATE UNIQUE INDEX incoming_changes_external ON incoming_changes (integration, external_id);
CREATE INDEX incoming_changes_pending ON incoming_changes (repository) WHERE status = 'pending';

CREATE TABLE own_writes_new (
    id INTEGER PRIMARY KEY,
    integration TEXT NOT NULL,
    repository TEXT NOT NULL,
    number INTEGER NOT NULL,
    field TEXT NOT NULL CHECK (field IN ('autopilot', 'auto_merge', 'evergreen', 'maintenance', 'skills', 'workstream', 'state', 'title', 'body', 'parent', 'blockers', 'kind', 'bucket', 'triage')),
    metadata TEXT NOT NULL,
    created_at TEXT NOT NULL
);

INSERT INTO own_writes_new (id, integration, repository, number, field, metadata, created_at)
SELECT id, integration, repository, number, field, metadata, created_at FROM own_writes;

DROP TABLE own_writes;

ALTER TABLE own_writes_new RENAME TO own_writes;

CREATE INDEX own_writes_item ON own_writes (repository, number);

CREATE TABLE outgoing_changes_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    integration TEXT NOT NULL,
    repository TEXT NOT NULL,
    number INTEGER,
    workstream INTEGER,
    issue INTEGER,
    reply_to INTEGER,
    field TEXT NOT NULL CHECK (field IN ('comment', 'reply', 'reply_resolve', 'review_comment', 'review_comment_edit', 'approval_comment', 'autopilot', 'auto_merge', 'evergreen', 'maintenance', 'skills', 'body', 'state', 'workstream', 'title', 'create', 'blockers', 'parent', 'kind', 'triage')),
    text TEXT,
    status TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'refused')),
    created_at TEXT NOT NULL
);

INSERT INTO outgoing_changes_new (id, integration, repository, number, workstream, issue, reply_to, field, text, status, created_at)
SELECT id, integration, repository, number, workstream, issue, reply_to, field, text, status, created_at FROM outgoing_changes;

DELETE FROM sqlite_sequence WHERE name = 'outgoing_changes_new';

INSERT INTO sqlite_sequence (name, seq)
SELECT 'outgoing_changes_new', seq FROM sqlite_sequence WHERE name = 'outgoing_changes';

DROP TABLE outgoing_changes;

ALTER TABLE outgoing_changes_new RENAME TO outgoing_changes;

CREATE INDEX outgoing_changes_pending ON outgoing_changes (repository) WHERE status = 'pending';
