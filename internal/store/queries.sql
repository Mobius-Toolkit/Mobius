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
