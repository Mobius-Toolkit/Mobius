CREATE TABLE lead_writes (
  id INTEGER PRIMARY KEY,
  session INTEGER NOT NULL,
  repository TEXT NOT NULL,
  issue INTEGER NOT NULL,
  kind TEXT NOT NULL,
  labels TEXT NOT NULL,
  text TEXT NOT NULL,
  created_at TEXT NOT NULL,
  item INTEGER
);
