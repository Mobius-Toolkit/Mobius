CREATE TABLE events (
    id INTEGER PRIMARY KEY,
    time TEXT NOT NULL,
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    issue INTEGER NOT NULL,
    actor TEXT NOT NULL,
    text TEXT NOT NULL,
    link TEXT NOT NULL
);
