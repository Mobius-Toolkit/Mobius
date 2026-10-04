-- name: ListWorkstreams :many
WITH activity AS (
    SELECT repository, workstream, dispatched_at AS time FROM tasks
    UNION ALL
    SELECT repository, workstream, started_at FROM sessions
    UNION ALL
    SELECT repository, workstream, time FROM chat_messages
    UNION ALL
    SELECT repository, workstream, time FROM lead_events
    UNION ALL
    SELECT repository, workstream, time FROM events
    UNION ALL
    SELECT repository, workstream, time FROM inbox_items
)
SELECT
    activity.repository,
    activity.workstream,
    CAST(MAX(activity.time) AS TEXT) AS last_activity,
    (SELECT COUNT(*) FROM tasks
     WHERE tasks.repository = activity.repository AND tasks.workstream = activity.workstream) AS tasks,
    (SELECT COUNT(*) FROM tasks
     WHERE tasks.repository = activity.repository AND tasks.workstream = activity.workstream
       AND tasks.state NOT IN ('ended', 'stopped')) AS open_tasks
FROM activity
WHERE activity.repository != '' AND activity.workstream != 0
GROUP BY activity.repository, activity.workstream
ORDER BY last_activity DESC;

-- name: ListLatestEvents :many
SELECT * FROM events ORDER BY id DESC LIMIT ?;

-- name: ListEventsAfter :many
SELECT * FROM events WHERE id > ? ORDER BY id;

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
INSERT INTO sessions (role, harness, model, organization, repository, workstream, issue, parent, started_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: SetACPSessionID :exec
UPDATE sessions SET acp_session_id = ? WHERE id = ?;

-- name: EndSession :one
UPDATE sessions SET ended_at = ?, end_reason = ?, queue_reason = NULL WHERE id = ?
RETURNING *;

-- name: ListSessions :many
SELECT * FROM sessions WHERE organization = ? AND repository = ? AND workstream = ? ORDER BY id;

-- name: AddTranscriptRow :one
INSERT INTO transcript (session, time, kind, json) VALUES (?, ?, ?, ?)
RETURNING *;

-- name: SetTranscriptJSON :one
UPDATE transcript SET json = ? WHERE id = ?
RETURNING *;

-- name: ListTranscript :many
SELECT * FROM transcript WHERE session = ? ORDER BY id;

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

-- name: ListOpenSessionIDs :many
SELECT id FROM sessions WHERE ended_at IS NULL ORDER BY id;

-- name: ListOpenSessions :many
SELECT sqlc.embed(sessions), w.title AS workstream_title, i.title AS issue_title,
       (SELECT t.pull_request FROM tasks t
        WHERE t.repository = sessions.repository AND t.issue = sessions.issue
        ORDER BY t.id DESC LIMIT 1) AS pull_request
FROM sessions
LEFT JOIN copied_workstreams w ON w.repository = sessions.repository AND w.number = sessions.workstream
LEFT JOIN copied_issues i
  ON i.repository = sessions.repository AND i.workstream = sessions.workstream AND i.number = sessions.issue
WHERE sessions.ended_at IS NULL ORDER BY sessions.id;

-- name: ListQueuedTasks :many
SELECT id, queued_at FROM tasks WHERE state = 'queued' ORDER BY queued_at, id;

-- name: SetTaskState :execrows
UPDATE tasks SET state = sqlc.arg(state) WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- name: AddWorkerRestart :one
UPDATE tasks SET worker_restarts = worker_restarts + 1 WHERE id = sqlc.arg(id) AND worker_restarts < sqlc.arg(max)
RETURNING worker_restarts;

-- name: GetHarnessPause :one
SELECT * FROM harness_pauses WHERE harness = ?;

-- name: ListHarnessPauses :many
SELECT * FROM harness_pauses ORDER BY harness;

-- name: SetHarnessPause :exec
INSERT INTO harness_pauses (harness, paused_until, inbox_item) VALUES (?, ?, ?)
ON CONFLICT (harness) DO UPDATE SET paused_until = excluded.paused_until, inbox_item = excluded.inbox_item;

-- name: DeleteHarnessPause :exec
DELETE FROM harness_pauses WHERE harness = ?;

-- name: AddInboxItem :one
INSERT INTO inbox_items (kind, organization, repository, workstream, issue, text, link, time)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: DismissInboxItem :exec
UPDATE inbox_items SET dismissed_at = ? WHERE id = ?;

-- name: AddChatMessage :one
INSERT INTO chat_messages (organization, repository, workstream, author, time, text)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: AddLeadEvent :exec
INSERT INTO lead_events (repository, workstream, issue, kind, payload, time, chat_message)
VALUES (?, ?, ?, ?, ?, ?, ?);
