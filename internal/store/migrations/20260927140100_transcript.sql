CREATE TABLE transcript (
    id INTEGER PRIMARY KEY,
    session INTEGER NOT NULL REFERENCES sessions (id),
    time TEXT NOT NULL,
    kind TEXT NOT NULL,
    json TEXT NOT NULL
);
