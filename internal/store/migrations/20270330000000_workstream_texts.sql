ALTER TABLE workstreams ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE workstreams ADD COLUMN body TEXT NOT NULL DEFAULT '';
ALTER TABLE workstreams ADD COLUMN state TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed'));
ALTER TABLE workstreams ADD COLUMN created_at TEXT NOT NULL DEFAULT '';

INSERT OR IGNORE INTO workstreams (repository, number)
SELECT repository, number FROM copied_workstreams;

UPDATE workstreams SET state = 'closed'
WHERE NOT EXISTS (SELECT 1 FROM copied_workstreams WHERE copied_workstreams.repository = workstreams.repository AND copied_workstreams.number = workstreams.number);

UPDATE workstreams SET
    title = (SELECT title FROM copied_workstreams WHERE copied_workstreams.repository = workstreams.repository AND copied_workstreams.number = workstreams.number),
    created_at = (SELECT created_at FROM copied_workstreams WHERE copied_workstreams.repository = workstreams.repository AND copied_workstreams.number = workstreams.number),
    body = (SELECT rtrim(body, ' ' || char(9, 13, 10)) FROM copied_workstreams WHERE copied_workstreams.repository = workstreams.repository AND copied_workstreams.number = workstreams.number)
WHERE state = 'open';

-- The head of a text is the text up to its last line break. The Brief loses its last line when that line is the link to the Lead chat.
UPDATE workstreams SET body = rtrim(rtrim(body, replace(body, char(10), '')), ' ' || char(9, 13, 10))
WHERE instr(substr(body, length(rtrim(body, replace(body, char(10), ''))) + 1), '[Open the Lead chat in Mobius](') = 1;

DROP TABLE copied_workstreams;
