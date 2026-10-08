CREATE TABLE held_triagers (
    repository TEXT NOT NULL,
    issue INTEGER NOT NULL,
    PRIMARY KEY (repository, issue)
);
