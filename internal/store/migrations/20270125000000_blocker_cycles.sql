CREATE TABLE blocker_cycles (
    cycle TEXT NOT NULL,
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    PRIMARY KEY (cycle, repository, workstream)
);
