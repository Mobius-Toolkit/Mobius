CREATE TABLE tool_auto_upgrades (
    package TEXT PRIMARY KEY,
    failed_version TEXT NOT NULL DEFAULT ''
);
