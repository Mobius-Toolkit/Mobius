CREATE TABLE network_pause (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    lost_at TEXT NOT NULL
);
