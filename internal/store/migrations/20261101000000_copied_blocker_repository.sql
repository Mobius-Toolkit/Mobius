DROP TABLE copied_blockers;

CREATE TABLE copied_blockers (
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    position INTEGER NOT NULL,
    blocker_repository TEXT NOT NULL,
    number INTEGER NOT NULL,
    blocker_workstream INTEGER,
    blocker_workstream_title TEXT,
    PRIMARY KEY (repository, workstream, position, blocker_repository, number)
);
