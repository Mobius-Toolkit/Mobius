ALTER TABLE ci_breaks ADD COLUMN issue_row INTEGER;

ALTER TABLE ci_failures ADD COLUMN issue_row INTEGER;

ALTER TABLE check_timeouts ADD COLUMN issue_row INTEGER;

ALTER TABLE init_failures ADD COLUMN issue_row INTEGER;
