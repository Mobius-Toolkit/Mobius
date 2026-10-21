ALTER TABLE lead_events ADD COLUMN issue_repository TEXT NOT NULL DEFAULT '';
UPDATE lead_events SET issue_repository = repository WHERE issue IS NOT NULL;
