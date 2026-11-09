CREATE TABLE role_settings_new (
    role TEXT PRIMARY KEY CHECK (role IN ('lead', 'coordinator', 'triager', 'implementer', 'researcher', 'reviewer', 'judge', 'curator', 'diagnoser', 'operator')),
    harness TEXT,
    model TEXT,
    effort TEXT,
    max INTEGER,
    updated_at TEXT NOT NULL,
    context INTEGER
);

INSERT INTO role_settings_new (role, harness, model, effort, max, updated_at, context)
SELECT role, harness, model, effort, max, updated_at, context FROM role_settings;

DROP TABLE role_settings;

ALTER TABLE role_settings_new RENAME TO role_settings;
