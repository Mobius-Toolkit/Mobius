ALTER TABLE ci_breaks ADD COLUMN issue_row INTEGER;

ALTER TABLE ci_failures ADD COLUMN issue_row INTEGER;

ALTER TABLE check_timeouts ADD COLUMN issue_row INTEGER;

ALTER TABLE init_failures ADD COLUMN issue_row INTEGER;

-- A negative issue is an issue that GitHub created but did not add below the Workstream. It becomes a row of issues
-- below the open maintenance Workstream of the same owner, with a pending parent change that links it.
INSERT OR IGNORE INTO issues (repository, number, title, body, state, author, workstream, kind, metadata)
SELECT u.repository, u.number, '', '', 'open', '', u.workstream, 'task', '{"github":{"id":0,"labels":[]}}'
FROM (
    SELECT n.repository, n.number, (
        SELECT w.id FROM workstreams w
        WHERE w.state = 'open' AND w.number IS NOT NULL AND w.maintenance
          AND w.repository LIKE substr(n.repository, 1, instr(n.repository, '/')) || '%'
        ORDER BY w.number LIMIT 1
    ) AS workstream
    FROM (
        SELECT DISTINCT repository, -issue AS number FROM (
            SELECT repository, issue FROM ci_breaks
            UNION ALL SELECT repository, issue FROM ci_failures
            UNION ALL SELECT repository, issue FROM check_timeouts
            UNION ALL SELECT repository, issue FROM init_failures
        ) WHERE issue < 0
    ) n
) u
WHERE u.workstream IS NOT NULL;

INSERT INTO outgoing_changes (integration, repository, issue, field, status, created_at)
SELECT 'github', i.repository, i.id, 'parent', 'pending', strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM issues i
WHERE i.workstream IS NOT NULL AND EXISTS (
    SELECT 1 FROM (
        SELECT repository, issue FROM ci_breaks
        UNION ALL SELECT repository, issue FROM ci_failures
        UNION ALL SELECT repository, issue FROM check_timeouts
        UNION ALL SELECT repository, issue FROM init_failures
    ) r WHERE r.issue < 0 AND r.repository = i.repository AND -r.issue = i.number
);

UPDATE ci_breaks SET issue = -issue WHERE issue < 0;

UPDATE ci_failures SET issue = -issue WHERE issue < 0;

UPDATE check_timeouts SET issue = -issue WHERE issue < 0;

UPDATE init_failures SET issue = -issue WHERE issue < 0;
