CREATE TABLE workstream_buckets (
  repository TEXT NOT NULL,
  workstream INTEGER NOT NULL,
  issue INTEGER NOT NULL,
  closed INTEGER NOT NULL,
  PRIMARY KEY (repository, issue)
);
