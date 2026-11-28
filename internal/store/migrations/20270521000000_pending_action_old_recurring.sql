ALTER TABLE pending_actions ADD COLUMN old_recurring_text TEXT NOT NULL DEFAULT '';
ALTER TABLE pending_actions ADD COLUMN old_recurring_cron TEXT NOT NULL DEFAULT '';
