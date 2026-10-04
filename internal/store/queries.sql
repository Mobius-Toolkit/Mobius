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
