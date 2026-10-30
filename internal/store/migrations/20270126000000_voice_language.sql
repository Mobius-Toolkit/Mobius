CREATE TABLE voice_language (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    language TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
