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
