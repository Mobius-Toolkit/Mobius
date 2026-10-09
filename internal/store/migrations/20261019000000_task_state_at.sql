ALTER TABLE tasks ADD COLUMN state_at TEXT NOT NULL DEFAULT '';
UPDATE tasks SET state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');
ALTER TABLE tasks ADD COLUMN long_wait_at TEXT;
