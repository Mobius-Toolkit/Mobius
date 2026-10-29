CREATE TABLE role_settings (
    role TEXT PRIMARY KEY CHECK (role IN ('lead', 'coordinator', 'triager', 'implementer', 'researcher', 'reviewer', 'judge', 'curator', 'diagnoser')),
    harness TEXT,
    model TEXT,
    effort TEXT,
    max INTEGER,
    updated_at TEXT NOT NULL
);
