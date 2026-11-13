CREATE TABLE missing_workstream_dismissals (
    organization TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('missing maintenance', 'missing skills')),
    PRIMARY KEY (organization, kind)
);
