CREATE TABLE workstreams (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    number INTEGER NOT NULL,
    autopilot INTEGER NOT NULL DEFAULT 0,
    auto_merge INTEGER NOT NULL DEFAULT 0,
    evergreen INTEGER NOT NULL DEFAULT 0,
    maintenance INTEGER NOT NULL DEFAULT 0,
    skills INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX workstreams_repository_number ON workstreams (repository, number);

INSERT INTO workstreams (repository, number, autopilot, auto_merge, evergreen, maintenance, skills)
SELECT repository, number, autopilot, auto_merge, evergreen, maintenance, skills FROM copied_workstreams;

ALTER TABLE copied_workstreams DROP COLUMN autopilot;
ALTER TABLE copied_workstreams DROP COLUMN auto_merge;
ALTER TABLE copied_workstreams DROP COLUMN evergreen;
ALTER TABLE copied_workstreams DROP COLUMN maintenance;
ALTER TABLE copied_workstreams DROP COLUMN skills;
