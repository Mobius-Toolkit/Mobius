ALTER TABLE harness_pauses DROP COLUMN inbox_item;

UPDATE inbox_items SET dismissed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE kind = 'usage limit' AND dismissed_at IS NULL;
