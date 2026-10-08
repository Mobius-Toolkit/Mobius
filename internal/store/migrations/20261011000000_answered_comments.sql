CREATE TABLE answered_comments (
    repository TEXT NOT NULL,
    review BOOLEAN NOT NULL,
    comment INTEGER NOT NULL,
    PRIMARY KEY (repository, review, comment)
);
