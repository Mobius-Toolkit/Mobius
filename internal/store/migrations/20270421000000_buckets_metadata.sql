UPDATE workstreams
SET metadata = json_set(metadata, '$.github', json(json_set(
    coalesce(json_extract(metadata, '$.github'), json_object()),
    '$.buckets', json(coalesce(json_extract(metadata, '$.github.buckets'), '[]')))))
WHERE EXISTS (SELECT 1 FROM workstream_buckets b WHERE b.repository = workstreams.repository AND b.workstream = workstreams.number);

UPDATE workstreams
SET metadata = json_set(metadata, '$.github.buckets', json((
    SELECT json_group_array(json(entry)) FROM (
        SELECT e.value AS entry FROM json_each(workstreams.metadata, '$.github.buckets') e
        WHERE NOT EXISTS (
            SELECT 1 FROM workstream_buckets b
            WHERE b.repository = workstreams.repository AND b.workstream = workstreams.number AND b.issue = json_extract(e.value, '$.number'))
        UNION ALL
        SELECT json_object('number', b.issue, 'state', CASE WHEN b.closed THEN 'closed' ELSE 'open' END, 'title', '', 'pending', json('true'))
        FROM workstream_buckets b
        WHERE b.repository = workstreams.repository AND b.workstream = workstreams.number
    ))))
WHERE EXISTS (SELECT 1 FROM workstream_buckets b WHERE b.repository = workstreams.repository AND b.workstream = workstreams.number);

DROP TABLE workstream_buckets;

CREATE TABLE own_writes_new (
    id INTEGER PRIMARY KEY,
    integration TEXT NOT NULL,
    repository TEXT NOT NULL,
    number INTEGER NOT NULL,
    field TEXT NOT NULL CHECK (field IN ('autopilot', 'auto_merge', 'evergreen', 'maintenance', 'skills', 'workstream', 'state', 'title', 'body', 'parent', 'blockers', 'kind', 'bucket')),
    metadata TEXT NOT NULL,
    created_at TEXT NOT NULL
);

INSERT INTO own_writes_new (id, integration, repository, number, field, metadata, created_at)
SELECT id, integration, repository, number, field, metadata, created_at FROM own_writes;

DROP TABLE own_writes;

ALTER TABLE own_writes_new RENAME TO own_writes;

CREATE INDEX own_writes_item ON own_writes (repository, number);
