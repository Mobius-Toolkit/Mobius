CREATE TABLE memory_versions (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    time TEXT NOT NULL,
    author TEXT NOT NULL CHECK (author IN ('curator', 'owner')),
    text TEXT NOT NULL
);

CREATE INDEX memory_versions_repository ON memory_versions (repository, id);
