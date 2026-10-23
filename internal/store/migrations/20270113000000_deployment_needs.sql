CREATE TABLE deployment_needs (
  id INTEGER PRIMARY KEY,
  task INTEGER NOT NULL,
  head TEXT NOT NULL,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  path TEXT NOT NULL,
  line INTEGER NOT NULL,
  reason TEXT NOT NULL,
  mandatory INTEGER NOT NULL
);
