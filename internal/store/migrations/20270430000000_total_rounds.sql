ALTER TABLE tasks ADD COLUMN total_fix_rounds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN total_review_rounds INTEGER NOT NULL DEFAULT 0;
UPDATE tasks SET total_fix_rounds = fix_rounds, total_review_rounds = review_rounds;
