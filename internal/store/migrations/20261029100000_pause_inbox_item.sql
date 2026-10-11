ALTER TABLE harness_pauses DROP COLUMN inbox_item;

CREATE TABLE harness_pause_ends (
    harness TEXT PRIMARY KEY,
    ended_at TEXT NOT NULL
);

UPDATE inbox_items SET dismissed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE kind = 'usage limit' AND dismissed_at IS NULL;
