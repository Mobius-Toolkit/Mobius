CREATE TABLE full_slots (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    since TEXT NOT NULL,
    item_at TEXT
);
