CREATE TABLE github_app (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    app_id INTEGER NOT NULL,
    slug TEXT NOT NULL,
    private_key TEXT NOT NULL,
    client_id TEXT NOT NULL,
    client_secret TEXT NOT NULL,
    user_token TEXT,
    refresh_token TEXT
);
