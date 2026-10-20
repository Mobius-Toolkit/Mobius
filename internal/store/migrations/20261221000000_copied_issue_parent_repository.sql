ALTER TABLE copied_issues ADD COLUMN parent_repository TEXT NOT NULL DEFAULT '';
UPDATE copied_issues SET parent_repository = repository;
