CREATE INDEX transcript_kind ON transcript (kind);
CREATE INDEX sessions_repository_role ON sessions (repository, role);
CREATE INDEX tasks_issue ON tasks (repository, issue);
CREATE INDEX lead_events_workstream ON lead_events (repository, workstream);
CREATE INDEX lead_events_issue ON lead_events (repository, issue_repository, issue);
