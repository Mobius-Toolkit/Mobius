ALTER TABLE tasks ADD COLUMN workstream_repository TEXT NOT NULL DEFAULT '';
UPDATE tasks SET workstream_repository = repository;
