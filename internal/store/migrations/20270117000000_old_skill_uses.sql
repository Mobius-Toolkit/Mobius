CREATE TABLE old_skill_uses (
    id INTEGER PRIMARY KEY,
    session INTEGER NOT NULL REFERENCES sessions (id),
    organization TEXT NOT NULL,
    repository TEXT NOT NULL,
    role TEXT NOT NULL,
    harness TEXT NOT NULL,
    skill TEXT NOT NULL,
    used_at TEXT NOT NULL
);

INSERT INTO old_skill_uses (session, organization, repository, role, harness, skill, used_at)
SELECT s.id, s.organization, s.repository, s.role, s.harness, u.skill, u.time
FROM (
    SELECT session, time, skill,
        CASE WHEN claude THEN row_number() OVER (PARTITION BY session, call ORDER BY id) ELSE 1 END AS n
    FROM (
        SELECT id, session, time,
            json_extract(json, '$.update.rawInput.skill') AS skill,
            json_extract(json, '$.update.toolCallId') AS call,
            json_extract(json, '$.update._meta.claudeCode.toolName') = 'Skill' AS claude,
            json_extract(json, '$.update.sessionUpdate') = 'tool_call'
                AND json_extract(json, '$.update._meta."cognition.ai/inferenceToolName"') = 'skill' AS devin
        FROM transcript
        WHERE kind = 'update'
    )
    WHERE (claude OR devin) AND skill <> ''
) AS u
JOIN sessions AS s ON s.id = u.session
WHERE u.n = 1 AND NOT EXISTS (SELECT 1 FROM skill_uses WHERE skill_uses.session = s.id);
