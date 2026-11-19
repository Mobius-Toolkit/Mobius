-- name: ListCopiedWorkstreams :many
SELECT w.repository, CAST(w.number AS INTEGER) AS number, w.title, w.body, CAST(w.autopilot AS BOOLEAN) AS autopilot,
       CAST(w.auto_merge AS BOOLEAN) AS auto_merge,
       CAST(w.evergreen AS BOOLEAN) AS evergreen,
       CAST(EXISTS (SELECT 1 FROM issues i WHERE i.workstream = w.id AND i.parent IS NULL)
       AND NOT EXISTS (SELECT 1 FROM issues i WHERE i.workstream = w.id AND i.parent IS NULL AND i.state != 'closed') AS BOOLEAN) AS all_tasks_closed,
       CAST(w.finished_at IS NOT NULL
       AND NOT EXISTS (SELECT 1 FROM issues i WHERE i.workstream = w.id AND i.parent IS NULL) AS BOOLEAN) AS reported_finished,
       CAST(EXISTS (SELECT 1 FROM tasks t
               WHERE t.workstream_repository = w.repository AND t.workstream = w.number AND t.state = 'ready_for_review') AS BOOLEAN) AS ready_to_merge
FROM workstreams w
WHERE w.state = 'open' AND w.number IS NOT NULL
ORDER BY w.repository, w.number DESC;

-- name: AddDeviceLogin :exec
INSERT INTO device_logins (token_hash, password_fingerprint, user_agent, created_at)
VALUES (?, ?, ?, ?);

-- name: FindDeviceLogin :one
SELECT id FROM device_logins WHERE token_hash = ?;

-- name: ListDeviceLogins :many
SELECT id, user_agent, created_at FROM device_logins ORDER BY id DESC;

-- name: DeleteDeviceLogin :exec
DELETE FROM device_logins WHERE id = ?;

-- name: DeleteOtherPasswordLogins :exec
DELETE FROM device_logins WHERE password_fingerprint != ?;

-- name: AddGitHubApp :exec
INSERT INTO github_apps (app_id, slug, private_key, client_id, client_secret)
VALUES (?, ?, ?, ?, ?);

-- name: ListGitHubApps :many
SELECT * FROM github_apps ORDER BY app_id;

-- name: GetGitHubApp :one
SELECT * FROM github_apps WHERE app_id = ?;

-- name: SetUserTokens :exec
UPDATE github_apps SET user_token = ?, refresh_token = ?, user_token_expires_at = ?
WHERE app_id = ?;

-- name: GetSyncCursor :one
SELECT since, etag FROM sync_cursors WHERE repository = ? AND endpoint = ?;

-- name: SetSyncCursor :exec
INSERT INTO sync_cursors (repository, endpoint, since, etag) VALUES (?, ?, ?, ?)
ON CONFLICT (repository, endpoint) DO UPDATE SET since = excluded.since, etag = excluded.etag;

-- name: DeleteSyncCursor :exec
DELETE FROM sync_cursors WHERE repository = ? AND endpoint = ?;

-- name: AddSession :one
INSERT INTO sessions (role, harness, model, effort, organization, repository, workstream_repository, workstream, issue, parent, started_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: AddTurnUsage :exec
INSERT INTO turn_usage (session, task, issue, workstream, organization, repository, role, harness, model, reported_model,
                        effort, started_at, ended_at, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: AddSkillUse :exec
INSERT INTO skill_uses (session, organization, repository, role, harness, skill, source, used_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListOldSkillUses :many
SELECT * FROM old_skill_uses ORDER BY id;

-- name: DeleteOldSkillUse :exec
DELETE FROM old_skill_uses WHERE id = ?;

-- name: AddSessionSkill :exec
INSERT INTO session_skills (session, organization, repository, role, harness, skill, copied_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListTurnUsageBetween :many
SELECT t.*, s.context_used AS session_context_used, s.context_size AS session_context_size
FROM turn_usage t
LEFT JOIN sessions s ON s.id = t.session
WHERE t.started_at >= sqlc.arg(after) AND t.started_at < sqlc.arg(before);

-- name: ListSkillUsesBetween :many
SELECT * FROM skill_uses WHERE used_at >= sqlc.arg(after) AND used_at < sqlc.arg(before);

-- name: ListSessionSkillsBetween :many
SELECT * FROM session_skills WHERE copied_at >= sqlc.arg(after) AND copied_at < sqlc.arg(before);

-- name: ListStepTimesBetween :many
SELECT * FROM step_times WHERE started_at >= sqlc.arg(after) AND started_at < sqlc.arg(before);

-- name: ListTaskStatesBetween :many
SELECT * FROM task_states WHERE started_at >= sqlc.arg(after) AND started_at < sqlc.arg(before);

-- name: ListReportedModelsBetween :many
SELECT session, reported_model FROM turn_usage
WHERE id IN (
    SELECT MAX(id) FROM turn_usage
    WHERE reported_model IS NOT NULL AND session IN (
        SELECT step_times.session FROM step_times
        WHERE step_times.started_at >= sqlc.arg(after) AND step_times.started_at < sqlc.arg(before)
        UNION
        SELECT task_states.session FROM task_states
        WHERE task_states.started_at >= sqlc.arg(after) AND task_states.started_at < sqlc.arg(before)
    )
    GROUP BY session
);

-- name: ListEndedTaskQuality :many
WITH ranked AS (
    SELECT ts.task, ts.started_at, ts.ended_at,
           ROW_NUMBER() OVER (PARTITION BY ts.task ORDER BY ts.id) AS first_rank,
           ROW_NUMBER() OVER (PARTITION BY ts.task ORDER BY julianday(ts.ended_at) DESC) AS last_rank
    FROM task_states ts
),
bounds AS (
    SELECT t.id AS task, t.repository AS repository, t.issue AS issue,
           MAX(CASE WHEN r.first_rank = 1 THEN r.started_at END) AS started_at,
           MAX(CASE WHEN r.last_rank = 1 THEN r.ended_at END) AS ended_at
    FROM tasks t
    JOIN ranked r ON r.task = t.id
    WHERE t.state = 'ended'
    GROUP BY t.id
),
working AS (
    SELECT ts.task, ts.started_at, ts.harness, ts.model, ts.effort
    FROM (
        SELECT ts.task, ts.started_at, ts.harness, ts.model, ts.effort,
               ROW_NUMBER() OVER (PARTITION BY ts.task ORDER BY ts.id) AS rn
        FROM task_states ts
        WHERE ts.state = 'working'
    ) ts
    WHERE ts.rn = 1
),
states AS (
    SELECT ts.task,
           SUM(CASE WHEN ts.state = 'needs_human' THEN 1 ELSE 0 END) AS needs_human,
           SUM(CASE WHEN ts.state = 'asked' THEN 1 ELSE 0 END) AS questions
    FROM task_states ts
    GROUP BY ts.task
),
usage AS (
    SELECT tu.task,
           SUM(tu.input_tokens) AS input_tokens,
           SUM(tu.output_tokens) AS output_tokens,
           SUM(tu.cache_read_tokens) AS cache_read_tokens,
           SUM(tu.cache_write_tokens) AS cache_write_tokens,
           SUM(tu.cost_usd) AS cost_usd
    FROM turn_usage tu
    GROUP BY tu.task
),
rounds AS (
    SELECT b.task,
           SUM(CASE WHEN s.role = 'reviewer' AND s.end_reason = 'done' THEN 1 ELSE 0 END) AS review_rounds,
           SUM(CASE WHEN s.role = 'implementer' AND (s.end_reason IS NULL OR s.end_reason NOT IN ('restart', 'moved'))
                    THEN 1 ELSE 0 END) AS implementer_sessions
    FROM bounds b
    JOIN sessions s ON s.repository = b.repository AND s.issue = b.issue
        AND julianday(s.started_at) >= julianday(b.started_at)
        AND julianday(s.started_at) <= julianday(b.ended_at)
    WHERE s.role IN ('reviewer', 'implementer')
    GROUP BY b.task
),
merged AS (
    SELECT b.task, 1 AS merged
    FROM bounds b
    JOIN lead_events le ON le.kind = 'end'
        AND le.issue_repository = b.repository AND le.issue = b.issue
        AND julianday(le.time) >= julianday(b.ended_at, '-1 minute')
        AND julianday(le.time) <= julianday(b.ended_at, '+1 minute')
        AND substr(le.payload, -8) = ' merged.'
    GROUP BY b.task
)
SELECT b.repository AS repository,
       CAST(b.ended_at AS TEXT) AS ended_at,
       w.harness AS harness,
       w.model AS model,
       w.effort AS effort,
       w.started_at AS working_started_at,
       CAST(COALESCE(st.needs_human, 0) AS INTEGER) AS needs_human,
       CAST(COALESCE(st.questions, 0) AS INTEGER) AS questions,
       CAST(COALESCE(r.review_rounds, 0) AS INTEGER) AS review_rounds,
       CAST(COALESCE(r.implementer_sessions, 0) AS INTEGER) AS implementer_sessions,
       u.input_tokens AS input_tokens,
       u.output_tokens AS output_tokens,
       u.cache_read_tokens AS cache_read_tokens,
       u.cache_write_tokens AS cache_write_tokens,
       u.cost_usd AS cost_usd,
       CAST(COALESCE(m.merged, 0) AS BOOLEAN) AS merged
FROM bounds b
LEFT JOIN working w ON w.task = b.task
LEFT JOIN states st ON st.task = b.task
LEFT JOIN usage u ON u.task = b.task
LEFT JOIN rounds r ON r.task = b.task
LEFT JOIN merged m ON m.task = b.task
ORDER BY julianday(b.ended_at), b.task;

-- name: AddStepTime :exec
INSERT INTO step_times (kind, session, task, issue, workstream, organization, repository, role, harness, model, effort,
                        started_at, ended_at, attempt, result)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SetACPSessionID :exec
UPDATE sessions SET acp_session_id = ? WHERE id = ?;

-- name: SetSessionContinued :exec
UPDATE sessions SET continued = 1 WHERE id = ?;

-- name: SetSessionContext :exec
UPDATE sessions SET context_used = ?, context_size = ? WHERE id = ?;

-- name: EndSession :one
UPDATE sessions SET ended_at = ?, end_reason = ?, queue_reason = NULL WHERE id = ?
RETURNING *;

-- name: GetSession :one
SELECT * FROM sessions WHERE id = ?;

-- name: ListSessions :many
SELECT * FROM sessions WHERE organization = ? AND workstream_repository = ? AND workstream = ? ORDER BY id;

-- name: AddTranscriptRow :one
INSERT INTO transcript (session, time, kind, json) VALUES (?, ?, ?, ?)
RETURNING *;

-- name: SetTranscriptJSON :one
UPDATE transcript SET json = ? WHERE id = ?
RETURNING *;

-- name: ListTranscriptPage :many
SELECT * FROM transcript WHERE session = ? AND id < ? ORDER BY id DESC LIMIT ?;

-- name: GetFirstTranscriptPrompt :one
SELECT CAST(COALESCE(MIN(id), 0) AS INTEGER) FROM transcript WHERE session = ? AND kind = 'prompt';

-- name: GetLiveTask :one
SELECT * FROM tasks WHERE repository = ? AND issue = ? AND state <> 'ended';

-- name: ListLiveTasks :many
SELECT * FROM tasks WHERE repository = ? AND state <> 'ended' ORDER BY id;

-- name: GetLiveTaskByPullRequest :one
SELECT * FROM tasks WHERE repository = ? AND pull_request = ? AND state <> 'ended';

-- name: SetQueueReason :one
UPDATE sessions SET queue_reason = ? WHERE id = ?
RETURNING *;

-- name: ClearQueueReason :one
UPDATE sessions SET queue_reason = NULL WHERE id = ?
RETURNING *;

-- name: StartSession :one
UPDATE sessions SET started_at = ?, queue_reason = NULL WHERE id = ?
RETURNING *;

-- name: SetSessionBinding :one
UPDATE sessions SET harness = ?, model = ?, effort = ? WHERE id = ?
RETURNING *;

-- name: ListOpenSessionIDs :many
SELECT id FROM sessions WHERE ended_at IS NULL ORDER BY id;

-- name: ListOpenSessions :many
SELECT sqlc.embed(sessions), w.title AS workstream_title, i.title AS issue_title,
       (SELECT t.pull_request FROM tasks t
        WHERE t.repository = sessions.repository AND t.issue = sessions.issue
        ORDER BY t.id DESC LIMIT 1) AS pull_request
FROM sessions
LEFT JOIN workstreams w ON w.repository = sessions.workstream_repository AND w.number = sessions.workstream AND w.state = 'open'
LEFT JOIN issues i ON i.workstream = w.id AND i.repository = sessions.repository AND i.number = sessions.issue
WHERE sessions.ended_at IS NULL ORDER BY sessions.id;

-- name: GetSessionIssueTitle :one
SELECT i.title FROM issues i
JOIN workstreams w ON w.id = i.workstream
WHERE w.repository = sqlc.arg(workstream_repository) AND w.number = CAST(sqlc.arg(workstream) AS INTEGER) AND i.number = CAST(sqlc.arg(issue) AS INTEGER)
  AND i.repository = sqlc.arg(repository);

-- name: ListBoardTasks :many
SELECT tasks.id, tasks.repository, tasks.issue, tasks.workstream_repository, tasks.workstream, tasks.state, tasks.state_at,
       tasks.pull_request, tasks.fix_rounds, tasks.review_rounds, tasks.merge_queued_at,
       i.title AS issue_title, w.title AS workstream_title,
       coalesce(m.merging, 0) AS merging, coalesce(m.merge_refusal, '') AS merge_refusal
FROM tasks
LEFT JOIN inbox_items m ON m.id = (
  SELECT max(id) FROM inbox_items
  WHERE kind = 'ready for review' AND repository = tasks.repository AND issue = tasks.issue AND pull_request = tasks.pull_request
)
JOIN workstreams w ON w.repository = tasks.workstream_repository AND w.number = tasks.workstream AND w.state = 'open'
JOIN issues i ON i.workstream = w.id AND i.repository = tasks.repository AND i.number = tasks.issue
ORDER BY tasks.id;

-- name: ListQueuedTasks :many
SELECT id, queued_at, EXISTS (
  SELECT 1 FROM ci_fix_issues WHERE ci_fix_issues.repository = tasks.repository AND ci_fix_issues.issue = tasks.issue
) AS ci FROM tasks WHERE state = 'queued' ORDER BY queued_at, id;

-- name: IsCIFixIssue :one
SELECT EXISTS (SELECT 1 FROM ci_fix_issues WHERE repository = ? AND issue = ?);

-- name: SetTaskState :execrows
UPDATE tasks SET state = sqlc.arg(state), state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), long_wait_at = NULL
WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- name: HandTaskToHuman :execrows
UPDATE tasks SET state = 'needs_human', merge_queued_at = NULL, merge_head_at = NULL, needs_human_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), long_wait_at = NULL
WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- name: AddWorkerRestart :one
UPDATE tasks SET worker_restarts = worker_restarts + 1 WHERE id = sqlc.arg(id) AND worker_restarts < sqlc.arg(max)
RETURNING worker_restarts;

-- name: GetHarnessPause :one
SELECT * FROM harness_pauses WHERE harness = ?;

-- name: ListHarnessPauses :many
SELECT * FROM harness_pauses ORDER BY harness;

-- name: SetHarnessPause :exec
INSERT INTO harness_pauses (harness, paused_until) VALUES (?, ?)
ON CONFLICT (harness) DO UPDATE SET paused_until = excluded.paused_until;

-- name: ListWaitingSessions :many
SELECT s.harness, s.role, s.repository, s.issue, s.workstream_repository, s.workstream,
       w.title AS workstream_title, i.title AS issue_title
FROM sessions s
LEFT JOIN workstreams w ON w.repository = s.workstream_repository AND w.number = s.workstream AND w.state = 'open'
LEFT JOIN issues i ON i.workstream = w.id AND i.repository = s.repository AND i.number = s.issue
WHERE s.ended_at IS NULL AND s.queue_reason LIKE CAST(sqlc.arg(prefix) AS TEXT) || '%'
ORDER BY s.id;

-- name: DeleteHarnessPause :exec
DELETE FROM harness_pauses WHERE harness = ?;

-- name: GetHarnessPauseEnd :one
SELECT ended_at FROM harness_pause_ends WHERE harness = ?;

-- name: SetHarnessPauseEnd :exec
INSERT INTO harness_pause_ends (harness, ended_at) VALUES (?, ?)
ON CONFLICT (harness) DO UPDATE SET ended_at = excluded.ended_at;

-- name: AddInboxItem :one
INSERT INTO inbox_items (kind, organization, repository, workstream_repository, workstream, issue, title, reason, text, link, pull_request, report_link, chat_message, merging, questions, time)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetInboxItem :one
SELECT * FROM inbox_items WHERE id = ?;

-- name: GetChatMessage :one
SELECT * FROM chat_messages WHERE id = ?;

-- name: DismissInboxItem :one
UPDATE inbox_items SET dismissed_at = ? WHERE id = ?
RETURNING *;

-- name: SetInboxItemReportLink :one
UPDATE inbox_items SET report_link = ? WHERE id = ?
RETURNING *;

-- name: SetInboxItemAnswer :exec
UPDATE inbox_items SET answer = ? WHERE id = ?;

-- name: SetInboxItemWithdrawn :exec
UPDATE inbox_items SET withdrawn = 1 WHERE id = ?;

-- name: GetQuestionByChatMessage :one
SELECT * FROM inbox_items WHERE chat_message = ? AND kind = 'question';

-- name: ReopenInboxItem :one
UPDATE inbox_items SET dismissed_at = NULL WHERE id = ?
RETURNING *;

-- name: SetInboxItemMerge :one
UPDATE inbox_items SET merging = ?, merge_refusal = ? WHERE id = ?
RETURNING *;

-- name: GetNewestInboxItem :one
SELECT * FROM inbox_items WHERE repository = ? AND issue = ? AND kind = ? ORDER BY id DESC LIMIT 1;

-- name: ListIssueInboxItems :many
SELECT * FROM inbox_items WHERE repository = ? AND issue = ? AND kind = ? ORDER BY id;

-- name: ListDismissedQuestionItems :many
SELECT * FROM inbox_items WHERE workstream_repository = ? AND kind = 'question' AND dismissed_at IS NOT NULL ORDER BY id;

-- name: ListOpenInboxItems :many
SELECT * FROM inbox_items WHERE dismissed_at IS NULL ORDER BY id;

-- name: ListMergingReadyItems :many
SELECT * FROM inbox_items
WHERE inbox_items.repository = ? AND inbox_items.kind = 'ready for review' AND inbox_items.merging != 0 AND EXISTS (
  SELECT 1 FROM tasks
  WHERE tasks.repository = inbox_items.repository AND tasks.issue = inbox_items.issue
    AND tasks.pull_request = inbox_items.pull_request AND tasks.state = 'ready_for_review'
)
ORDER BY id;

-- name: ListOpenCoordinatorQuestions :many
SELECT * FROM inbox_items
WHERE kind = 'question' AND organization = ? AND repository = '' AND issue = 0 AND dismissed_at IS NULL
ORDER BY id;

-- name: ListOpenLeadQuestions :many
SELECT * FROM inbox_items
WHERE kind = 'question' AND workstream_repository = ? AND workstream = ? AND issue <> 0 AND dismissed_at IS NULL
ORDER BY id;

-- name: ListOpenActionRequests :many
SELECT inbox_items.id, pending_actions.tool, inbox_items.title, inbox_items.time
FROM pending_actions
JOIN inbox_items ON inbox_items.id = pending_actions.inbox_item
WHERE pending_actions.role = ? AND pending_actions.organization = ? AND pending_actions.repository = ? AND pending_actions.workstream = ?
  AND pending_actions.state = 'pending' AND pending_actions.running = 0 AND inbox_items.dismissed_at IS NULL
ORDER BY inbox_items.id;

-- name: AddChatMessage :one
INSERT INTO chat_messages (organization, repository, workstream, author, time, text, browser_id, inbox_items, voice, selections)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: DeliverChatMessage :one
UPDATE chat_messages SET delivered_at = ?, failed_at = NULL WHERE id = ? AND delivered_at IS NULL
RETURNING *;

-- name: ClearChatMessageFailure :exec
UPDATE chat_messages SET failed_at = NULL WHERE id = ?;

-- name: UndeliverChatMessage :one
UPDATE chat_messages SET delivered_at = NULL WHERE id = ? AND delivered_at IS NOT NULL
RETURNING *;

-- name: FailChatMessage :one
UPDATE chat_messages SET failed_at = sqlc.arg(failed_at)
WHERE id = sqlc.arg(id) AND delivered_at IS NULL AND stopped_at IS NULL AND cancelled_at IS NULL
RETURNING *;

-- name: CancelChatMessage :one
UPDATE chat_messages SET cancelled_at = sqlc.arg(cancelled_at)
WHERE id = sqlc.arg(id) AND delivered_at IS NULL AND cancelled_at IS NULL
RETURNING *;

-- name: UndoCancelChatMessage :one
UPDATE chat_messages SET cancelled_at = NULL
WHERE id = sqlc.arg(id) AND delivered_at IS NULL AND cancelled_at IS NOT NULL
RETURNING *;

-- name: StopChatMessage :one
UPDATE chat_messages SET stopped_at = ? WHERE id = ? AND delivered_at IS NULL AND stopped_at IS NULL AND cancelled_at IS NULL
RETURNING *;

-- name: MaxChatMessageID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) FROM chat_messages;

-- name: ListUndeliveredOwnerMessages :many
SELECT * FROM chat_messages
WHERE organization = ? AND repository = ? AND id <= ?
  AND (author = 'Owner' AND browser_id IS NOT NULL OR author = 'Approval')
  AND delivered_at IS NULL AND stopped_at IS NULL AND cancelled_at IS NULL
ORDER BY id;

-- name: ListFailedChatMessages :many
SELECT * FROM chat_messages
WHERE organization = ? AND repository = ? AND workstream = ?
  AND failed_at IS NOT NULL AND delivered_at IS NULL AND stopped_at IS NULL AND cancelled_at IS NULL
ORDER BY id;

-- name: ListFailedChatMessagesOfRepository :many
SELECT * FROM chat_messages
WHERE organization = ? AND repository = ?
  AND failed_at IS NOT NULL AND delivered_at IS NULL AND stopped_at IS NULL AND cancelled_at IS NULL
ORDER BY id;

-- name: ListFailedCoordinatorMessages :many
SELECT * FROM chat_messages
WHERE organization = ? AND repository = ''
  AND failed_at IS NOT NULL AND delivered_at IS NULL AND stopped_at IS NULL AND cancelled_at IS NULL
ORDER BY id;

-- name: GetOpenFailedItemOfMessage :one
SELECT * FROM inbox_items WHERE kind = 'Lead failed' AND chat_message = ? AND dismissed_at IS NULL;

-- name: ListOpenFailedItemsOfMessage :many
SELECT * FROM inbox_items WHERE kind = 'Lead failed' AND chat_message = ? AND dismissed_at IS NULL;

-- name: GetLastChatMessageOf :one
SELECT * FROM chat_messages
WHERE organization = ? AND repository = ? AND workstream = ? AND author = ?
ORDER BY id DESC LIMIT 1;

-- name: DeleteChatMessage :exec
DELETE FROM chat_messages WHERE id = ?;

-- name: AppendChatMessage :one
UPDATE chat_messages SET text = text || sqlc.arg(text) WHERE id = sqlc.arg(id)
RETURNING *;

-- The chat shows no Researcher message, no Operator message, no Curator message and no Approval message.
-- name: ListChatMessagesPage :many
SELECT * FROM chat_messages
WHERE organization = ? AND repository = ? AND workstream = ? AND author NOT IN ('Researcher', 'Operator', 'Curator', 'Approval') AND id < ?
ORDER BY id DESC LIMIT ?;

-- name: ListChatMessagesBefore :many
SELECT * FROM chat_messages
WHERE organization = ? AND repository = ? AND workstream = ? AND author != 'Record' AND id < ?
  AND cancelled_at IS NULL
ORDER BY id DESC LIMIT ?;

-- name: SetChatSeen :exec
INSERT INTO chat_seen (organization, repository, workstream, message) VALUES (?, ?, ?, ?)
ON CONFLICT (organization, repository, workstream) DO UPDATE SET message = max(message, excluded.message);

-- The messages of the Owner, of a Researcher, of an Operator, of a Curator, of an approval, of an event and of a record are never unread.
-- name: ListUnread :many
SELECT m.organization, m.repository, m.workstream, count(*) AS count
FROM chat_messages m
LEFT JOIN chat_seen s ON s.organization = m.organization AND s.repository = m.repository AND s.workstream = m.workstream
WHERE m.author NOT IN ('Owner', 'Researcher', 'Operator', 'Curator', 'Approval', 'Event', 'Record') AND m.id > coalesce(s.message, 0)
GROUP BY m.organization, m.repository, m.workstream
ORDER BY m.organization, m.repository, m.workstream;

-- name: CountUnread :one
SELECT count(*) FROM chat_messages m
WHERE m.organization = sqlc.arg(organization) AND m.repository = sqlc.arg(repository) AND m.workstream = sqlc.arg(workstream)
  AND m.author NOT IN ('Owner', 'Researcher', 'Operator', 'Curator', 'Approval', 'Event', 'Record')
  AND m.id > coalesce((SELECT s.message FROM chat_seen s
                       WHERE s.organization = m.organization AND s.repository = m.repository AND s.workstream = m.workstream), 0);

-- name: AddLeadEvent :one
INSERT INTO lead_events (repository, workstream, issue_repository, issue, kind, payload, time, chat_message, comment, review)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: HasLeadEvent :one
SELECT EXISTS (
    SELECT 1 FROM lead_events
    WHERE repository = ? AND workstream = ? AND kind = ? AND payload = ?
);

-- A follow-up event of a Reviewer names its review comment as ", item <id>, ".
-- name: HasFollowUpEvent :one
SELECT EXISTS (
    SELECT 1 FROM lead_events
    WHERE repository = sqlc.arg(workstream_repository) AND issue_repository = sqlc.arg(repository) AND issue = sqlc.arg(issue) AND kind = 'follow_up'
      AND instr(payload, ', item ' || CAST(sqlc.arg(item) AS INTEGER) || ', ') > 0
);

-- name: HasFollowUpEventSince :one
SELECT EXISTS (
    SELECT 1 FROM lead_events
    WHERE repository = sqlc.arg(workstream_repository) AND issue_repository = sqlc.arg(repository) AND issue = sqlc.arg(issue) AND kind = 'follow_up'
      AND julianday(time) >= julianday(sqlc.arg(since))
);

-- name: DeliverLeadEvents :exec
UPDATE lead_events SET delivered_at = ? WHERE repository = ? AND workstream = ? AND delivered_at IS NULL;

-- name: DeliverLeadEvent :exec
UPDATE lead_events SET delivered_at = ? WHERE id = ?;

-- name: ListUndeliveredLeadEvents :many
SELECT * FROM lead_events WHERE repository = ? AND workstream = ? AND delivered_at IS NULL ORDER BY id;

-- A held event holds each later event of its task issue.
-- name: ListReadyLeadEvents :many
SELECT * FROM lead_events AS event
WHERE event.repository = ? AND event.workstream = ? AND event.delivered_at IS NULL AND event.held = 0
  AND NOT EXISTS (
      SELECT 1 FROM lead_events AS earlier
      WHERE earlier.repository = event.repository AND earlier.workstream = event.workstream AND earlier.issue = event.issue
        AND earlier.issue_repository = event.issue_repository
        AND earlier.id < event.id AND earlier.delivered_at IS NULL AND earlier.held = 1
  )
ORDER BY event.id;

-- name: GetLastLeadEventDelivery :one
SELECT delivered_at FROM lead_events
WHERE repository = ? AND workstream = ? AND delivered_at IS NOT NULL
ORDER BY julianday(delivered_at) DESC LIMIT 1;

-- name: GetLastLeadEventTime :one
SELECT time FROM lead_events
WHERE repository = ? AND workstream = ? AND kind = ?
ORDER BY julianday(time) DESC LIMIT 1;

-- A failed start of a Lead session gets no first prompt. Compare the started_at texts with the bound only as a wide
-- range, because the fractions of the times have no fixed length.
-- name: ListFailedLeadStarts :many
SELECT started_at FROM sessions AS session
WHERE session.repository = sqlc.arg(repository) AND session.workstream = sqlc.arg(workstream) AND session.role = 'lead_chat'
  AND session.end_reason = 'failed' AND session.started_at >= sqlc.arg(since)
  AND NOT EXISTS (SELECT 1 FROM transcript WHERE transcript.session = session.id AND transcript.kind = 'prompt');

-- name: ListWorkstreamsWithReadyLeadEvents :many
SELECT DISTINCT event.workstream FROM lead_events AS event
WHERE event.repository = ? AND event.delivered_at IS NULL AND event.held = 0
  AND NOT EXISTS (
      SELECT 1 FROM lead_events AS earlier
      WHERE earlier.repository = event.repository AND earlier.workstream = event.workstream AND earlier.issue = event.issue
        AND earlier.issue_repository = event.issue_repository
        AND earlier.id < event.id AND earlier.delivered_at IS NULL AND earlier.held = 1
  )
ORDER BY event.workstream;

-- A Workstream with an issue that has the question label, or with an open question item, has no row.
-- name: ListHeldLeadEventsWithoutQuestion :many
SELECT * FROM lead_events AS event
WHERE event.repository = sqlc.arg(repository) AND event.held = 1 AND event.delivered_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM issues AS i
      JOIN workstreams AS w ON w.id = i.workstream
      WHERE w.repository = event.repository AND w.number = event.workstream AND i.state = 'open'
        AND instr(json_extract(i.metadata, '$.github.labels'), json_quote(CAST(sqlc.arg(question_label) AS TEXT))) > 0
  )
  AND NOT EXISTS (
      SELECT 1 FROM inbox_items AS n
      WHERE n.workstream_repository = event.repository AND n.workstream = event.workstream AND n.kind = 'question' AND n.dismissed_at IS NULL
  )
ORDER BY event.id;

-- name: HoldLeadEvent :exec
UPDATE lead_events SET held = 1, held_at = ? WHERE id = ?;

-- name: HasHeldLeadEvent :one
SELECT EXISTS (
    SELECT 1 FROM lead_events
    WHERE repository = sqlc.arg(workstream_repository) AND issue_repository = sqlc.arg(repository) AND issue = sqlc.arg(issue) AND held = 1 AND delivered_at IS NULL
);

-- name: HasUndeliveredLeadEventOfIssue :one
SELECT EXISTS (
    SELECT 1 FROM lead_events
    WHERE repository = sqlc.arg(workstream_repository) AND issue_repository = sqlc.arg(repository) AND issue = sqlc.arg(issue) AND delivered_at IS NULL
);

-- name: FreeLeadEvents :many
UPDATE lead_events SET held = 0, held_at = NULL WHERE repository = ? AND workstream = ? AND held = 1
RETURNING *;

-- A stop writes a stop event, a stale_pull_request event, or an Inbox item of the kind stopped or lost.
-- name: GetNewestStopText :one
SELECT text FROM (
    SELECT payload AS text, time FROM lead_events
    WHERE lead_events.repository = sqlc.arg(workstream_repository) AND lead_events.issue_repository = sqlc.arg(repository) AND lead_events.issue = sqlc.arg(issue) AND lead_events.kind IN ('stop', 'stale_pull_request')
    UNION ALL
    SELECT text, time FROM inbox_items
    WHERE inbox_items.repository = sqlc.arg(repository) AND inbox_items.issue = sqlc.arg(issue) AND inbox_items.kind IN ('stopped', 'lost')
) ORDER BY time DESC LIMIT 1;

-- name: ListWaitingLeadWorkstreams :many
SELECT DISTINCT repository, workstream FROM lead_events WHERE delivered_at IS NULL ORDER BY repository, workstream;

-- name: AddTask :one
INSERT INTO tasks (repository, issue, workstream_repository, workstream, state, dispatched_at, state_at)
VALUES (?, ?, ?, ?, 'dispatched', ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
RETURNING *;

-- An ended task counts too.
-- name: HasTask :one
SELECT EXISTS (SELECT 1 FROM tasks WHERE repository = ? AND issue = ?);

-- name: CountActiveTasks :one
SELECT count(*) FROM tasks WHERE state IN ('dispatched', 'queued', 'working');

-- name: ResetTaskCounters :exec
UPDATE tasks SET fix_rounds = 0, review_rounds = 0, worker_restarts = 0, check_head = NULL WHERE id = ?;

-- name: EndTask :exec
UPDATE tasks SET state = 'ended', lead_findings = '', merge_queued_at = NULL, merge_head_at = NULL, state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), long_wait_at = NULL WHERE id = ?;

-- name: GetEndedTaskWithPullRequest :one
SELECT * FROM tasks
WHERE repository = ? AND issue = ? AND state = 'ended' AND branch IS NOT NULL AND pull_request IS NOT NULL
ORDER BY id DESC LIMIT 1;

-- name: ListBranchesToCheck :many
SELECT * FROM tasks
WHERE state = 'ended' AND branch IS NOT NULL AND pull_request IS NOT NULL AND branch_checked = 0
ORDER BY id;

-- name: SetTaskBranchChecked :exec
UPDATE tasks SET branch_checked = 1 WHERE id = ?;

-- name: GetLastTaskState :one
SELECT state FROM task_states WHERE task = ? ORDER BY id DESC LIMIT 1;

-- name: RecoverTask :execrows
UPDATE tasks SET state = sqlc.arg(state), state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), recoveries = recoveries + 1,
    needs_human_at = CASE WHEN sqlc.arg(state) = 'needs_human' THEN strftime('%Y-%m-%dT%H:%M:%fZ', 'now') ELSE needs_human_at END
WHERE id = sqlc.arg(id) AND state = 'ended' AND recoveries < sqlc.arg(max);

-- name: ContinueEndedTask :execrows
UPDATE tasks SET state = 'stopped', state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), recoveries = 0
WHERE id = ? AND state = 'ended';

-- name: QueueTask :execrows
UPDATE tasks SET state = 'queued', queued_at = sqlc.arg(queued_at), state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), long_wait_at = NULL
WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- A restart keeps the place of the task in the queue.
-- name: RequeueTask :execrows
UPDATE tasks SET state = 'queued', state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), long_wait_at = NULL WHERE id = ? AND state IN ('queued', 'working');

-- name: SetTaskWorker :exec
UPDATE tasks SET worker = ?, worker_input = ? WHERE id = ?;

-- name: StartTaskWorker :execrows
UPDATE tasks SET state = 'working', merge_queued_at = NULL, merge_head_at = NULL, worker = sqlc.arg(worker), worker_input = sqlc.arg(worker_input), state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), long_wait_at = NULL
WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- name: SetTaskBranch :exec
UPDATE tasks SET branch = ? WHERE id = ?;

-- name: SetTaskPullRequest :exec
UPDATE tasks SET pull_request = ? WHERE id = ?;

-- name: SetTaskReport :exec
UPDATE tasks SET report = ? WHERE id = ?;

-- name: AddFixRound :execrows
UPDATE tasks SET fix_rounds = fix_rounds + 1 WHERE id = sqlc.arg(id) AND fix_rounds < sqlc.arg(max);

-- name: KeepLeadFindings :execrows
UPDATE tasks SET lead_findings = lead_findings || sqlc.arg(findings) WHERE id = sqlc.arg(id) AND state IN ('working', 'reviewed');

-- name: GetLeadFindings :one
SELECT lead_findings FROM tasks WHERE id = ?;

-- name: DropLeadFindings :exec
UPDATE tasks SET lead_findings = substr(lead_findings, length(sqlc.arg(taken)) + 1)
WHERE id = sqlc.arg(id) AND substr(lead_findings, 1, length(sqlc.arg(taken))) = sqlc.arg(taken);

-- name: SetTaskCheckHead :exec
UPDATE tasks SET check_head = ? WHERE id = ?;

-- name: SetTaskReviewedHead :exec
UPDATE tasks SET reviewed_head = ? WHERE id = ?;

-- name: SetTaskApprovedHead :exec
UPDATE tasks SET approved_head = ? WHERE id = ?;

-- name: SetTaskApprovedReport :exec
UPDATE tasks SET approved_report = ?, approved_report_head = ? WHERE id = ?;

-- name: SetTaskApprovalChange :exec
UPDATE tasks SET approved_again_change = ?, approved_again_comment = NULL WHERE id = ?;

-- name: SetTaskApprovedAgainHead :exec
UPDATE tasks SET approved_again_head = ? WHERE id = ?;

-- name: SetTaskApprovedAgainComment :exec
UPDATE tasks SET approved_again_comment = ? WHERE id = ?;

-- name: SetTaskConflictHead :exec
UPDATE tasks SET conflict_head = ? WHERE id = ?;

-- name: SetTaskCiFailedHead :exec
UPDATE tasks SET ci_failed_head = ? WHERE id = ?;

-- name: AddReviewRound :exec
UPDATE tasks SET review_rounds = review_rounds + 1 WHERE id = ?;

-- name: GetReviewComment :one
SELECT review_comment FROM tasks WHERE id = ?;

-- name: SetReviewComment :exec
UPDATE tasks SET review_comment = ? WHERE id = ?;

-- name: AddOutgoingChange :one
INSERT INTO outgoing_changes (integration, repository, number, workstream, reply_to, field, text, status, created_at) VALUES ('github', ?, ?, ?, ?, ?, ?, 'pending', ?)
RETURNING id;

-- name: GetOutgoingChange :one
SELECT * FROM outgoing_changes WHERE id = ?;

-- name: ListPendingOutgoingChanges :many
SELECT * FROM outgoing_changes WHERE repository = ? AND status = 'pending' ORDER BY id;

-- name: GetCreateChange :one
SELECT id FROM outgoing_changes WHERE field = 'create' AND workstream = ?;

-- name: GetNewestCommentChange :one
SELECT * FROM outgoing_changes WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER) AND field = 'comment' ORDER BY id DESC LIMIT 1;

-- name: HasCommentChangeWithText :one
SELECT EXISTS (
    SELECT 1 FROM outgoing_changes
    WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER) AND field = 'comment' AND status <> 'refused' AND instr(text, sqlc.arg(part)) > 0
        AND julianday(created_at) >= julianday(sqlc.arg(since))
);

-- name: SetOutgoingChangeStatus :exec
UPDATE outgoing_changes SET status = ? WHERE id = ?;

-- name: DeleteFinishedOutgoingChanges :exec
DELETE FROM outgoing_changes WHERE status <> 'pending' AND julianday(created_at) < julianday(?);

-- name: AddIncomingChange :exec
INSERT INTO incoming_changes (integration, external_id, repository, number, field, change, actor, metadata, status, created_at)
VALUES ('github', ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (integration, external_id) DO NOTHING;

-- name: HasIncomingChange :one
SELECT EXISTS (SELECT 1 FROM incoming_changes WHERE integration = 'github' AND external_id = ?);

-- name: ListPendingIncomingChanges :many
SELECT * FROM incoming_changes WHERE repository = ? AND status = 'pending' ORDER BY id;

-- name: SetIncomingChangeDone :exec
UPDATE incoming_changes SET status = 'done' WHERE id = ?;

-- name: DeleteDoneIncomingChanges :exec
DELETE FROM incoming_changes WHERE status = 'done' AND julianday(created_at) < julianday(?);

-- name: AddOwnWrite :one
INSERT INTO own_writes (integration, repository, number, field, metadata, created_at) VALUES ('github', ?, ?, ?, ?, ?)
RETURNING id;

-- name: HasOwnWrite :one
SELECT EXISTS (SELECT 1 FROM own_writes WHERE repository = ? AND number = ?);

-- name: DeleteOwnWrite :exec
DELETE FROM own_writes WHERE id = ?;

-- name: DeleteOldestOwnWrite :execrows
DELETE FROM own_writes
WHERE id = (
    SELECT o.id FROM own_writes o
    WHERE o.repository = sqlc.arg(repository) AND o.number = sqlc.arg(number)
      AND json_extract(o.metadata, '$.label') = CAST(sqlc.arg(label) AS TEXT)
      AND json_extract(o.metadata, '$.change') = CAST(sqlc.arg(change) AS TEXT)
    ORDER BY o.id LIMIT 1
);

-- name: DeleteOldOwnWrites :exec
DELETE FROM own_writes WHERE julianday(created_at) < julianday(?);

-- name: GetNewestTaskOfPullRequest :one
SELECT * FROM tasks WHERE repository = ? AND pull_request = ? ORDER BY id DESC LIMIT 1;

-- name: SetReviewCommentOfPullRequest :exec
UPDATE tasks SET review_comment = ?
WHERE id = (SELECT t.id FROM tasks t WHERE t.repository = ? AND t.pull_request = ? ORDER BY t.id DESC LIMIT 1);

-- name: AddStopNotice :exec
INSERT INTO stop_notices (task, title, reason, text, output, next, lead_kind, lead_text, check_title, check_summary, check_create, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListStopNotices :many
SELECT sqlc.embed(stop_notices), sqlc.embed(tasks) FROM stop_notices
JOIN tasks ON tasks.id = stop_notices.task
WHERE tasks.repository = ?
ORDER BY stop_notices.id;

-- name: DeleteStopNotice :exec
DELETE FROM stop_notices WHERE id = ?;

-- name: ListStopEvents :many
SELECT * FROM lead_events
WHERE repository = sqlc.arg(workstream_repository) AND issue_repository = sqlc.arg(repository) AND issue = sqlc.arg(issue) AND kind = sqlc.arg(kind) AND payload = sqlc.arg(payload);

-- name: SetJudgedAt :exec
UPDATE tasks SET judged_at = ? WHERE id = ?;

-- name: StopTask :execrows
UPDATE tasks SET state = 'stopped', merge_queued_at = NULL, merge_head_at = NULL, state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), long_wait_at = NULL WHERE id = ? AND state NOT IN ('stopped', 'ended');

-- name: ListLiveTaskRepositories :many
SELECT DISTINCT repository FROM tasks WHERE state <> 'ended' ORDER BY repository;

-- name: ListTaskPullRequests :many
SELECT repository, CAST(pull_request AS INTEGER) AS pull_request FROM tasks
WHERE workstream_repository = ? AND workstream = ? AND pull_request IS NOT NULL ORDER BY id;

-- name: ListWorkstreamLiveTasks :many
SELECT * FROM tasks WHERE workstream_repository = ? AND workstream = ? AND state <> 'ended' ORDER BY id;

-- name: ListCopiedRepositories :many
SELECT DISTINCT repository FROM workstreams;

-- name: ListRepositoriesWithOpenWorkstreams :many
SELECT DISTINCT repository FROM workstreams WHERE state = 'open';

-- name: DeleteCopiedWorkstreamsOf :exec
DELETE FROM workstreams WHERE repository = ?;

-- name: ListOpenWorkstreamNumbers :many
SELECT CAST(number AS INTEGER) AS number FROM workstreams WHERE repository = ? AND state = 'open' AND number IS NOT NULL ORDER BY number;

-- name: CloseWorkstream :execrows
UPDATE workstreams SET state = 'closed', close_pending = 1, finished_at = NULL WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER) AND state != 'closed';

-- name: ClearWorkstreamClosePending :exec
UPDATE workstreams SET close_pending = 0 WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: ListClosePendingWorkstreams :many
SELECT CAST(number AS INTEGER) AS number FROM workstreams WHERE repository = ? AND close_pending != 0 AND number IS NOT NULL ORDER BY number;

-- name: IsWorkstreamClosePending :one
SELECT EXISTS (SELECT 1 FROM workstreams WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER) AND close_pending != 0);

-- name: DeleteCopiedBlockersOfTrees :exec
DELETE FROM blockers WHERE issue IN (
    SELECT i.id FROM issues i JOIN workstreams w ON w.id = i.workstream WHERE w.repository = ?
);

-- name: DetachCopiedTrees :exec
UPDATE issues SET workstream = NULL, parent = NULL
WHERE workstream IN (SELECT w.id FROM workstreams w WHERE w.repository = ?);

-- name: DeleteCopiedWorkstream :execrows
DELETE FROM workstreams WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: DeleteCopiedBlockersOfTree :exec
DELETE FROM blockers WHERE issue IN (SELECT id FROM issues WHERE workstream = ?);

-- name: DetachCopiedTree :exec
UPDATE issues SET workstream = NULL, parent = NULL WHERE workstream = ?;

-- An issue that is in no tree and blocks no issue goes away.
-- name: DeleteUnusedIssues :exec
DELETE FROM issues WHERE workstream IS NULL AND NOT EXISTS (SELECT 1 FROM blockers WHERE blockers.blocker = issues.id);

-- The row stays after its Workstream closes. It gives 1 when it added the row.
-- name: AddWorkstream :execrows
INSERT INTO workstreams (repository, number, title, body, state, created_at, autopilot, auto_merge, evergreen, maintenance, skills)
VALUES (sqlc.arg(repository), CAST(sqlc.arg(number) AS INTEGER), sqlc.arg(title), sqlc.arg(body), sqlc.arg(state), sqlc.arg(created_at), 0, 0,
        CAST(sqlc.arg(evergreen) AS BOOLEAN), CAST(sqlc.arg(maintenance) AS BOOLEAN), CAST(sqlc.arg(skills) AS BOOLEAN))
ON CONFLICT (repository, number) DO NOTHING;

-- The row has no number until the send of its create writes it.
-- name: AddWorkstreamToCreate :one
INSERT INTO workstreams (repository, title, body, state, created_at, autopilot, auto_merge, evergreen, maintenance, skills)
VALUES (sqlc.arg(repository), sqlc.arg(title), sqlc.arg(body), 'open', sqlc.arg(created_at), 0, 0,
        CAST(sqlc.arg(evergreen) AS BOOLEAN), CAST(sqlc.arg(maintenance) AS BOOLEAN), CAST(sqlc.arg(skills) AS BOOLEAN))
RETURNING id;

-- name: ListWaitingWorkstreams :many
SELECT id, repository, title, CAST(evergreen AS BOOLEAN) AS evergreen
FROM workstreams WHERE state = 'open' AND number IS NULL
ORDER BY id DESC;

-- name: GetWorkstreamToCreate :one
SELECT number, title, body, CAST(evergreen AS BOOLEAN) AS evergreen, CAST(maintenance AS BOOLEAN) AS maintenance, CAST(skills AS BOOLEAN) AS skills
FROM workstreams WHERE id = ?;

-- name: DeleteWorkstreamToCreate :exec
DELETE FROM workstreams WHERE id = ? AND number IS NULL;

-- name: SetWorkstreamNumber :exec
UPDATE workstreams SET number = ? WHERE id = ? AND number IS NULL;

-- name: GetWorkstream :one
SELECT title, body, state, created_at FROM workstreams WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: HasWorkstream :one
SELECT EXISTS (SELECT 1 FROM workstreams WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER));

-- name: SetWorkstreamBody :execrows
UPDATE workstreams SET body = ? WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER) AND state = 'open';

-- name: SetWorkstreamTitle :execrows
UPDATE workstreams SET title = ? WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: SetWorkstreamFinishedAt :execrows
UPDATE workstreams SET finished_at = ? WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER) AND state = 'open';

-- name: GetWorkstreamFinishedAt :one
SELECT finished_at FROM workstreams WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER) AND state = 'open';

-- name: ReopenWorkstream :execrows
UPDATE workstreams SET state = 'open', close_pending = 0, finished_at = NULL WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER) AND state = 'closed';

-- name: FillAndReopenWorkstream :execrows
UPDATE workstreams SET title = ?, body = ?, created_at = ?, state = 'open', close_pending = 0, finished_at = NULL
WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER) AND state = 'closed';

-- name: HasLeadEventKind :one
SELECT EXISTS (SELECT 1 FROM lead_events WHERE repository = ? AND workstream = ? AND kind = ?);

-- name: GetWorkstreamSettings :one
SELECT CAST(autopilot AS BOOLEAN) AS autopilot, CAST(auto_merge AS BOOLEAN) AS auto_merge, CAST(evergreen AS BOOLEAN) AS evergreen,
       CAST(maintenance AS BOOLEAN) AS maintenance, CAST(skills AS BOOLEAN) AS skills
FROM workstreams WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER);

-- The GitHub data of a row is the JSON object $.github of its metadata.
-- name: AddCopiedIssue :one
INSERT INTO issues (repository, number, title, body, state, author, workstream, parent, kind, metadata)
VALUES (sqlc.arg(repository), CAST(sqlc.arg(number) AS INTEGER), sqlc.arg(title), sqlc.arg(body), sqlc.arg(state), sqlc.arg(author),
        sqlc.arg(workstream), sqlc.arg(parent), sqlc.arg(kind), json_object('github', json(CAST(sqlc.arg(github) AS TEXT))))
ON CONFLICT (repository, number) DO UPDATE SET
    title = excluded.title, body = excluded.body, state = excluded.state, author = excluded.author,
    workstream = excluded.workstream, parent = excluded.parent, kind = excluded.kind,
    metadata = json_set(issues.metadata, '$.github', json(json_extract(excluded.metadata, '$.github')))
RETURNING id;

-- A row that exists keeps its Workstream, parent, body and labels.
-- name: AddCopiedBlockerIssue :one
INSERT INTO issues (repository, number, title, body, state, author, kind, metadata)
VALUES (sqlc.arg(repository), CAST(sqlc.arg(number) AS INTEGER), sqlc.arg(title), sqlc.arg(body), sqlc.arg(state), sqlc.arg(author),
        sqlc.arg(kind), json_object('github', json(CAST(sqlc.arg(github) AS TEXT))))
ON CONFLICT (repository, number) DO UPDATE SET title = excluded.title, state = excluded.state, author = excluded.author
RETURNING id;

-- name: AddCopiedBlocker :exec
INSERT INTO blockers (issue, blocker) VALUES (?, ?)
ON CONFLICT DO NOTHING;

-- name: DeleteCopiedBlockersOfIssue :exec
DELETE FROM blockers WHERE issue = ?;

-- name: DeleteCopiedOpenBlockersOfIssue :exec
DELETE FROM blockers WHERE issue = ? AND blocker IN (SELECT id FROM issues WHERE state = 'open');

-- name: GetWorkstreamID :one
SELECT id FROM workstreams WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: SetCopiedBuckets :exec
UPDATE workstreams
SET metadata = json_set(metadata, '$.github', json(json_set(coalesce(json_extract(metadata, '$.github'), json_object()), '$.buckets', json(CAST(sqlc.arg(buckets) AS TEXT)))))
WHERE id = sqlc.arg(id);

-- name: GetCopiedBuckets :one
SELECT CAST(coalesce(json_extract(metadata, '$.github.buckets'), '[]') AS TEXT) AS buckets
FROM workstreams WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: HasCopiedWorkstream :one
SELECT EXISTS (SELECT 1 FROM workstreams WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER) AND state = 'open');

-- name: GetCopiedIssueWorkstream :one
SELECT CAST(w.number AS INTEGER) AS number FROM issues i
JOIN workstreams w ON w.id = i.workstream
WHERE w.repository = sqlc.arg(workstream_repository) AND i.repository = sqlc.arg(repository) AND i.number = CAST(sqlc.arg(number) AS INTEGER)
LIMIT 1;

-- name: HasCopiedWorkstreamAnyCase :one
SELECT EXISTS (SELECT 1 FROM workstreams WHERE repository = ? COLLATE NOCASE AND number = CAST(sqlc.arg(number) AS INTEGER) AND state = 'open');

-- name: GetCopiedIssueTree :one
SELECT w.repository, CAST(w.number AS INTEGER) AS workstream FROM issues i
JOIN workstreams w ON w.id = i.workstream
WHERE i.number = CAST(sqlc.arg(number) AS INTEGER) AND i.repository = sqlc.arg(repository)
ORDER BY w.repository, w.number
LIMIT 1;

-- name: ListCopiedWorkstreamsWithLabelChange :many
SELECT w.repository, CAST(w.number AS INTEGER) AS workstream FROM issues i
JOIN workstreams w ON w.id = i.workstream
WHERE i.repository = sqlc.arg(repository) AND i.number = CAST(sqlc.arg(number) AS INTEGER)
  AND (instr(json_extract(i.metadata, '$.github.labels'), json_quote(CAST(sqlc.arg(name) AS TEXT))) > 0) != CAST(sqlc.arg(present) AS BOOLEAN);

-- The Workstream with the label mobius:skills in a repository of the organization, with the lowest number.
-- name: GetSkillsWorkstream :one
SELECT w.repository, CAST(w.number AS INTEGER) AS number FROM workstreams w
WHERE w.state = 'open' AND w.number IS NOT NULL AND w.skills AND substr(w.repository, 1, length(sqlc.arg(organization)) + 1) = sqlc.arg(organization) || '/'
ORDER BY w.number, w.repository
LIMIT 1;

-- The open Workstream with the label mobius:maintenance in a repository, with the lowest number.
-- name: GetMaintenanceWorkstream :one
SELECT CAST(w.number AS INTEGER) AS number FROM workstreams w
WHERE w.state = 'open' AND w.number IS NOT NULL AND w.maintenance AND w.repository = ?
ORDER BY w.number
LIMIT 1;

-- name: ListCopiedWorkstreamsWithIssue :many
SELECT w.repository, CAST(w.number AS INTEGER) AS workstream FROM issues i
JOIN workstreams w ON w.id = i.workstream
WHERE i.repository = ? AND i.number = CAST(sqlc.arg(number) AS INTEGER);

-- name: GetCopiedWorkstream :one
SELECT title, body, CAST(evergreen AS BOOLEAN) AS evergreen, CAST(maintenance AS BOOLEAN) AS maintenance, CAST(skills AS BOOLEAN) AS skills, created_at
FROM workstreams
WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER) AND state = 'open';

-- name: GetWorkstreamBrief :one
SELECT body, CAST(evergreen AS BOOLEAN) AS evergreen, CAST(maintenance AS BOOLEAN) AS maintenance, CAST(skills AS BOOLEAN) AS skills
FROM workstreams
WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: SetCopiedEvergreen :exec
UPDATE workstreams SET evergreen = CAST(sqlc.arg(evergreen) AS BOOLEAN)
WHERE repository = sqlc.arg(repository) AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: SetCopiedMaintenance :exec
UPDATE workstreams SET maintenance = CAST(sqlc.arg(maintenance) AS BOOLEAN)
WHERE repository = sqlc.arg(repository) AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: SetCopiedSkills :exec
UPDATE workstreams SET skills = CAST(sqlc.arg(skills) AS BOOLEAN)
WHERE repository = sqlc.arg(repository) AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: ListAutopilotWorkstreams :many
SELECT CAST(number AS INTEGER) AS number FROM workstreams
WHERE repository = ? AND state = 'open' AND number IS NOT NULL AND autopilot
ORDER BY number DESC;

-- name: GetCopiedAutopilot :one
SELECT CAST(autopilot AS BOOLEAN) AS autopilot, CAST(auto_merge AS BOOLEAN) AS auto_merge FROM workstreams
WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER) AND state = 'open';

-- name: SetCopiedAutopilot :exec
UPDATE workstreams SET autopilot = CAST(sqlc.arg(autopilot) AS BOOLEAN), auto_merge = CAST(sqlc.arg(auto_merge) AS BOOLEAN)
WHERE repository = sqlc.arg(repository) AND number = CAST(sqlc.arg(number) AS INTEGER);

-- The labels in the metadata are a copy of GitHub. The change of the title, the body, the state or the author gives
-- the number of changed rows.
-- name: UpdateCopiedIssue :execrows
UPDATE issues SET title = sqlc.arg(title), body = sqlc.arg(body), state = sqlc.arg(state), author = sqlc.arg(author)
WHERE repository = sqlc.arg(repository) AND number = CAST(sqlc.arg(number) AS INTEGER)
  AND (title != sqlc.arg(title) OR body != sqlc.arg(body) OR state != sqlc.arg(state) OR author != sqlc.arg(author));

-- name: GetCopiedIssue :one
SELECT id, state, metadata FROM issues WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: SetCopiedIssueLabels :exec
UPDATE issues SET metadata = json_set(metadata, '$.github.labels', json(CAST(sqlc.arg(labels) AS TEXT))), kind = sqlc.arg(kind)
WHERE repository = sqlc.arg(repository) AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: GetCopiedIssueWorkstreamOf :one
SELECT w.repository, CAST(w.number AS INTEGER) AS number FROM issues i
JOIN workstreams w ON w.id = i.workstream
WHERE i.repository = ? AND i.number = CAST(sqlc.arg(number) AS INTEGER);

-- name: ListCopiedTree :many
SELECT i.id, i.parent, CAST(i.number AS INTEGER) AS number, i.repository AS issue_repository, i.title, i.state, i.author, i.metadata
FROM issues i
JOIN workstreams w ON w.id = i.workstream
WHERE w.repository = ? AND w.number = CAST(sqlc.arg(number) AS INTEGER)
ORDER BY i.number, i.id;

-- name: GetCopiedIssueTitle :one
SELECT title FROM issues WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: GetCopiedIssueBody :one
SELECT body FROM issues WHERE repository = ? AND number = CAST(sqlc.arg(number) AS INTEGER);

-- name: ListCopiedTreeBlockers :many
SELECT b.issue, bi.repository AS blocker_repository, CAST(bi.number AS INTEGER) AS number,
       bw.number AS blocker_workstream, bw.title AS blocker_workstream_title
FROM blockers b
JOIN issues i ON i.id = b.issue
JOIN workstreams w ON w.id = i.workstream
JOIN issues bi ON bi.id = b.blocker AND bi.state = 'open'
LEFT JOIN workstreams bw ON bw.id = bi.workstream AND bw.repository = w.repository AND bi.repository = w.repository
WHERE w.repository = ? AND w.number = CAST(sqlc.arg(number) AS INTEGER)
ORDER BY b.issue, bi.repository, bi.number;

-- name: ListCopiedIssuesWithLabels :many
SELECT w.repository, CAST(w.number AS INTEGER) AS workstream, CAST(i.number AS INTEGER) AS number, i.repository AS issue_repository,
    i.title, i.state, i.author,
    instr(json_extract(i.metadata, '$.github.labels'), json_quote(CAST(sqlc.arg(needs_human) AS TEXT))) > 0 AS has_needs_human,
    instr(json_extract(i.metadata, '$.github.labels'), json_quote(CAST(sqlc.arg(question) AS TEXT))) > 0 AS has_question
FROM issues i
JOIN workstreams w ON w.id = i.workstream
WHERE has_needs_human OR has_question
ORDER BY w.repository, w.number, i.number;

-- name: ListEndedTaskIssueLabels :many
SELECT DISTINCT CAST(i.number AS INTEGER) AS number, i.repository AS issue_repository, i.metadata,
    EXISTS (
        SELECT 1 FROM inbox_items n
        WHERE n.kind = 'lost' AND n.dismissed_at IS NULL AND n.issue = i.number AND i.repository = n.repository
    ) AS lost
FROM issues i
JOIN workstreams w ON w.id = i.workstream
WHERE w.repository = sqlc.arg(repository) AND i.state = 'open'
  AND EXISTS (SELECT 1 FROM tasks t WHERE t.issue = i.number AND t.state = 'ended' AND i.repository = t.repository)
  AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.issue = i.number AND t.state <> 'ended' AND i.repository = t.repository)
ORDER BY i.number;

-- name: AddMemoryVersion :exec
INSERT INTO memory_versions (repository, file, time, author, reason, text) VALUES (?, ?, ?, ?, ?, ?);

-- name: SetTaskApprovedReview :exec
UPDATE tasks SET approved_review = ? WHERE id = ?;

-- name: SetTaskRefusedReview :exec
UPDATE tasks SET refused_review = ?, auto_merge = 0 WHERE id = ?;

-- name: SetTaskAutoMerge :exec
UPDATE tasks SET auto_merge = ? WHERE id = ?;

-- name: AddTriagerTurn :exec
INSERT INTO triager_turns (repository, issue) VALUES (?, ?)
ON CONFLICT (repository, issue) DO NOTHING;

-- name: ListTriagerTurns :many
SELECT issue FROM triager_turns WHERE repository = ? ORDER BY issue;

-- name: EndTriagerTurn :exec
DELETE FROM triager_turns WHERE repository = ? AND issue = ?;

-- name: ListMemoryVersionsPage :many
SELECT id, time, author, reason, text FROM memory_versions WHERE repository = ? AND file = ? AND id < ? ORDER BY id DESC LIMIT ?;

-- name: GetMemoryVersion :one
SELECT id, repository, file, time, author, text FROM memory_versions WHERE id = ?;

-- name: GetMemoryVersionBefore :one
SELECT text FROM memory_versions WHERE repository = ? AND file = ? AND id < ? ORDER BY id DESC LIMIT 1;

-- name: GetNewestMemoryVersionID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) FROM memory_versions WHERE repository = ? AND file = ?;

-- name: ListCuratorReasonsAfter :many
SELECT file, reason FROM memory_versions WHERE repository = ? AND author = 'curator' AND id > ? ORDER BY id;

-- name: AddCuratorRequest :one
INSERT INTO curator_requests (repository, text) VALUES (?, ?) RETURNING id;

-- name: ListCuratorRequests :many
SELECT id, text FROM curator_requests WHERE repository = ? ORDER BY id;

-- name: DeleteCuratorRequestsUpTo :exec
DELETE FROM curator_requests WHERE repository = ? AND id <= ?;

-- name: DeleteCuratorRequest :exec
DELETE FROM curator_requests WHERE id = ?;

-- name: ListRecentMemoryVersions :many
SELECT time, author, file, reason FROM memory_versions WHERE repository = ? ORDER BY id DESC LIMIT 20;

-- name: CountSessionsEndedSinceCurator :one
SELECT count(*) FROM sessions s
WHERE s.repository = sqlc.arg(repository) AND s.role <> 'curator' AND s.ended_at IS NOT NULL
  AND julianday(s.ended_at) > coalesce((
      SELECT max(julianday(c.started_at)) FROM sessions c
      WHERE c.repository = sqlc.arg(repository) AND c.role = 'curator' AND (c.ended_at IS NULL OR c.end_reason IN ('done', 'failed', 'hung'))
  ), 0);

-- name: CountOrganizationSessionsEndedSinceCurator :one
SELECT count(*) FROM sessions s
WHERE substr(s.repository, 1, length(sqlc.arg(organization)) + 1) = sqlc.arg(organization) || '/'
  AND s.role <> 'curator' AND s.ended_at IS NOT NULL
  AND julianday(s.ended_at) > coalesce((
      SELECT max(julianday(c.started_at)) FROM sessions c
      WHERE c.repository = sqlc.arg(curator_repository) AND c.role = 'curator' AND (c.ended_at IS NULL OR c.end_reason IN ('done', 'failed', 'hung'))
  ), 0);

-- name: ListOrganizationSessionsEndedSince :many
SELECT id, role, repository, issue FROM sessions
WHERE substr(repository, 1, length(sqlc.arg(organization)) + 1) = sqlc.arg(organization) || '/'
  AND role <> 'curator' AND ended_at IS NOT NULL AND julianday(ended_at) > julianday(CAST(sqlc.arg(since) AS TEXT))
ORDER BY id;

-- name: ListToolCallUpdates :many
SELECT json FROM transcript
WHERE session = ? AND kind = 'update' AND json_extract(json, '$.update.sessionUpdate') IN ('tool_call', 'tool_call_update')
ORDER BY id;

-- name: GetLastDoneCuratorStart :one
SELECT started_at FROM sessions
WHERE repository = ? AND role = 'curator' AND end_reason = 'done'
ORDER BY julianday(started_at) DESC LIMIT 1;

-- name: ListOwnerMessagesSince :many
SELECT m.workstream, m.time, m.text,
       CAST(COALESCE((SELECT p.text FROM chat_messages p
                      WHERE p.organization = m.organization AND p.repository = m.repository AND p.workstream = m.workstream
                        AND p.id < m.id AND p.author IN ('Lead', 'tell_owner')
                      ORDER BY p.id DESC LIMIT 1), '') AS TEXT) AS lead_text
FROM chat_messages m
WHERE m.repository = sqlc.arg(repository) AND m.author = 'Owner' AND julianday(m.time) > julianday(CAST(sqlc.arg(since) AS TEXT))
ORDER BY m.id;

-- name: ListToolCallsSince :many
SELECT s.role, s.issue, t.time, t.json FROM transcript t
JOIN sessions s ON s.id = t.session
WHERE s.repository = sqlc.arg(repository) AND s.role <> 'curator' AND t.kind = 'mcp_call'
  AND julianday(t.time) > julianday(CAST(sqlc.arg(since) AS TEXT))
  AND json_extract(t.json, '$.tool') IN ('cannot_do', 'submit_review', 'start_fix_round')
  AND json_extract(t.json, '$.error') IS NULL
ORDER BY t.id;

-- name: ListHungSessionsSince :many
SELECT id, role, issue, ended_at FROM sessions
WHERE repository = sqlc.arg(repository) AND role <> 'curator' AND end_reason = 'hung'
  AND julianday(ended_at) > julianday(CAST(sqlc.arg(since) AS TEXT))
ORDER BY id;

-- name: ListRetryPromptsSince :many
SELECT s.role, s.issue, t.time, CAST(json_extract(t.json, '$.text') AS TEXT) AS text FROM transcript t
JOIN sessions s ON s.id = t.session
WHERE s.repository = sqlc.arg(repository) AND s.role <> 'curator' AND t.kind = 'prompt'
  AND julianday(t.time) > julianday(CAST(sqlc.arg(since) AS TEXT))
  AND json_extract(t.json, '$.check') IS NULL
  AND substr(json_extract(t.json, '$.text'), 1, length(CAST(sqlc.arg(prefix) AS TEXT))) = CAST(sqlc.arg(prefix) AS TEXT)
ORDER BY t.id;

-- name: IsCommentAnswered :one
SELECT EXISTS (SELECT 1 FROM answered_comments WHERE repository = ? AND review = ? AND comment = ?);

-- name: MarkCommentAnswered :exec
INSERT INTO answered_comments (repository, review, comment) VALUES (?, ?, ?)
ON CONFLICT (repository, review, comment) DO NOTHING;

-- name: SetTaskLongWaitAt :exec
UPDATE tasks SET long_wait_at = ? WHERE id = ?;

-- name: SetTaskCheckErrors :exec
UPDATE tasks SET check_errors = ? WHERE id = ?;

-- name: AddCIBreak :exec
INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, run_event, rerun_at, result, started_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SetCIBreakRunEvent :exec
UPDATE ci_breaks SET run_event = ? WHERE id = ?;

-- name: ListOpenCIBreaks :many
SELECT * FROM ci_breaks WHERE repository = ? AND ended_at = '' ORDER BY id;

-- name: ListOpenHardCIBreaks :many
SELECT * FROM ci_breaks WHERE repository = ? AND ended_at = '' AND result = 'hard' ORDER BY id;

-- name: HasDiagnosingHardCIBreak :one
SELECT EXISTS (
  SELECT 1 FROM ci_breaks JOIN ci_diagnoses ON ci_diagnoses.ci_break = ci_breaks.id
  WHERE ci_breaks.repository = ? AND ci_breaks.issue = 0 AND ci_breaks.result = 'hard' AND ci_breaks.ended_at = ''
    AND ci_diagnoses.ended_at = ''
);

-- name: ListAllOpenHardCIBreaks :many
SELECT * FROM ci_breaks WHERE ended_at = '' AND result = 'hard' AND issue != 0 ORDER BY repository, id;

-- name: SetCIBreakRerun :exec
UPDATE ci_breaks SET rerun_at = ? WHERE id = ?;

-- name: SetCIBreakResult :exec
UPDATE ci_breaks SET result = ? WHERE id = ?;

-- name: EndCIBreak :exec
UPDATE ci_breaks SET result = ?, ended_run_id = ?, ended_at = ? WHERE id = ?;

-- name: NewestCIBreakRun :one
SELECT CAST(COALESCE(MAX(run_id), 0) AS INTEGER) FROM ci_breaks WHERE repository = ? AND workflow_id = ?;

-- name: ListCIBreaksWithoutIssue :many
SELECT * FROM ci_breaks
WHERE ci_breaks.repository = ? AND issue = 0 AND (result = 'flaky' OR result = 'hard' AND ended_at = '')
  AND NOT EXISTS (SELECT 1 FROM ci_diagnoses WHERE ci_diagnoses.ci_break = ci_breaks.id AND ci_diagnoses.ended_at = '')
ORDER BY id;

-- name: ListCIBreaksToDiagnose :many
SELECT * FROM ci_breaks
WHERE ci_breaks.repository = ? AND issue = 0 AND (result = 'flaky' OR result = 'hard' AND ended_at = '')
  AND NOT EXISTS (SELECT 1 FROM ci_diagnoses WHERE ci_diagnoses.ci_break = ci_breaks.id)
ORDER BY id;

-- name: GetCIBreak :one
SELECT * FROM ci_breaks WHERE id = ?;

-- name: ListCIBreaksNotBelowWorkstream :many
SELECT * FROM ci_breaks WHERE repository = ? AND issue < 0 ORDER BY id;

-- name: SetCIBreakIssue :exec
UPDATE ci_breaks SET issue = ? WHERE id = ?;

-- name: AddCheckTimeout :exec
INSERT INTO check_timeouts (repository, task_issue, session, timeout, output, created_at)
SELECT ?1, ?2, ?3, ?4, ?5, ?6
WHERE NOT EXISTS (SELECT 1 FROM check_timeouts WHERE repository = ?1 AND ended_at = '');

-- name: GetOpenCheckTimeout :one
SELECT * FROM check_timeouts WHERE repository = ? AND ended_at = '';

-- name: SetCheckTimeoutIssue :exec
UPDATE check_timeouts SET issue = ? WHERE id = ?;

-- name: EndCheckTimeout :exec
UPDATE check_timeouts SET ended_at = ? WHERE id = ?;

-- name: AddInitFailure :exec
INSERT INTO init_failures (repository, task_issue, session, commit_sha, result, output, created_at)
SELECT ?1, ?2, ?3, ?4, ?5, ?6, ?7
WHERE NOT EXISTS (SELECT 1 FROM init_failures WHERE repository = ?1 AND ended_at = '');

-- name: GetOpenInitFailure :one
SELECT * FROM init_failures WHERE repository = ? AND ended_at = '';

-- name: SetInitFailureIssue :exec
UPDATE init_failures SET issue = ? WHERE id = ?;

-- name: EndInitFailure :exec
UPDATE init_failures SET ended_at = ? WHERE id = ?;

-- name: AddOwnerAnswer :exec
INSERT OR IGNORE INTO owner_answers (repository, comment_id) VALUES (?, ?);

-- name: IsOwnerAnswer :one
SELECT EXISTS (SELECT 1 FROM owner_answers WHERE repository = ? AND comment_id = ?);

-- name: SetTaskBreakWaitHead :exec
UPDATE tasks SET break_wait_head = ? WHERE id = ?;

-- name: SetTaskRerunHead :exec
UPDATE tasks SET rerun_head = ?, rerun_runs = ? WHERE id = ?;

-- name: AddPendingAction :one
INSERT INTO pending_actions (organization, repository, workstream, issue, role, tool, arguments, state, time)
VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?)
RETURNING *;

-- name: GetPendingAction :one
SELECT * FROM pending_actions WHERE id = ?;

-- name: GetPendingActionByChatMessage :one
SELECT * FROM pending_actions WHERE chat_message = ?;

-- name: GetPendingActionByCreatedWorkstream :one
SELECT * FROM pending_actions WHERE created_workstream = ?;

-- name: GetPendingActionByInboxItem :one
SELECT * FROM pending_actions WHERE inbox_item = ?;

-- name: LinkPendingAction :one
UPDATE pending_actions SET chat_message = ?, inbox_item = ? WHERE id = ?
RETURNING *;

-- name: ListReplaceablePendingActions :many
SELECT * FROM pending_actions
WHERE organization = ? AND repository = ? AND workstream = ? AND issue = ? AND role = ? AND tool = ?
  AND state = 'pending' AND running = 0 AND id <> ?
ORDER BY id;

-- name: ReplacePendingAction :one
UPDATE pending_actions SET state = 'replaced', decided_at = ?
WHERE id = ? AND state = 'pending' AND running = 0
RETURNING *;

-- name: WithdrawPendingAction :one
UPDATE pending_actions SET state = 'withdrawn', decided_at = ?
WHERE id = ? AND state = 'pending' AND running = 0
RETURNING *;

-- name: ClaimPendingAction :one
UPDATE pending_actions SET running = 1 WHERE id = ? AND state = 'pending' AND running = 0
RETURNING *;

-- name: FinishPendingAction :one
UPDATE pending_actions SET state = 'done', running = 0, error = '', result = ?, message = ?, decided_at = ?
WHERE id = ?
RETURNING *;

-- name: FailPendingAction :one
UPDATE pending_actions SET running = 0, error = ? WHERE id = ?
RETURNING *;

-- name: RejectPendingAction :one
UPDATE pending_actions SET state = 'rejected', message = ?, decided_at = ?
WHERE id = ? AND state = 'pending' AND running = 0
RETURNING *;

-- name: SetPendingActionCreatedWorkstream :exec
UPDATE pending_actions SET created_workstream = ? WHERE id = ?;

-- name: ClearPendingActionCreatedWorkstream :exec
UPDATE pending_actions SET created_workstream = NULL, awaits_number = 0 WHERE created_workstream = ?;

-- The result of the action told that the number comes later. It gives 0 when the row already has its number.
-- name: SetPendingActionAwaitsNumber :execrows
UPDATE pending_actions SET awaits_number = 1
WHERE pending_actions.id = ? AND EXISTS (SELECT 1 FROM workstreams WHERE workstreams.id = pending_actions.created_workstream AND workstreams.number IS NULL);

-- name: ClearPendingActionAwaitsNumber :exec
UPDATE pending_actions SET awaits_number = 0 WHERE id = ?;

-- name: SetPendingActionOldBrief :exec
UPDATE pending_actions SET old_brief = ? WHERE id = ?;

-- name: FailRunningPendingActions :exec
UPDATE pending_actions SET running = 0, error = ? WHERE running = 1;

-- name: AddCIDiagnosis :execrows
INSERT INTO ci_diagnoses (repository, pull_request, ci_break, head, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (repository, pull_request, ci_break, head) DO NOTHING;

-- name: GetPullRequestCIDiagnosis :one
SELECT * FROM ci_diagnoses WHERE repository = ? AND pull_request = ? AND ci_break = 0 AND head = ?;

-- name: GetCIDiagnosisBySession :one
SELECT * FROM ci_diagnoses WHERE session = ?;

-- name: NextCIDiagnosis :one
SELECT * FROM ci_diagnoses WHERE repository = ? AND ended_at = '' ORDER BY id LIMIT 1;

-- name: SetCIDiagnosisSession :exec
UPDATE ci_diagnoses SET session = ?, reasons = '' WHERE id = ?;

-- name: AddCIDiagnosisReason :exec
UPDATE ci_diagnoses SET reasons = reasons || ? WHERE id = ?;

-- name: EndCIDiagnosis :exec
UPDATE ci_diagnoses SET ended_at = ? WHERE id = ?;

-- name: ListOpenCIFailures :many
SELECT ci_failures.id, ci_failures.reason, ci_failures.test, ci_failures.location, COUNT(ci_failure_places.id) AS places
FROM ci_failures LEFT JOIN ci_failure_places ON ci_failure_places.failure = ci_failures.id
WHERE ci_failures.repository = ? AND ci_failures.ended_at = ''
GROUP BY ci_failures.id
ORDER BY ci_failures.id;

-- name: GetOpenCIFailure :one
SELECT * FROM ci_failures WHERE id = ? AND repository = ? AND ended_at = '';

-- name: FindOpenCIFailure :one
SELECT * FROM ci_failures WHERE repository = ? AND ended_at = '' AND test = ? AND location = ? ORDER BY id LIMIT 1;

-- name: AddCIFailure :one
INSERT INTO ci_failures (repository, reason, test, location, created_at) VALUES (?, ?, ?, ?, ?)
RETURNING id;

-- name: AddCIFailurePlace :exec
INSERT INTO ci_failure_places (failure, pull_request, ci_break, head, job_urls, lines, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (failure, pull_request, ci_break) DO UPDATE SET head = excluded.head, job_urls = excluded.job_urls, lines = excluded.lines;

-- name: ListCIFailuresWithoutIssue :many
SELECT ci_failures.* FROM ci_failures
WHERE ci_failures.repository = ? AND issue = 0 AND ended_at = ''
  AND (SELECT COUNT(*) FROM ci_failure_places WHERE failure = ci_failures.id) >= 2
  AND NOT EXISTS (
    SELECT 1 FROM ci_failure_places JOIN ci_breaks ON ci_breaks.id = ci_failure_places.ci_break
    WHERE ci_failure_places.failure = ci_failures.id AND ci_breaks.issue = 0
      AND (ci_breaks.result = 'flaky' OR ci_breaks.result = 'hard' AND ci_breaks.ended_at = '')
  )
ORDER BY id;

-- name: ListCIFailuresNotBelowWorkstream :many
SELECT * FROM ci_failures WHERE repository = ? AND issue < 0 AND ended_at = '' ORDER BY id;

-- name: SetCIFailureIssue :exec
UPDATE ci_failures SET issue = ? WHERE id = ?;

-- name: ListCIFailuresWithIssue :many
SELECT * FROM ci_failures WHERE repository = ? AND issue <> 0 AND ended_at = '' ORDER BY id;

-- name: EndCIFailure :exec
UPDATE ci_failures SET ended_at = ? WHERE id = ?;

-- name: SetCIFailurePlacesUpdated :exec
UPDATE ci_failure_places SET branch_updated_at = ? WHERE failure = ? AND branch_updated_at = '';

-- name: SetCIFailurePlaceUpdated :exec
UPDATE ci_failure_places SET branch_updated_at = ? WHERE id = ?;

-- name: ListEndedCIFailurePullRequestPlaces :many
SELECT ci_failure_places.* FROM ci_failure_places JOIN ci_failures ON ci_failures.id = ci_failure_places.failure
WHERE ci_failures.repository = sqlc.arg(repository) AND ci_failures.ended_at <> '' AND ci_failure_places.pull_request <> 0
  AND (ci_failure_places.branch_updated_at = '' OR ci_failure_places.branch_updated_at > sqlc.arg(updated_after))
ORDER BY ci_failure_places.id;

-- name: GetNewestTaskPullRequest :one
SELECT pull_request FROM tasks WHERE repository = ? AND issue = ? ORDER BY id DESC LIMIT 1;

-- name: GetCIBreakFailureIssue :one
SELECT ci_failures.issue FROM ci_failures JOIN ci_failure_places ON ci_failure_places.failure = ci_failures.id
WHERE ci_failure_places.ci_break = ? AND ci_failures.issue != 0 AND ci_failures.ended_at = ''
ORDER BY ci_failures.id LIMIT 1;

-- name: SetCIBreakFailuresIssue :exec
UPDATE ci_failures SET issue = ?
WHERE issue = 0 AND ended_at = '' AND id IN (SELECT failure FROM ci_failure_places WHERE ci_break = ?);

-- name: IsRepairIssue :one
SELECT EXISTS (
  SELECT 1 FROM ci_breaks WHERE ci_breaks.repository = ?1 AND ABS(ci_breaks.issue) = ?2
  UNION ALL
  SELECT 1 FROM check_timeouts WHERE check_timeouts.repository = ?1 AND ABS(check_timeouts.issue) = ?2
  UNION ALL
  SELECT 1 FROM init_failures WHERE init_failures.repository = ?1 AND ABS(init_failures.issue) = ?2
  UNION ALL
  SELECT 1 FROM ci_failures WHERE ci_failures.repository = ?1 AND ABS(ci_failures.issue) = ?2
);

-- name: HasOpenPullRequestFailureWithIssue :one
SELECT EXISTS (
  SELECT 1 FROM ci_failures JOIN ci_failure_places ON ci_failure_places.failure = ci_failures.id
  WHERE ci_failures.repository = ? AND ci_failures.ended_at = '' AND ABS(ci_failures.issue) = ? AND ci_failure_places.pull_request != 0
);

-- name: ListCIFailurePlaces :many
SELECT ci_failure_places.*, COALESCE(ci_breaks.run_url, '') AS run_url
FROM ci_failure_places LEFT JOIN ci_breaks ON ci_breaks.id = ci_failure_places.ci_break
WHERE ci_failure_places.failure = ?
ORDER BY ci_failure_places.id;

-- name: AddLeadWrite :exec
INSERT INTO lead_writes (session, repository, issue, kind, labels, text, created_at) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListLeadWrites :many
SELECT * FROM lead_writes WHERE repository = ? COLLATE NOCASE AND (issue = ? OR kind = 'created') ORDER BY id;

-- name: SetLeadWriteItem :exec
UPDATE lead_writes SET item = ? WHERE id = ? AND item IS NULL;

-- name: DeleteDeploymentNeeds :exec
DELETE FROM deployment_needs WHERE task = ?;

-- name: SetTaskMergeQueuedAt :exec
UPDATE tasks SET merge_queued_at = ? WHERE id = ?;

-- name: ClearTaskMergeQueuedAt :execrows
UPDATE tasks SET merge_queued_at = NULL, merge_head_at = NULL WHERE id = ? AND merge_queued_at IS NOT NULL;

-- name: SetTaskMergeHeadAt :exec
UPDATE tasks SET merge_head_at = ? WHERE id = ? AND merge_head_at IS NULL;

-- name: GetMergeQueueHead :one
SELECT * FROM tasks
WHERE repository = ? AND state <> 'ended' AND merge_queued_at IS NOT NULL
ORDER BY merge_head_at IS NULL, NOT EXISTS (
  SELECT 1 FROM ci_fix_issues WHERE ci_fix_issues.repository = tasks.repository AND ci_fix_issues.issue = tasks.issue
), julianday(merge_queued_at), id
LIMIT 1;

-- name: ListMergeQueueTasks :many
SELECT * FROM tasks
WHERE repository = ? AND state <> 'ended' AND merge_queued_at IS NOT NULL
ORDER BY merge_head_at IS NULL, NOT EXISTS (
  SELECT 1 FROM ci_fix_issues WHERE ci_fix_issues.repository = tasks.repository AND ci_fix_issues.issue = tasks.issue
), julianday(merge_queued_at), id;

-- name: ListAllMergeQueueTasks :many
SELECT id, repository FROM tasks
WHERE state <> 'ended' AND merge_queued_at IS NOT NULL
ORDER BY merge_head_at IS NULL, NOT EXISTS (
  SELECT 1 FROM ci_fix_issues WHERE ci_fix_issues.repository = tasks.repository AND ci_fix_issues.issue = tasks.issue
), julianday(merge_queued_at), id;

-- name: GetTask :one
SELECT * FROM tasks WHERE id = ?;

-- name: AddDeploymentNeed :exec
INSERT INTO deployment_needs (task, head, kind, name, path, line, reason, mandatory, after_merge) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListDeploymentNeeds :many
SELECT * FROM deployment_needs WHERE task = ? ORDER BY id;

-- name: AddDeploymentTodo :exec
INSERT INTO deployment_todos (task, inbox_item, kind, name, path, line, reason, mandatory, after_merge) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListDeploymentTodos :many
SELECT * FROM deployment_todos WHERE task = ? ORDER BY id;

-- name: GetDeploymentTodoOfItem :one
SELECT * FROM deployment_todos WHERE inbox_item = ?;

-- name: SetDeploymentTodoDone :exec
UPDATE deployment_todos SET note = ?, done_at = ? WHERE id = ?;

-- name: SetDeploymentTodoDismissed :exec
UPDATE deployment_todos SET dismissed_at = ? WHERE id = ?;

-- name: SetDeploymentTodoMandatory :exec
UPDATE deployment_todos SET mandatory = ? WHERE id = ?;

-- name: ReopenDeploymentTodo :exec
UPDATE deployment_todos SET mandatory = ?, dismissed_at = NULL, inbox_item = ? WHERE id = ?;

-- name: SetInboxItemReason :one
UPDATE inbox_items SET reason = ? WHERE id = ?
RETURNING *;

-- A Workstream needs buckets when it has more than the maximum of direct sub-issues, or when an open bucket has no
-- sub-issues or holds a closed issue.
-- name: ListWorkstreamsNeedingBuckets :many
SELECT CAST(w.number AS INTEGER) AS number FROM workstreams w
WHERE w.repository = sqlc.arg(repository) AND w.state = 'open' AND w.number IS NOT NULL
  AND ((SELECT COUNT(*) FROM issues c
        WHERE c.workstream = w.id AND c.parent IS NULL AND json_extract(c.metadata, '$.github.bucket') IS NULL)
       + (SELECT COUNT(*) FROM json_each(w.metadata, '$.github.buckets')) > CAST(sqlc.arg(maximum) AS INTEGER)
       OR EXISTS (
           SELECT 1 FROM json_each(w.metadata, '$.github.buckets') b
           WHERE json_extract(b.value, '$.state') = 'open'
             AND (NOT EXISTS (SELECT 1 FROM issues c
                              WHERE c.workstream = w.id AND json_extract(c.metadata, '$.github.bucket') = json_extract(b.value, '$.number'))
                  OR EXISTS (SELECT 1 FROM issues c
                             WHERE c.workstream = w.id AND json_extract(c.metadata, '$.github.bucket') = json_extract(b.value, '$.number')
                               AND c.state = 'closed'))))
ORDER BY w.number;

-- name: ListWorkstreamBuckets :many
SELECT b.workstream, b.issue, CAST(b.closed AS BOOLEAN) AS closed FROM workstream_buckets b
JOIN workstreams w ON w.repository = b.repository AND w.number = b.workstream AND w.state = 'open'
WHERE b.repository = ?
ORDER BY b.issue;

-- name: AddWorkstreamBucket :exec
INSERT INTO workstream_buckets (repository, workstream, issue, closed) VALUES (?, ?, ?, CAST(sqlc.arg(closed) AS BOOLEAN));

-- name: DeleteWorkstreamBucket :exec
DELETE FROM workstream_buckets WHERE repository = ? AND issue = ?;

-- name: ListGlobalPauses :many
SELECT * FROM global_pauses ORDER BY kind;

-- name: AddGlobalPause :exec
INSERT INTO global_pauses (kind, paused_at) VALUES (?, ?)
ON CONFLICT (kind) DO NOTHING;

-- name: DeleteGlobalPause :exec
DELETE FROM global_pauses WHERE kind = ?;

-- name: AddHeldWork :execrows
INSERT INTO held_work (kind, repository, issue, held_at)
SELECT ?1, ?2, ?3, ?4 WHERE EXISTS (SELECT 1 FROM global_pauses WHERE kind = ?1)
ON CONFLICT (kind, repository, issue) DO NOTHING;

-- name: DeleteHeldWork :exec
DELETE FROM held_work WHERE kind = ?;

-- name: ListHeldWork :many
SELECT h.kind, h.repository, h.issue, h.held_at, i.title, w.repository AS workstream_repository, CAST(w.number AS INTEGER) AS workstream,
       w.title AS workstream_title
FROM held_work h
JOIN global_pauses p ON p.kind = h.kind
JOIN issues i ON i.number = h.issue AND i.state = 'open' AND i.repository = h.repository
JOIN workstreams w ON w.id = i.workstream AND w.state = 'open'
ORDER BY julianday(h.held_at), h.kind, h.repository, h.issue;

-- name: ListRoleSettings :many
SELECT * FROM role_settings;

-- name: UpsertRoleSetting :exec
INSERT INTO role_settings (role, harness, model, effort, max, context, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (role) DO UPDATE SET harness = excluded.harness, model = excluded.model, effort = excluded.effort, max = excluded.max, context = excluded.context, updated_at = excluded.updated_at;

-- name: DeleteRoleSetting :exec
DELETE FROM role_settings WHERE role = ?;

-- name: GetIssueBinding :one
SELECT * FROM issue_bindings WHERE repository = ? AND issue = ? AND role = ?;

-- name: ListIssueBindings :many
SELECT * FROM issue_bindings ORDER BY repository, issue, role;

-- A row is live when its task issue is open in a copied tree, or its Workstream issue is copied.
-- name: ListLiveIssueBindings :many
SELECT b.* FROM issue_bindings b
WHERE EXISTS (SELECT 1 FROM workstreams w WHERE b.repository = w.repository AND w.number = b.issue AND w.state = 'open')
   OR EXISTS (SELECT 1 FROM issues i WHERE i.number = b.issue AND i.state = 'open' AND i.workstream IS NOT NULL AND i.repository = b.repository)
ORDER BY b.repository, b.issue, b.role;

-- name: ListWorkstreamSettingUsers :many
SELECT b.harness, b.role, w.repository, b.issue AS number, w.title
FROM issue_bindings b
JOIN workstreams w ON b.repository = w.repository AND b.issue = w.number AND w.state = 'open'
ORDER BY w.repository, b.issue, b.role;

-- name: ListTaskSettingUsers :many
SELECT b.harness, b.role, b.repository, CAST(i.number AS INTEGER) AS number, i.title,
       w.repository AS workstream_repository, CAST(w.number AS INTEGER) AS workstream, w.title AS workstream_title
FROM issue_bindings b
JOIN issues i ON i.number = b.issue AND i.state = 'open' AND i.repository = b.repository
JOIN workstreams w ON w.id = i.workstream AND w.state = 'open'
ORDER BY b.repository, b.issue, b.role;

-- name: UpsertIssueBinding :exec
INSERT INTO issue_bindings (repository, issue, role, harness, model, effort, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (repository, issue, role) DO UPDATE SET harness = excluded.harness, model = excluded.model, effort = excluded.effort, updated_at = excluded.updated_at;

-- name: DeleteIssueBinding :exec
DELETE FROM issue_bindings WHERE repository = ? AND issue = ? AND role = ?;

-- name: ListBlockerEdges :many
SELECT w.repository, CAST(w.number AS INTEGER) AS workstream, i.repository AS issue_repository, CAST(i.number AS INTEGER) AS number,
       bi.repository AS blocker_repository, CAST(bi.number AS INTEGER) AS blocker_number
FROM blockers b
JOIN issues i ON i.id = b.issue
JOIN workstreams w ON w.id = i.workstream
JOIN issues bi ON bi.id = b.blocker AND bi.state = 'open';

-- name: ListBlockerCycles :many
SELECT cycle, repository, workstream FROM blocker_cycles;

-- name: AddBlockerCycle :exec
INSERT INTO blocker_cycles (cycle, repository, workstream) VALUES (?, ?, ?);

-- name: DeleteBlockerCycle :exec
DELETE FROM blocker_cycles WHERE cycle = ?;

-- name: GetVoiceLanguage :one
SELECT * FROM voice_language WHERE id = 1;

-- name: UpsertVoiceLanguage :exec
INSERT INTO voice_language (id, language, updated_at) VALUES (1, ?, ?)
ON CONFLICT (id) DO UPDATE SET language = excluded.language, updated_at = excluded.updated_at;

-- name: AddSkillRejection :exec
INSERT INTO skill_rejections (organization, name, reason, rejected_at) VALUES (?, ?, ?, ?)
ON CONFLICT (organization, name) DO UPDATE SET reason = excluded.reason, rejected_at = excluded.rejected_at;

-- name: ListSkillRejections :many
SELECT name, reason, rejected_at FROM skill_rejections WHERE organization = ? ORDER BY name;

-- name: ListToolAutoUpgrades :many
SELECT * FROM tool_auto_upgrades ORDER BY package;

-- name: AddToolAutoUpgrade :exec
INSERT INTO tool_auto_upgrades (package) VALUES (?) ON CONFLICT (package) DO NOTHING;

-- name: DeleteToolAutoUpgrade :exec
DELETE FROM tool_auto_upgrades WHERE package = ?;

-- name: SetToolAutoUpgradeFailure :exec
UPDATE tool_auto_upgrades SET failed_version = ? WHERE package = ?;

-- name: AddRecurringTask :one
INSERT INTO recurring_tasks (repository, workstream, cron, text, checked_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: ListRecurringTasks :many
SELECT * FROM recurring_tasks
WHERE repository = ? AND workstream = ?
ORDER BY id;

-- name: GetRecurringTask :one
SELECT * FROM recurring_tasks
WHERE id = ? AND repository = ? AND workstream = ?;

-- name: ListRecurringTaskRuns :many
SELECT * FROM recurring_task_runs
WHERE recurring_task = ?
ORDER BY id DESC;

-- name: ListAllRecurringTasks :many
SELECT t.* FROM recurring_tasks t
JOIN workstreams w ON w.repository = t.repository AND w.number = t.workstream AND w.state = 'open'
WHERE t.paused = 0
ORDER BY t.id;

-- A new cron expression gives no event for a time before the change.
-- name: UpdateRecurringTask :one
UPDATE recurring_tasks
SET checked_at = CASE WHEN cron != sqlc.arg(cron) THEN sqlc.arg(checked_at) ELSE checked_at END,
    cron = sqlc.arg(cron), text = sqlc.arg(text)
WHERE id = sqlc.arg(id) AND repository = sqlc.arg(repository) AND workstream = sqlc.arg(workstream)
RETURNING *;

-- A resume gives no event for the times of the pause.
-- name: SetRecurringTaskPaused :one
UPDATE recurring_tasks
SET paused = CAST(sqlc.arg(paused) AS BOOLEAN), checked_at = sqlc.arg(checked_at)
WHERE id = sqlc.arg(id) AND repository = sqlc.arg(repository) AND workstream = sqlc.arg(workstream)
RETURNING *;

-- name: DeleteRecurringTask :execrows
DELETE FROM recurring_tasks WHERE id = ? AND repository = ? AND workstream = ?;

-- name: DeleteRecurringTaskRuns :exec
DELETE FROM recurring_task_runs WHERE recurring_task = ?;

-- name: DeleteWorkstreamRecurringTaskRuns :exec
DELETE FROM recurring_task_runs
WHERE recurring_task IN (SELECT id FROM recurring_tasks WHERE repository = ? AND workstream = ?);

-- name: DeleteWorkstreamRecurringTasks :exec
DELETE FROM recurring_tasks WHERE repository = ? AND workstream = ?;

-- The row changes only when the task still has the cron expression and the check time that the step read, and is not
-- paused. Else a person changed the task during the step.
-- name: AdvanceRecurringTask :execrows
UPDATE recurring_tasks SET checked_at = sqlc.arg(checked_at), event = coalesce(sqlc.narg(event), event)
WHERE id = sqlc.arg(id) AND cron = sqlc.arg(cron) AND checked_at = sqlc.arg(read_checked_at) AND paused = 0;

-- name: SetRecurringTaskEvent :execrows
UPDATE recurring_tasks SET event = ? WHERE id = ? AND repository = ? AND workstream = ?;

-- name: HasUnchangedRecurringTask :one
SELECT EXISTS (
    SELECT 1 FROM recurring_tasks
    WHERE id = ? AND cron = ? AND checked_at = ? AND paused = 0
);

-- name: AddRecurringTaskRun :exec
INSERT INTO recurring_task_runs (recurring_task, time, result, reason) VALUES (?, ?, ?, ?);

-- name: TrimRecurringTaskRuns :exec
DELETE FROM recurring_task_runs
WHERE recurring_task_runs.recurring_task = sqlc.arg(task)
  AND recurring_task_runs.id NOT IN (SELECT newest.id FROM recurring_task_runs AS newest WHERE newest.recurring_task = sqlc.arg(task) ORDER BY newest.id DESC LIMIT 20);

-- name: HasUndeliveredLeadEvent :one
SELECT EXISTS (SELECT 1 FROM lead_events WHERE id = ? AND delivered_at IS NULL);

-- name: AddOwnerAction :exec
INSERT INTO owner_actions (task, inbox_item, role, action, next, created_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetOwnerActionOfItem :one
SELECT * FROM owner_actions WHERE inbox_item = ?;

-- name: SetOwnerActionDone :exec
UPDATE owner_actions SET done_at = ? WHERE id = ?;

-- name: ListOpenOwnerActionsOfTask :many
SELECT * FROM owner_actions WHERE task = ? AND done_at IS NULL ORDER BY id;

-- name: AddMcpServer :one
INSERT INTO mcp_servers (organization, name, kind, url, headers, command, args, env, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListMcpServers :many
SELECT * FROM mcp_servers
WHERE organization = ?
ORDER BY name;

-- name: GetMcpServer :one
SELECT * FROM mcp_servers
WHERE id = ? AND organization = ?;

-- name: UpdateMcpServer :one
UPDATE mcp_servers
SET name = ?, kind = ?, url = ?, headers = ?, command = ?, args = ?, env = ?, updated_at = ?
WHERE id = ? AND organization = ?
RETURNING *;

-- name: DeleteMcpServer :execrows
DELETE FROM mcp_servers WHERE id = ? AND organization = ?;

-- name: ListWorkstreamMcpServers :many
SELECT s.* FROM mcp_servers s
JOIN workstream_mcp_servers w ON w.server = s.id
WHERE w.repository = ? AND w.workstream = ?
ORDER BY s.name;

-- name: AddWorkstreamMcpServer :exec
INSERT INTO workstream_mcp_servers (repository, workstream, server) VALUES (?, ?, ?);

-- name: DeleteWorkstreamMcpServers :exec
DELETE FROM workstream_mcp_servers WHERE repository = ? AND workstream = ?;

-- name: GetMcpServerOAuth :one
SELECT * FROM mcp_server_oauth WHERE server = ?;

-- name: ListMcpServerOAuth :many
SELECT o.* FROM mcp_server_oauth o
JOIN mcp_servers s ON s.id = o.server
WHERE s.organization = ?;

-- name: GetMcpServerOAuthByState :one
SELECT * FROM mcp_server_oauth WHERE state = ? AND state <> '';

-- name: StartMcpServerOAuth :exec
INSERT INTO mcp_server_oauth (
    server, client_id, client_secret, issuer, token_endpoint, resource, scope, state, code_verifier, flow_started_at,
    access_token, refresh_token, expires_at, failure, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', NULL, '', ?)
ON CONFLICT (server) DO UPDATE SET
    client_id = excluded.client_id, client_secret = excluded.client_secret, issuer = excluded.issuer,
    token_endpoint = excluded.token_endpoint, resource = excluded.resource, scope = excluded.scope,
    state = excluded.state, code_verifier = excluded.code_verifier, flow_started_at = excluded.flow_started_at,
    updated_at = excluded.updated_at;

-- name: StoreMcpServerOAuthTokens :exec
UPDATE mcp_server_oauth
SET access_token = ?, refresh_token = ?, expires_at = ?, state = '', code_verifier = '', flow_started_at = NULL,
    failure = '', updated_at = ?
WHERE server = ? AND state = ?;

-- name: StoreMcpServerOAuthRefresh :exec
UPDATE mcp_server_oauth
SET access_token = sqlc.arg(access_token), refresh_token = sqlc.arg(refresh_token), expires_at = sqlc.arg(expires_at), updated_at = sqlc.arg(updated_at)
WHERE server = sqlc.arg(server) AND access_token = sqlc.arg(current_access_token) AND refresh_token = sqlc.arg(current_refresh_token);

-- name: FailMcpServerOAuthToken :exec
UPDATE mcp_server_oauth SET failure = ?, updated_at = ? WHERE server = ? AND access_token = ?;

-- name: ClearMcpServerOAuthTokens :exec
UPDATE mcp_server_oauth SET access_token = '', refresh_token = '', expires_at = NULL WHERE server = ?;

-- name: FailMcpServerOAuth :exec
UPDATE mcp_server_oauth
SET state = '', code_verifier = '', flow_started_at = NULL, failure = ?, updated_at = ?
WHERE server = ? AND state = ?;

-- name: EndMcpServerOAuthFlow :exec
UPDATE mcp_server_oauth
SET state = '', code_verifier = '', flow_started_at = NULL, updated_at = ?
WHERE server = ? AND state = ?;

-- name: DeleteMcpServerOAuth :exec
DELETE FROM mcp_server_oauth WHERE server = ?;

-- name: GetIssueRepositoryChoice :one
SELECT repository FROM issue_repositories WHERE organization = ?;

-- name: SetIssueRepositoryChoice :exec
INSERT OR REPLACE INTO issue_repositories (organization, repository) VALUES (?, ?);

-- name: GetLastRelease :one
SELECT tag FROM last_release WHERE id = 1;

-- name: SetLastRelease :exec
INSERT INTO last_release (id, tag) VALUES (1, ?)
ON CONFLICT (id) DO UPDATE SET tag = excluded.tag;

-- name: ReleaseChangelogSent :one
SELECT EXISTS (SELECT 1 FROM release_changelogs WHERE organization = ? AND tag = ?);

-- name: AddReleaseChangelog :exec
INSERT INTO release_changelogs (organization, tag, sent_at) VALUES (?, ?, ?);

-- name: ListImplementerBindings :many
SELECT * FROM implementer_bindings ORDER BY id;

-- name: AddImplementerBinding :one
INSERT INTO implementer_bindings (harness, model, effort, context, max, updated_at) VALUES (?, ?, ?, ?, ?, ?) RETURNING id;

-- name: UpdateImplementerBinding :exec
UPDATE implementer_bindings SET harness = ?, model = ?, effort = ?, context = ?, max = ?, updated_at = ? WHERE id = ?;

-- name: DeleteImplementerBinding :exec
DELETE FROM implementer_bindings WHERE id = ?;

-- name: ListOpenWorkstreamsOfOrganization :many
SELECT w.repository, CAST(w.number AS INTEGER) AS number, CAST(w.maintenance AS BOOLEAN) AS maintenance, CAST(w.skills AS BOOLEAN) AS skills
FROM workstreams w
WHERE w.state = 'open' AND w.number IS NOT NULL AND substr(w.repository, 1, length(sqlc.arg(organization)) + 1) = sqlc.arg(organization) || '/'
ORDER BY w.repository, w.number;

-- name: ListMissingWorkstreamDismissals :many
SELECT organization, kind FROM missing_workstream_dismissals;

-- name: AddMissingWorkstreamDismissal :exec
INSERT OR IGNORE INTO missing_workstream_dismissals (organization, kind) VALUES (?, ?);
