-- The id of the session that started this session. `NULL` for a session that no agent started.
ALTER TABLE sessions ADD COLUMN parent INTEGER;
