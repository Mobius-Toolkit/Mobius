CREATE TABLE implementer_bindings (
    id INTEGER PRIMARY KEY,
    harness TEXT NOT NULL,
    model TEXT NOT NULL,
    effort TEXT NOT NULL,
    context INTEGER NOT NULL,
    max INTEGER NOT NULL,
    updated_at TEXT NOT NULL
);
