ALTER TABLE tasks ADD COLUMN queued_at TEXT;
ALTER TABLE sessions ADD COLUMN queue_reason TEXT;
