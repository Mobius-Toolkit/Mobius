CREATE TABLE sync_cursors (
    repository TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    since TEXT,
    etag TEXT,
    PRIMARY KEY (repository, endpoint)
);
