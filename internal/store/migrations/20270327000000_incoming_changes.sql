CREATE TABLE incoming_changes (
    id INTEGER PRIMARY KEY,
    integration TEXT NOT NULL,
    external_id TEXT NOT NULL,
    repository TEXT NOT NULL,
    number INTEGER NOT NULL,
    field TEXT NOT NULL CHECK (field IN ('autopilot', 'auto_merge', 'evergreen', 'maintenance', 'skills')),
    change TEXT NOT NULL CHECK (change IN ('labeled', 'unlabeled')),
    actor TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL CHECK (status IN ('pending', 'done')),
    created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX incoming_changes_external ON incoming_changes (integration, external_id);
CREATE INDEX incoming_changes_pending ON incoming_changes (repository) WHERE status = 'pending';

CREATE TABLE own_writes (
    id INTEGER PRIMARY KEY,
    integration TEXT NOT NULL,
    repository TEXT NOT NULL,
    number INTEGER NOT NULL,
    field TEXT NOT NULL CHECK (field IN ('autopilot', 'auto_merge', 'evergreen', 'maintenance', 'skills')),
    metadata TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX own_writes_item ON own_writes (repository, number);
