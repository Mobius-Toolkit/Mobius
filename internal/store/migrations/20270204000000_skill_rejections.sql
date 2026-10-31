CREATE TABLE skill_rejections (
    id INTEGER PRIMARY KEY,
    organization TEXT NOT NULL,
    name TEXT NOT NULL,
    reason TEXT NOT NULL,
    rejected_at TEXT NOT NULL,
    UNIQUE (organization, name)
);
