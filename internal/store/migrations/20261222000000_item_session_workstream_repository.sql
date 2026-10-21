ALTER TABLE inbox_items ADD COLUMN workstream_repository TEXT NOT NULL DEFAULT '';
UPDATE inbox_items SET workstream_repository = repository;
ALTER TABLE sessions ADD COLUMN workstream_repository TEXT NOT NULL DEFAULT '';
UPDATE sessions SET workstream_repository = repository;
