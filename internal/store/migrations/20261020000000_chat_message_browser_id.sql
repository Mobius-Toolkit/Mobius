ALTER TABLE chat_messages ADD COLUMN browser_id TEXT;
ALTER TABLE chat_messages ADD COLUMN delivered_at TEXT;
CREATE UNIQUE INDEX chat_messages_browser_id ON chat_messages (browser_id);
