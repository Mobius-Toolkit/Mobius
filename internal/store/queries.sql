-- name: ListCopiedWorkstreams :many
SELECT w.repository, w.number, w.title, w.body, CAST(w.autopilot AS BOOLEAN) AS autopilot,
       CAST(w.auto_merge AS BOOLEAN) AS auto_merge,
       CAST(w.evergreen AS BOOLEAN) AS evergreen,
       CAST(EXISTS (SELECT 1 FROM copied_issues i
               WHERE i.repository = w.repository AND i.workstream = w.number AND i.parent = w.number AND i.parent_repository = w.repository)
       AND NOT EXISTS (SELECT 1 FROM copied_issues i
                       WHERE i.repository = w.repository AND i.workstream = w.number AND i.parent = w.number AND i.parent_repository = w.repository
                         AND i.state != 'closed') AS BOOLEAN) AS all_tasks_closed,
       CAST(EXISTS (SELECT 1 FROM tasks t
               WHERE t.workstream_repository = w.repository AND t.workstream = w.number AND t.state = 'ready_for_review') AS BOOLEAN) AS ready_to_merge
FROM copied_workstreams w
ORDER BY w.repository, w.number DESC;

-- name: ListLatestEvents :many
SELECT * FROM events ORDER BY id DESC LIMIT ?;

-- name: ListEventsAfter :many
SELECT * FROM events WHERE id > ? ORDER BY id;

-- name: AddEvent :exec
INSERT INTO events (time, organization, repository, workstream, issue, actor, text, link) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

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
SELECT * FROM turn_usage WHERE started_at >= sqlc.arg(after) AND started_at < sqlc.arg(before);

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
WITH working AS (
    SELECT ts.task, ts.started_at, ts.harness, ts.model, ts.effort
    FROM task_states ts
    WHERE ts.state = 'working'
      AND ts.id = (SELECT MIN(inner_ts.id) FROM task_states inner_ts
                   WHERE inner_ts.task = ts.task AND inner_ts.state = 'working')
)
SELECT t.repository AS repository,
       (SELECT ts.ended_at FROM task_states ts WHERE ts.task = t.id
        ORDER BY julianday(ts.ended_at) DESC LIMIT 1) AS ended_at,
       working.harness AS harness,
       working.model AS model,
       working.effort AS effort,
       working.started_at AS working_started_at,
       (SELECT COUNT(*) FROM task_states ts WHERE ts.task = t.id AND ts.state = 'needs_human') AS needs_human,
       (SELECT COUNT(*) FROM task_states ts WHERE ts.task = t.id AND ts.state = 'asked') AS questions,
       (SELECT COUNT(*) FROM sessions s
        WHERE s.repository = t.repository AND s.issue = t.issue
          AND s.role = 'reviewer' AND s.end_reason = 'done'
          AND julianday(s.started_at) >= julianday((SELECT ts.started_at FROM task_states ts
                                                    WHERE ts.task = t.id ORDER BY ts.id LIMIT 1))
          AND julianday(s.started_at) <= julianday((SELECT ts.ended_at FROM task_states ts
                                                    WHERE ts.task = t.id
                                                    ORDER BY julianday(ts.ended_at) DESC LIMIT 1))) AS review_rounds,
       (SELECT COUNT(*) FROM sessions s
        WHERE s.repository = t.repository AND s.issue = t.issue
          AND s.role = 'implementer'
          AND (s.end_reason IS NULL OR s.end_reason <> 'restart')
          AND julianday(s.started_at) >= julianday((SELECT ts.started_at FROM task_states ts
                                                    WHERE ts.task = t.id ORDER BY ts.id LIMIT 1))
          AND julianday(s.started_at) <= julianday((SELECT ts.ended_at FROM task_states ts
                                                    WHERE ts.task = t.id
                                                    ORDER BY julianday(ts.ended_at) DESC LIMIT 1))) AS implementer_sessions,
       (SELECT SUM(tu.input_tokens) FROM turn_usage tu WHERE tu.task = t.id) AS input_tokens,
       (SELECT SUM(tu.output_tokens) FROM turn_usage tu WHERE tu.task = t.id) AS output_tokens,
       (SELECT SUM(tu.cache_read_tokens) FROM turn_usage tu WHERE tu.task = t.id) AS cache_read_tokens,
       (SELECT SUM(tu.cache_write_tokens) FROM turn_usage tu WHERE tu.task = t.id) AS cache_write_tokens,
       (SELECT SUM(tu.cost_usd) FROM turn_usage tu WHERE tu.task = t.id) AS cost_usd,
       EXISTS (
           SELECT 1 FROM lead_events le
           WHERE le.kind = 'end' AND le.issue_repository = t.repository AND le.issue = t.issue
             AND julianday(le.time) >= julianday((SELECT ts.ended_at FROM task_states ts
                                                  WHERE ts.task = t.id
                                                  ORDER BY julianday(ts.ended_at) DESC LIMIT 1))
             AND julianday(le.time) <= julianday((SELECT ts.ended_at FROM task_states ts
                                                  WHERE ts.task = t.id
                                                  ORDER BY julianday(ts.ended_at) DESC LIMIT 1), '+1 minute')
             AND substr(le.payload, -8) = ' merged.'
       ) AS merged
FROM tasks t
LEFT JOIN working ON working.task = t.id
WHERE t.state = 'ended'
  AND EXISTS (SELECT 1 FROM task_states ts WHERE ts.task = t.id)
ORDER BY julianday(ended_at), t.id;

-- name: AddStepTime :exec
INSERT INTO step_times (kind, session, task, issue, workstream, organization, repository, role, harness, model, effort,
                        started_at, ended_at, attempt, result)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SetACPSessionID :exec
UPDATE sessions SET acp_session_id = ? WHERE id = ?;

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
LEFT JOIN copied_workstreams w ON w.repository = sessions.workstream_repository AND w.number = sessions.workstream
LEFT JOIN copied_issues i
  ON i.repository = sessions.workstream_repository AND i.workstream = sessions.workstream AND i.number = sessions.issue
  AND substr(lower(i.repository_url), -(length(sessions.repository) + 7)) = '/repos/' || lower(sessions.repository)
WHERE sessions.ended_at IS NULL ORDER BY sessions.id;

-- name: ListBoardTasks :many
SELECT tasks.repository, tasks.issue, tasks.workstream_repository, tasks.workstream, tasks.state, tasks.state_at,
       tasks.pull_request, tasks.fix_rounds, tasks.review_rounds,
       i.title AS issue_title, i.html_url AS issue_url, w.title AS workstream_title,
       coalesce(m.merging, 0) AS merging, coalesce(m.merge_refusal, '') AS merge_refusal
FROM tasks
LEFT JOIN inbox_items m ON m.id = (
  SELECT max(id) FROM inbox_items
  WHERE kind = 'ready for review' AND repository = tasks.repository AND issue = tasks.issue AND pull_request = tasks.pull_request
)
JOIN copied_workstreams w ON w.repository = tasks.workstream_repository AND w.number = tasks.workstream
JOIN copied_issues i
  ON i.repository = tasks.workstream_repository AND i.workstream = tasks.workstream AND i.number = tasks.issue
  AND substr(lower(i.repository_url), -(length(tasks.repository) + 7)) = '/repos/' || lower(tasks.repository)
ORDER BY tasks.id;

-- name: ListQueuedTasks :many
SELECT id, queued_at, EXISTS (
  SELECT 1 FROM ci_breaks WHERE ci_breaks.repository = tasks.repository AND ci_breaks.issue = tasks.issue
    AND ci_breaks.result = 'hard' AND ci_breaks.ended_at = ''
) AS ci FROM tasks WHERE state = 'queued' ORDER BY queued_at, id;

-- name: SetTaskState :execrows
UPDATE tasks SET state = sqlc.arg(state), state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), long_wait_at = NULL
WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- name: HandTaskToHuman :execrows
UPDATE tasks SET state = 'needs_human', needs_human_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), long_wait_at = NULL
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
INSERT INTO chat_messages (organization, repository, workstream, author, time, text, browser_id, inbox_items)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
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

-- The chat shows no Researcher message, no Curator message and no Approval message.
-- name: ListChatMessagesPage :many
SELECT * FROM chat_messages
WHERE organization = ? AND repository = ? AND workstream = ? AND author NOT IN ('Researcher', 'Curator', 'Approval') AND id < ?
ORDER BY id DESC LIMIT ?;

-- name: ListChatMessagesBefore :many
SELECT * FROM chat_messages
WHERE organization = ? AND repository = ? AND workstream = ? AND author != 'Record' AND id < ?
  AND cancelled_at IS NULL
ORDER BY id DESC LIMIT ?;

-- name: SetChatSeen :exec
INSERT INTO chat_seen (organization, repository, workstream, message) VALUES (?, ?, ?, ?)
ON CONFLICT (organization, repository, workstream) DO UPDATE SET message = max(message, excluded.message);

-- The messages of the Owner, of a Researcher, of a Curator, of an approval, of an event and of a record are never unread.
-- name: ListUnread :many
SELECT m.organization, m.repository, m.workstream, count(*) AS count
FROM chat_messages m
LEFT JOIN chat_seen s ON s.organization = m.organization AND s.repository = m.repository AND s.workstream = m.workstream
WHERE m.author NOT IN ('Owner', 'Researcher', 'Curator', 'Approval', 'Event', 'Record') AND m.id > coalesce(s.message, 0)
GROUP BY m.organization, m.repository, m.workstream
ORDER BY m.organization, m.repository, m.workstream;

-- name: CountUnread :one
SELECT count(*) FROM chat_messages m
WHERE m.organization = sqlc.arg(organization) AND m.repository = sqlc.arg(repository) AND m.workstream = sqlc.arg(workstream)
  AND m.author NOT IN ('Owner', 'Researcher', 'Curator', 'Approval', 'Event', 'Record')
  AND m.id > coalesce((SELECT s.message FROM chat_seen s
                       WHERE s.organization = m.organization AND s.repository = m.repository AND s.workstream = m.workstream), 0);

-- name: AddLeadEvent :exec
INSERT INTO lead_events (repository, workstream, issue_repository, issue, kind, payload, time, chat_message, comment, review)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

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
      SELECT 1 FROM copied_issues AS i
      JOIN copied_issue_labels AS l ON l.repository = i.repository AND l.workstream = i.workstream AND l.position = i.position
      WHERE i.repository = event.repository AND i.workstream = event.workstream AND i.state = 'open' AND l.name = sqlc.arg(question_label)
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

-- name: ListActiveTasks :many
SELECT repository, issue, state, state_at FROM tasks WHERE state IN ('dispatched', 'queued', 'working') ORDER BY id;

-- name: StartFullSlots :exec
INSERT INTO full_slots (id, since) VALUES (1, ?) ON CONFLICT DO NOTHING;

-- name: GetFullSlots :one
SELECT * FROM full_slots;

-- name: SetFullSlotsItemAt :exec
UPDATE full_slots SET item_at = ? WHERE item_at IS NULL;

-- name: EndFullSlots :exec
DELETE FROM full_slots;

-- name: ResetTaskCounters :exec
UPDATE tasks SET fix_rounds = 0, review_rounds = 0, worker_restarts = 0, check_head = NULL WHERE id = ?;

-- name: EndTask :exec
UPDATE tasks SET state = 'ended', lead_findings = '', state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), long_wait_at = NULL WHERE id = ?;

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
UPDATE tasks SET state = 'working', worker = sqlc.arg(worker), worker_input = sqlc.arg(worker_input), state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), long_wait_at = NULL
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

-- name: SetTaskApprovedHead :exec
UPDATE tasks SET approved_head = ? WHERE id = ?;

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

-- name: AddCommentWrite :exec
INSERT INTO comment_writes (repository, number, comment, reply_to, body) VALUES (?, ?, ?, ?, ?);

-- name: ListCommentWrites :many
SELECT * FROM comment_writes WHERE repository = ? ORDER BY id;

-- name: DeleteCommentWrite :exec
DELETE FROM comment_writes WHERE id = ?;

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
UPDATE tasks SET state = 'stopped', state_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), long_wait_at = NULL WHERE id = ? AND state NOT IN ('stopped', 'ended');

-- name: ListLiveTaskRepositories :many
SELECT DISTINCT repository FROM tasks WHERE state <> 'ended' ORDER BY repository;

-- name: ListTaskPullRequests :many
SELECT repository, CAST(pull_request AS INTEGER) AS pull_request FROM tasks
WHERE workstream_repository = ? AND workstream = ? AND pull_request IS NOT NULL ORDER BY id;

-- name: ListWorkstreamLiveTasks :many
SELECT * FROM tasks WHERE workstream_repository = ? AND workstream = ? AND state <> 'ended' ORDER BY id;

-- name: ListCopiedRepositories :many
SELECT DISTINCT repository FROM copied_workstreams;

-- name: DeleteCopiedWorkstreamsOf :exec
DELETE FROM copied_workstreams WHERE repository = ?;

-- name: DeleteCopiedIssuesOf :exec
DELETE FROM copied_issues WHERE repository = ?;

-- name: DeleteCopiedIssueLabelsOf :exec
DELETE FROM copied_issue_labels WHERE repository = ?;

-- name: DeleteCopiedBlockersOf :exec
DELETE FROM copied_blockers WHERE repository = ?;

-- name: DeleteCopiedWorkstream :exec
DELETE FROM copied_workstreams WHERE repository = ? AND number = ?;

-- name: DeleteCopiedIssues :exec
DELETE FROM copied_issues WHERE repository = ? AND workstream = ?;

-- name: DeleteCopiedIssueLabels :exec
DELETE FROM copied_issue_labels WHERE repository = ? AND workstream = ?;

-- name: DeleteCopiedBlockers :exec
DELETE FROM copied_blockers WHERE repository = ? AND workstream = ?;

-- The Lead tool create_workstream and the poll can both add a new Workstream.
-- name: AddCopiedWorkstream :exec
INSERT INTO copied_workstreams (repository, number, title, body, autopilot, auto_merge, evergreen, skills, maintenance, created_at)
VALUES (sqlc.arg(repository), sqlc.arg(number), sqlc.arg(title), sqlc.arg(body), CAST(sqlc.arg(autopilot) AS BOOLEAN), CAST(sqlc.arg(auto_merge) AS BOOLEAN),
        CAST(sqlc.arg(evergreen) AS BOOLEAN), CAST(sqlc.arg(skills) AS BOOLEAN), CAST(sqlc.arg(maintenance) AS BOOLEAN), sqlc.arg(created_at))
ON CONFLICT (repository, number) DO NOTHING;

-- name: AddCopiedIssue :exec
INSERT INTO copied_issues (repository, workstream, position, number, parent, parent_repository, title, body, state, author, html_url, repository_url)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: AddCopiedIssueLabel :exec
INSERT INTO copied_issue_labels (repository, workstream, position, name) VALUES (?, ?, ?, ?);

-- name: AddCopiedBlocker :exec
INSERT INTO copied_blockers (repository, workstream, position, blocker_repository, number, blocker_workstream, blocker_workstream_title)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: HasCopiedWorkstream :one
SELECT EXISTS (SELECT 1 FROM copied_workstreams WHERE repository = ? AND number = ?);

-- A row of another repository can have the number of an issue of this repository, so the repository URL must match.
-- name: GetCopiedIssueWorkstream :one
SELECT workstream FROM copied_issues WHERE repository = ? AND number = ? AND repository_url = ? LIMIT 1;

-- The issue can be in the trees of the copies of many repositories.
-- name: ListCopiedWorkstreamsWithLabelChange :many
SELECT i.repository, i.workstream FROM copied_issues i
WHERE i.number = sqlc.arg(number) AND i.repository_url = sqlc.arg(repository_url)
  AND EXISTS (
      SELECT 1 FROM copied_issue_labels l
      WHERE l.repository = i.repository AND l.workstream = i.workstream AND l.position = i.position AND l.name = sqlc.arg(name)
  ) != CAST(sqlc.arg(present) AS BOOLEAN);

-- The Workstream with the label mobius:skills in a repository of the organization, with the lowest number.
-- name: GetSkillsWorkstream :one
SELECT repository, number FROM copied_workstreams
WHERE skills AND substr(repository, 1, length(sqlc.arg(organization)) + 1) = sqlc.arg(organization) || '/'
ORDER BY number, repository
LIMIT 1;

-- name: ListCopiedWorkstreamsWithBlockerIn :many
SELECT DISTINCT workstream FROM copied_blockers WHERE repository = ? AND blocker_workstream = ?;

-- name: ListCopiedWorkstreamsWithIssue :many
SELECT DISTINCT repository, workstream FROM copied_issues WHERE number = ? AND repository_url = ?;

-- name: ListCopiedWorkstreamsWithBlocker :many
SELECT DISTINCT workstream FROM copied_blockers WHERE repository = ? AND blocker_repository = ? AND number = ?;

-- name: UpdateCopiedWorkstream :execrows
UPDATE copied_workstreams SET title = sqlc.arg(title), body = sqlc.arg(body), autopilot = CAST(sqlc.arg(autopilot) AS BOOLEAN),
    auto_merge = CAST(sqlc.arg(auto_merge) AS BOOLEAN), evergreen = CAST(sqlc.arg(evergreen) AS BOOLEAN), skills = CAST(sqlc.arg(skills) AS BOOLEAN),
    maintenance = CAST(sqlc.arg(maintenance) AS BOOLEAN)
WHERE repository = sqlc.arg(repository) AND number = sqlc.arg(number)
  AND (title != sqlc.arg(title) OR body != sqlc.arg(body) OR autopilot != CAST(sqlc.arg(autopilot) AS BOOLEAN)
       OR auto_merge != CAST(sqlc.arg(auto_merge) AS BOOLEAN) OR evergreen != CAST(sqlc.arg(evergreen) AS BOOLEAN) OR skills != CAST(sqlc.arg(skills) AS BOOLEAN)
       OR maintenance != CAST(sqlc.arg(maintenance) AS BOOLEAN));

-- name: GetCopiedWorkstream :one
SELECT title, body, CAST(evergreen AS BOOLEAN) AS evergreen, CAST(maintenance AS BOOLEAN) AS maintenance, CAST(skills AS BOOLEAN) AS skills, created_at
FROM copied_workstreams WHERE repository = ? AND number = ?;

-- name: SetCopiedEvergreen :exec
UPDATE copied_workstreams SET evergreen = CAST(sqlc.arg(evergreen) AS BOOLEAN)
WHERE repository = sqlc.arg(repository) AND number = sqlc.arg(number);

-- name: UpdateCopiedBlockerTitles :execrows
UPDATE copied_blockers SET blocker_workstream_title = sqlc.arg(title)
WHERE repository = sqlc.arg(repository) AND blocker_workstream = sqlc.arg(workstream)
  AND blocker_workstream_title IS NOT sqlc.arg(title);

-- name: ListAutopilotWorkstreams :many
SELECT number FROM copied_workstreams WHERE repository = ? AND autopilot ORDER BY number DESC;

-- name: GetCopiedAutopilot :one
SELECT CAST(autopilot AS BOOLEAN) AS autopilot, CAST(auto_merge AS BOOLEAN) AS auto_merge FROM copied_workstreams WHERE repository = ? AND number = ?;

-- name: SetCopiedAutopilot :exec
UPDATE copied_workstreams SET autopilot = CAST(sqlc.arg(autopilot) AS BOOLEAN), auto_merge = CAST(sqlc.arg(auto_merge) AS BOOLEAN)
WHERE repository = sqlc.arg(repository) AND number = sqlc.arg(number);

-- A tree can hold the blocker of an issue of another repository, so the delete goes to the rows of all repositories.
-- name: DeleteCopiedBlocker :execrows
DELETE FROM copied_blockers WHERE blocker_repository = ? AND number = ?;

-- A tree can hold an issue of another repository as a leaf, so the change goes to the rows of all repositories.
-- name: UpdateCopiedIssue :execrows
UPDATE copied_issues SET title = sqlc.arg(title), body = sqlc.arg(body), state = sqlc.arg(state), author = sqlc.arg(author)
WHERE number = sqlc.arg(number) AND repository_url = sqlc.arg(repository_url)
  AND (title != sqlc.arg(title) OR body != sqlc.arg(body) OR state != sqlc.arg(state) OR author != sqlc.arg(author));

-- name: GetCopiedIssueState :one
SELECT state FROM copied_issues WHERE number = ? AND repository_url = ? LIMIT 1;

-- name: ListCopiedIssueRows :many
SELECT repository, workstream, position FROM copied_issues WHERE number = ? AND repository_url = ?;

-- A tree can hold an issue of another repository with the same number, so the caller checks repository_url.
-- name: ListCopiedIssueRowsByNumber :many
SELECT repository, workstream, position, repository_url FROM copied_issues WHERE number = ?;

-- name: AddCopiedIssueLabelIfMissing :exec
INSERT INTO copied_issue_labels (repository, workstream, position, name) VALUES (?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: DeleteCopiedIssueLabel :exec
DELETE FROM copied_issue_labels WHERE repository = ? AND workstream = ? AND position = ? AND name = ?;

-- name: ListCopiedIssueLabels :many
SELECT name FROM copied_issue_labels WHERE repository = ? AND workstream = ? AND position = ? ORDER BY name;

-- name: DeleteCopiedIssueLabelsAt :exec
DELETE FROM copied_issue_labels WHERE repository = ? AND workstream = ? AND position = ?;

-- name: ListCopiedTree :many
SELECT position, number, parent, parent_repository, title, state, author, html_url, repository_url FROM copied_issues
WHERE repository = ? AND workstream = ? ORDER BY position;

-- A tree can hold an issue of another repository with the same number, so the caller checks repository_url.
-- name: ListCopiedIssuesByNumber :many
SELECT title, html_url, repository_url FROM copied_issues WHERE repository = ? AND workstream = ? AND number = ?;

-- name: ListCopiedTreeLabels :many
SELECT position, name FROM copied_issue_labels WHERE repository = ? AND workstream = ? ORDER BY position, name;

-- name: ListCopiedTreeBlockers :many
SELECT position, blocker_repository, number, blocker_workstream, blocker_workstream_title FROM copied_blockers
WHERE repository = ? AND workstream = ? ORDER BY position, blocker_repository, number;

-- name: ListCopiedIssuesWithLabels :many
SELECT i.repository, i.workstream, i.number, i.title, i.state, i.author, i.html_url, i.repository_url,
    EXISTS (
        SELECT 1 FROM copied_issue_labels l
        WHERE l.repository = i.repository AND l.workstream = i.workstream AND l.position = i.position AND l.name = sqlc.arg(needs_human)
    ) AS has_needs_human,
    EXISTS (
        SELECT 1 FROM copied_issue_labels l
        WHERE l.repository = i.repository AND l.workstream = i.workstream AND l.position = i.position AND l.name = sqlc.arg(question)
    ) AS has_question
FROM copied_issues i
WHERE has_needs_human OR has_question
ORDER BY i.repository, i.workstream, i.number;

-- name: ListEndedTaskIssueLabels :many
SELECT DISTINCT i.number, i.repository_url, l.name
FROM copied_issues i
JOIN copied_issue_labels l ON l.repository = i.repository AND l.workstream = i.workstream AND l.position = i.position
WHERE i.repository = sqlc.arg(repository) AND i.state = 'open'
  AND l.name IN (sqlc.arg(working), sqlc.arg(review), sqlc.arg(question), sqlc.arg(needs_human))
  AND EXISTS (SELECT 1 FROM tasks t WHERE t.issue = i.number AND t.state = 'ended'
      AND substr(lower(i.repository_url), -(length(t.repository) + 7)) = '/repos/' || lower(t.repository))
  AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.issue = i.number AND t.state <> 'ended'
      AND substr(lower(i.repository_url), -(length(t.repository) + 7)) = '/repos/' || lower(t.repository))
  AND NOT (l.name = sqlc.arg(needs_human) AND EXISTS (
      SELECT 1 FROM inbox_items n
      WHERE n.kind = 'lost' AND n.dismissed_at IS NULL AND n.issue = i.number
        AND substr(lower(i.repository_url), -(length(n.repository) + 7)) = '/repos/' || lower(n.repository)
  ))
ORDER BY i.number, l.name;

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
INSERT INTO ci_breaks (repository, workflow_id, workflow_name, run_id, run_url, run_attempt, rerun_at, result, started_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

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

-- name: SetPendingActionCreatedIssue :exec
UPDATE pending_actions SET created_issue = ? WHERE id = ?;

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

-- name: AddDeploymentNeed :exec
INSERT INTO deployment_needs (task, head, kind, name, path, line, reason, mandatory) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListDeploymentNeeds :many
SELECT * FROM deployment_needs WHERE task = ? ORDER BY id;

-- name: AddDeploymentTodo :exec
INSERT INTO deployment_todos (task, inbox_item, kind, name, path, line, reason, mandatory) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListDeploymentTodos :many
SELECT * FROM deployment_todos WHERE task = ? ORDER BY id;

-- name: GetDeploymentTodoOfItem :one
SELECT * FROM deployment_todos WHERE inbox_item = ?;

-- name: SetDeploymentTodoDone :exec
UPDATE deployment_todos SET note = ?, done_at = ? WHERE id = ?;

-- A Workstream needs buckets when it has more than the maximum of direct sub-issues, or when an open bucket has no
-- sub-issues or holds a closed issue.
-- name: ListWorkstreamsNeedingBuckets :many
SELECT w.number FROM copied_workstreams w
WHERE w.repository = sqlc.arg(repository)
  AND ((SELECT COUNT(*) FROM copied_issues c
        WHERE c.repository = w.repository AND c.workstream = w.number AND c.parent = w.number AND c.parent_repository = w.repository) > CAST(sqlc.arg(maximum) AS INTEGER)
       OR EXISTS (
           SELECT 1 FROM copied_issues b
           JOIN copied_issue_labels l
             ON l.repository = b.repository AND l.workstream = b.workstream AND l.position = b.position AND l.name = 'mobius:bucket'
           WHERE b.repository = w.repository AND b.workstream = w.number AND b.parent = w.number AND b.parent_repository = w.repository
             AND b.state = 'open'
             AND (NOT EXISTS (SELECT 1 FROM copied_issues c
                              WHERE c.repository = b.repository AND c.workstream = b.workstream AND c.parent = b.number AND c.parent_repository = w.repository)
                  OR EXISTS (SELECT 1 FROM copied_issues c
                             WHERE c.repository = b.repository AND c.workstream = b.workstream AND c.parent = b.number AND c.parent_repository = w.repository
                               AND c.state = 'closed'))))
ORDER BY w.number;

-- name: ListWorkstreamBuckets :many
SELECT b.workstream, b.issue, CAST(b.closed AS BOOLEAN) AS closed FROM workstream_buckets b
JOIN copied_workstreams w ON w.repository = b.repository AND w.number = b.workstream
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

-- The issue can be in the trees of the copies of many repositories, so a row can repeat. The API shows the first.
-- name: ListHeldWork :many
SELECT h.kind, h.repository, h.issue, h.held_at, i.title, i.repository AS workstream_repository, i.workstream,
       w.title AS workstream_title
FROM held_work h
JOIN global_pauses p ON p.kind = h.kind
JOIN copied_issues i ON i.number = h.issue AND i.state = 'open'
  AND lower(substr(i.repository_url, instr(i.repository_url, '/repos/') + 7)) = lower(h.repository)
JOIN copied_workstreams w ON w.repository = i.repository AND w.number = i.workstream
ORDER BY julianday(h.held_at), h.kind, h.repository, h.issue, i.repository, i.workstream;

-- name: ListRoleSettings :many
SELECT * FROM role_settings;

-- name: UpsertRoleSetting :exec
INSERT INTO role_settings (role, harness, model, effort, max, updated_at) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (role) DO UPDATE SET harness = excluded.harness, model = excluded.model, effort = excluded.effort, max = excluded.max, updated_at = excluded.updated_at;

-- name: DeleteRoleSetting :exec
DELETE FROM role_settings WHERE role = ?;

-- name: ListBlockerEdges :many
SELECT b.repository, b.workstream, i.repository_url, i.number, b.blocker_repository, b.number AS blocker_number
FROM copied_blockers b
JOIN copied_issues i ON i.repository = b.repository AND i.workstream = b.workstream AND i.position = b.position;

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
