CREATE TABLE harness_pauses (
    harness TEXT PRIMARY KEY,
    paused_until TEXT NOT NULL,
    inbox_item INTEGER NOT NULL
);
