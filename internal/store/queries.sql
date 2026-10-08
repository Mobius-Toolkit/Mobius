-- name: ListCopiedWorkstreams :many
SELECT w.repository, w.number, w.title, w.body, CAST(w.autopilot AS BOOLEAN) AS autopilot,
       CAST(EXISTS (SELECT 1 FROM copied_issues i
               WHERE i.repository = w.repository AND i.workstream = w.number AND i.parent = w.number)
       AND NOT EXISTS (SELECT 1 FROM copied_issues i
                       WHERE i.repository = w.repository AND i.workstream = w.number AND i.parent = w.number
                         AND i.state != 'closed') AS BOOLEAN) AS all_tasks_closed
FROM copied_workstreams w
ORDER BY w.repository, w.number DESC;

-- name: ListLatestEvents :many
SELECT * FROM events ORDER BY id DESC LIMIT ?;

-- name: ListEventsAfter :many
SELECT * FROM events WHERE id > ? ORDER BY id;

-- name: AddEvent :exec
INSERT INTO events (time, repository, workstream, issue, actor, text, link) VALUES (?, ?, ?, ?, ?, ?, ?);

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

-- name: GetSession :one
SELECT * FROM sessions WHERE id = ?;

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

-- name: DismissInboxItem :one
UPDATE inbox_items SET dismissed_at = ? WHERE id = ?
RETURNING *;

-- name: ReopenInboxItem :one
UPDATE inbox_items SET text = ?, time = ?, dismissed_at = NULL
WHERE id = ? AND (dismissed_at IS NULL OR julianday(dismissed_at) > julianday(CAST(sqlc.arg(retry_since) AS TEXT)))
RETURNING *;

-- name: ListOpenInboxItems :many
SELECT * FROM inbox_items WHERE dismissed_at IS NULL ORDER BY id;

-- name: AddChatMessage :one
INSERT INTO chat_messages (organization, repository, workstream, author, time, text)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: DeleteChatMessage :exec
DELETE FROM chat_messages WHERE id = ?;

-- name: AppendChatMessage :one
UPDATE chat_messages SET text = text || sqlc.arg(text) WHERE id = sqlc.arg(id)
RETURNING *;

-- The chat shows no Researcher message.
-- name: ListChatMessages :many
SELECT * FROM chat_messages
WHERE organization = ? AND repository = ? AND workstream = ? AND author <> 'Researcher'
ORDER BY id;

-- name: ListChatMessagesBefore :many
SELECT * FROM chat_messages
WHERE organization = ? AND repository = ? AND workstream = ? AND id < ?
ORDER BY id DESC LIMIT ?;

-- name: SetChatSeen :exec
INSERT INTO chat_seen (organization, repository, workstream, message) VALUES (?, ?, ?, ?)
ON CONFLICT (organization, repository, workstream) DO UPDATE SET message = max(message, excluded.message);

-- The messages of the Owner, of a Researcher and of an event are never unread.
-- name: ListUnread :many
SELECT m.organization, m.repository, m.workstream, count(*) AS count
FROM chat_messages m
LEFT JOIN chat_seen s ON s.organization = m.organization AND s.repository = m.repository AND s.workstream = m.workstream
WHERE m.author NOT IN ('Owner', 'Researcher', 'Event') AND m.id > coalesce(s.message, 0)
GROUP BY m.organization, m.repository, m.workstream
ORDER BY m.organization, m.repository, m.workstream;

-- name: CountUnread :one
SELECT count(*) FROM chat_messages m
WHERE m.organization = sqlc.arg(organization) AND m.repository = sqlc.arg(repository) AND m.workstream = sqlc.arg(workstream)
  AND m.author NOT IN ('Owner', 'Researcher', 'Event')
  AND m.id > coalesce((SELECT s.message FROM chat_seen s
                       WHERE s.organization = m.organization AND s.repository = m.repository AND s.workstream = m.workstream), 0);

-- name: AddLeadEvent :exec
INSERT INTO lead_events (repository, workstream, issue, kind, payload, time, chat_message)
VALUES (?, ?, ?, ?, ?, ?, ?);

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
        AND earlier.id < event.id AND earlier.delivered_at IS NULL AND earlier.held = 1
  )
ORDER BY event.id;

-- name: HoldLeadEvent :exec
UPDATE lead_events SET held = 1 WHERE id = ?;

-- name: FreeLeadEvents :many
UPDATE lead_events SET held = 0 WHERE repository = ? AND workstream = ? AND held = 1
RETURNING *;

-- name: ListWaitingLeadWorkstreams :many
SELECT DISTINCT repository, workstream FROM lead_events WHERE delivered_at IS NULL ORDER BY repository, workstream;

-- name: AddTask :one
INSERT INTO tasks (repository, issue, workstream, state, dispatched_at) VALUES (?, ?, ?, 'dispatched', ?)
RETURNING *;

-- An ended task counts too.
-- name: HasTask :one
SELECT EXISTS (SELECT 1 FROM tasks WHERE repository = ? AND issue = ?);

-- name: CountActiveTasks :one
SELECT count(*) FROM tasks WHERE state IN ('dispatched', 'queued', 'working');

-- name: ResetTaskCounters :exec
UPDATE tasks SET fix_rounds = 0, review_rounds = 0, worker_restarts = 0 WHERE id = ?;

-- name: EndTask :exec
UPDATE tasks SET state = 'ended' WHERE id = ?;

-- name: QueueTask :execrows
UPDATE tasks SET state = 'queued', queued_at = sqlc.arg(queued_at) WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- A restart keeps the place of the task in the queue.
-- name: RequeueTask :execrows
UPDATE tasks SET state = 'queued' WHERE id = ? AND state IN ('queued', 'working');

-- name: SetTaskWorker :exec
UPDATE tasks SET worker = ?, worker_input = ? WHERE id = ?;

-- name: StartTaskWorker :execrows
UPDATE tasks SET state = 'working', worker = sqlc.arg(worker), worker_input = sqlc.arg(worker_input)
WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- name: SetTaskBranch :exec
UPDATE tasks SET branch = ? WHERE id = ?;

-- name: SetTaskPullRequest :exec
UPDATE tasks SET pull_request = ? WHERE id = ?;

-- name: AddFixRound :execrows
UPDATE tasks SET fix_rounds = fix_rounds + 1 WHERE id = sqlc.arg(id) AND fix_rounds < sqlc.arg(max);

-- name: SetTaskCheckHead :exec
UPDATE tasks SET check_head = ? WHERE id = ?;

-- name: AddReviewRound :exec
UPDATE tasks SET review_rounds = review_rounds + 1 WHERE id = ?;

-- name: GetReviewComment :one
SELECT review_comment FROM tasks WHERE id = ?;

-- name: SetReviewComment :exec
UPDATE tasks SET review_comment = ? WHERE id = ?;

-- name: SetJudgedAt :exec
UPDATE tasks SET judged_at = ? WHERE id = ?;

-- name: StopTask :execrows
UPDATE tasks SET state = 'stopped' WHERE id = ? AND state NOT IN ('stopped', 'ended');

-- name: ListLiveTaskRepositories :many
SELECT DISTINCT repository FROM tasks WHERE state <> 'ended' ORDER BY repository;

-- name: ListTaskPullRequests :many
SELECT CAST(pull_request AS INTEGER) FROM tasks WHERE repository = ? AND workstream = ? AND pull_request IS NOT NULL ORDER BY id;

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
INSERT INTO copied_workstreams (repository, number, title, body, autopilot)
VALUES (sqlc.arg(repository), sqlc.arg(number), sqlc.arg(title), sqlc.arg(body), CAST(sqlc.arg(autopilot) AS BOOLEAN))
ON CONFLICT (repository, number) DO NOTHING;

-- name: AddCopiedIssue :exec
INSERT INTO copied_issues (repository, workstream, position, number, parent, title, body, state, author, html_url, repository_url)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: AddCopiedIssueLabel :exec
INSERT INTO copied_issue_labels (repository, workstream, position, name) VALUES (?, ?, ?, ?);

-- name: AddCopiedBlocker :exec
INSERT INTO copied_blockers (repository, workstream, position, number, blocker_workstream, blocker_workstream_title)
VALUES (?, ?, ?, ?, ?, ?);

-- name: HasCopiedWorkstream :one
SELECT EXISTS (SELECT 1 FROM copied_workstreams WHERE repository = ? AND number = ?);

-- A row of another repository can have the number of an issue of this repository, so the repository URL must match.
-- name: GetCopiedIssueWorkstream :one
SELECT workstream FROM copied_issues WHERE repository = ? AND number = ? AND repository_url = ? LIMIT 1;

-- name: ListCopiedWorkstreamsWithLabelChange :many
SELECT i.workstream FROM copied_issues i
WHERE i.repository = sqlc.arg(repository) AND i.number = sqlc.arg(number) AND i.repository_url = sqlc.arg(repository_url)
  AND EXISTS (
      SELECT 1 FROM copied_issue_labels l
      WHERE l.repository = i.repository AND l.workstream = i.workstream AND l.position = i.position AND l.name = sqlc.arg(name)
  ) != CAST(sqlc.arg(present) AS BOOLEAN);

-- name: ListCopiedWorkstreamsWithBlockerIn :many
SELECT DISTINCT workstream FROM copied_blockers WHERE repository = ? AND blocker_workstream = ?;

-- name: ListCopiedWorkstreamsWithBlocker :many
SELECT DISTINCT workstream FROM copied_blockers WHERE repository = ? AND number = ?;

-- name: UpdateCopiedWorkstream :execrows
UPDATE copied_workstreams SET title = sqlc.arg(title), body = sqlc.arg(body), autopilot = CAST(sqlc.arg(autopilot) AS BOOLEAN)
WHERE repository = sqlc.arg(repository) AND number = sqlc.arg(number)
  AND (title != sqlc.arg(title) OR body != sqlc.arg(body) OR autopilot != CAST(sqlc.arg(autopilot) AS BOOLEAN));

-- name: UpdateCopiedBlockerTitles :execrows
UPDATE copied_blockers SET blocker_workstream_title = sqlc.arg(title)
WHERE repository = sqlc.arg(repository) AND blocker_workstream = sqlc.arg(workstream)
  AND blocker_workstream_title IS NOT sqlc.arg(title);

-- name: SetCopiedAutopilot :exec
UPDATE copied_workstreams SET autopilot = CAST(sqlc.arg(autopilot) AS BOOLEAN)
WHERE repository = sqlc.arg(repository) AND number = sqlc.arg(number);

-- name: DeleteCopiedBlocker :execrows
DELETE FROM copied_blockers WHERE repository = ? AND number = ?;

-- A tree can hold an issue of another repository as a leaf, so the change goes to the rows of all repositories.
-- name: UpdateCopiedIssue :execrows
UPDATE copied_issues SET title = sqlc.arg(title), body = sqlc.arg(body), state = sqlc.arg(state), author = sqlc.arg(author)
WHERE number = sqlc.arg(number) AND repository_url = sqlc.arg(repository_url)
  AND (title != sqlc.arg(title) OR body != sqlc.arg(body) OR state != sqlc.arg(state) OR author != sqlc.arg(author));

-- name: ListCopiedIssueRows :many
SELECT repository, workstream, position FROM copied_issues WHERE number = ? AND repository_url = ?;

-- name: ListCopiedIssueLabels :many
SELECT name FROM copied_issue_labels WHERE repository = ? AND workstream = ? AND position = ? ORDER BY name;

-- name: DeleteCopiedIssueLabelsAt :exec
DELETE FROM copied_issue_labels WHERE repository = ? AND workstream = ? AND position = ?;

-- name: ListCopiedTree :many
SELECT position, number, parent, title, state, author, html_url, repository_url FROM copied_issues
WHERE repository = ? AND workstream = ? ORDER BY position;

-- name: ListCopiedTreeLabels :many
SELECT position, name FROM copied_issue_labels WHERE repository = ? AND workstream = ? ORDER BY position, name;

-- name: ListCopiedTreeBlockers :many
SELECT position, number, blocker_workstream, blocker_workstream_title FROM copied_blockers
WHERE repository = ? AND workstream = ? ORDER BY position, number;

-- name: ListCopiedIssuesWithLabel :many
SELECT i.repository, i.workstream, i.number, i.title, i.state, i.author, i.html_url, i.repository_url FROM copied_issues i
WHERE EXISTS (
    SELECT 1 FROM copied_issue_labels l
    WHERE l.repository = i.repository AND l.workstream = i.workstream AND l.position = i.position AND l.name = sqlc.arg(name)
)
ORDER BY i.repository, i.workstream, i.number;

-- name: AddMemoryVersion :exec
INSERT INTO memory_versions (repository, time, author, reason, text) VALUES (?, ?, ?, ?, ?);

-- name: ListRecentMemoryVersions :many
SELECT time, author, reason FROM memory_versions WHERE repository = ? ORDER BY id DESC LIMIT 20;

-- name: CountSessionsEndedSinceCurator :one
SELECT count(*) FROM sessions s
WHERE s.repository = sqlc.arg(repository) AND s.role <> 'curator' AND s.ended_at IS NOT NULL
  AND julianday(s.ended_at) > coalesce((SELECT max(julianday(c.started_at)) FROM sessions c WHERE c.repository = s.repository AND c.role = 'curator' AND (c.ended_at IS NULL OR c.end_reason = 'done')), 0);
