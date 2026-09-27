CREATE TABLE chat_messages (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    author TEXT NOT NULL,
    time TEXT NOT NULL,
    text TEXT NOT NULL
);

CREATE TABLE chat_seen (
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    message INTEGER NOT NULL,
    PRIMARY KEY (repository, workstream)
);
