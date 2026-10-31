ALTER TABLE memory_versions ADD COLUMN file TEXT NOT NULL DEFAULT 'shared';

DROP INDEX memory_versions_repository;
CREATE INDEX memory_versions_repository ON memory_versions (repository, file, id);
