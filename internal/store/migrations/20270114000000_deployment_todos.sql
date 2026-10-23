CREATE TABLE deployment_todos (
  id INTEGER PRIMARY KEY,
  task INTEGER NOT NULL,
  inbox_item INTEGER NOT NULL,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  path TEXT NOT NULL,
  line INTEGER NOT NULL,
  reason TEXT NOT NULL,
  mandatory INTEGER NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  done_at TEXT,
  UNIQUE (task, kind, name)
);
