CREATE TABLE issues (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL COLLATE NOCASE,
    number INTEGER,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('open', 'closed')),
    author TEXT NOT NULL,
    workstream INTEGER,
    parent INTEGER,
    kind TEXT NOT NULL DEFAULT 'task' CHECK (kind IN ('task', 'project')),
    metadata TEXT NOT NULL DEFAULT '{}'
);

CREATE UNIQUE INDEX issues_repository_number ON issues (repository, number);

CREATE INDEX issues_workstream ON issues (workstream);

CREATE TABLE blockers (
    issue INTEGER NOT NULL,
    blocker INTEGER NOT NULL,
    PRIMARY KEY (issue, blocker)
);

CREATE INDEX blockers_blocker ON blockers (blocker);

ALTER TABLE workstreams ADD COLUMN metadata TEXT NOT NULL DEFAULT '{}';

DROP TABLE copied_issues;
DROP TABLE copied_issue_labels;
DROP TABLE copied_blockers;
