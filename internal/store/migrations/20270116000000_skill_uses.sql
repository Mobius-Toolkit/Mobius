CREATE TABLE skill_uses (
    id INTEGER PRIMARY KEY,
    session INTEGER NOT NULL REFERENCES sessions (id),
    organization TEXT NOT NULL,
    repository TEXT NOT NULL,
    role TEXT NOT NULL,
    harness TEXT NOT NULL,
    skill TEXT NOT NULL,
    source TEXT NOT NULL,
    used_at TEXT NOT NULL
);

CREATE INDEX skill_uses_used_at ON skill_uses (used_at);

CREATE TABLE session_skills (
    id INTEGER PRIMARY KEY,
    session INTEGER NOT NULL REFERENCES sessions (id),
    organization TEXT NOT NULL,
    repository TEXT NOT NULL,
    role TEXT NOT NULL,
    harness TEXT NOT NULL,
    skill TEXT NOT NULL,
    copied_at TEXT NOT NULL
);

CREATE INDEX session_skills_copied_at ON session_skills (copied_at);
