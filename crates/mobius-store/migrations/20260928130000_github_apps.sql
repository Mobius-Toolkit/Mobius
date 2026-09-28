CREATE TABLE github_apps (
    app_id INTEGER PRIMARY KEY,
    slug TEXT NOT NULL,
    private_key TEXT NOT NULL,
    client_id TEXT NOT NULL,
    client_secret TEXT NOT NULL,
    user_token TEXT,
    refresh_token TEXT,
    user_token_expires_at TEXT
);
INSERT INTO github_apps
SELECT app_id, slug, private_key, client_id, client_secret, user_token, refresh_token, user_token_expires_at
FROM github_app;
DROP TABLE github_app;
