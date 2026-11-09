CREATE TABLE issue_bindings_new (
    repository TEXT NOT NULL COLLATE NOCASE,
    issue INTEGER NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('implementer', 'lead', 'operator')),
    harness TEXT NOT NULL,
    model TEXT NOT NULL,
    effort TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (repository, issue, role)
);

INSERT INTO issue_bindings_new (repository, issue, role, harness, model, effort, updated_at)
SELECT repository, issue, role, harness, model, effort, updated_at FROM issue_bindings;

DROP TABLE issue_bindings;

ALTER TABLE issue_bindings_new RENAME TO issue_bindings;
