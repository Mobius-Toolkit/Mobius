UPDATE inbox_items SET dismissed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE dismissed_at IS NULL;

ALTER TABLE inbox_items ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE inbox_items ADD COLUMN reason TEXT NOT NULL DEFAULT '';
ALTER TABLE inbox_items ADD COLUMN pull_request INTEGER NOT NULL DEFAULT 0;
ALTER TABLE inbox_items ADD COLUMN report_link TEXT NOT NULL DEFAULT '';
