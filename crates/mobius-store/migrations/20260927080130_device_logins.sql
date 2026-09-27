CREATE TABLE device_logins (
    id INTEGER PRIMARY KEY,
    token_hash BLOB NOT NULL UNIQUE,
    password_fingerprint BLOB NOT NULL,
    user_agent TEXT NOT NULL,
    created_at TEXT NOT NULL
);
