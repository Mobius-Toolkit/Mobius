CREATE TABLE ci_failures (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    reason TEXT NOT NULL,
    test TEXT NOT NULL DEFAULT '',
    location TEXT NOT NULL DEFAULT '',
    issue INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    ended_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE ci_failure_places (
    id INTEGER PRIMARY KEY,
    failure INTEGER NOT NULL,
    pull_request INTEGER NOT NULL DEFAULT 0,
    ci_break INTEGER NOT NULL DEFAULT 0,
    head TEXT NOT NULL,
    job_urls TEXT NOT NULL,
    lines TEXT NOT NULL,
    created_at TEXT NOT NULL,
    branch_updated_at TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX ci_failure_places_place ON ci_failure_places (failure, pull_request, ci_break);

CREATE TABLE ci_diagnoses (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    pull_request INTEGER NOT NULL DEFAULT 0,
    ci_break INTEGER NOT NULL DEFAULT 0,
    head TEXT NOT NULL,
    reasons TEXT NOT NULL DEFAULT '',
    session INTEGER,
    created_at TEXT NOT NULL,
    ended_at TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX ci_diagnoses_place ON ci_diagnoses (repository, pull_request, ci_break, head);
