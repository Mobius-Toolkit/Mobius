CREATE TABLE pending_actions (
    id INTEGER PRIMARY KEY,
    organization TEXT NOT NULL,
    repository TEXT NOT NULL,
    workstream INTEGER NOT NULL,
    issue INTEGER NOT NULL,
    role TEXT NOT NULL,
    tool TEXT NOT NULL,
    arguments TEXT NOT NULL,
    state TEXT NOT NULL,
    running INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    result TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    time TEXT NOT NULL,
    decided_at TEXT,
    chat_message INTEGER,
    inbox_item INTEGER
);
CREATE INDEX pending_actions_chat_message ON pending_actions (chat_message);
CREATE INDEX pending_actions_inbox_item ON pending_actions (inbox_item);
