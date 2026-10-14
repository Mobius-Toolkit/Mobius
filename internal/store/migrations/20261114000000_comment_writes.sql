CREATE TABLE comment_writes (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    number INTEGER,
    comment INTEGER,
    body TEXT NOT NULL
);
