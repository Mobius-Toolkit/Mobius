CREATE TABLE issue_bindings (
    repository TEXT NOT NULL COLLATE NOCASE,
    issue INTEGER NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('implementer', 'lead')),
    harness TEXT NOT NULL,
    model TEXT NOT NULL,
    effort TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (repository, issue, role)
);
