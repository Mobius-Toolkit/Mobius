CREATE TABLE owner_answers (
    repository TEXT NOT NULL,
    comment_id INTEGER NOT NULL,
    PRIMARY KEY (repository, comment_id)
);
