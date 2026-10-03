CREATE TABLE copied_workstreams (
    repository TEXT NOT NULL,
    number INTEGER NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    autopilot INTEGER NOT NULL,
    PRIMARY KEY (repository, number)
);

-- The position is the place of the issue in the depth-first walk of the Workstream tree.
CREATE TABLE copied_issues (
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    position INTEGER NOT NULL,
    number INTEGER NOT NULL,
    parent INTEGER NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    state TEXT NOT NULL,
    author TEXT NOT NULL,
    html_url TEXT NOT NULL,
    repository_url TEXT NOT NULL,
    PRIMARY KEY (repository, workstream, position)
);

CREATE TABLE copied_issue_labels (
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    position INTEGER NOT NULL,
    name TEXT NOT NULL,
    PRIMARY KEY (repository, workstream, position, name)
);

CREATE TABLE copied_blockers (
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    position INTEGER NOT NULL,
    number INTEGER NOT NULL,
    blocker_workstream INTEGER,
    blocker_workstream_title TEXT,
    PRIMARY KEY (repository, workstream, position, number)
);
