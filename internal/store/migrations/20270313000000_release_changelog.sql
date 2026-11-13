CREATE TABLE last_release (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    tag TEXT NOT NULL
);

CREATE TABLE release_changelogs (
    organization TEXT NOT NULL,
    tag TEXT NOT NULL,
    sent_at TEXT NOT NULL,
    PRIMARY KEY (organization, tag)
);
