CREATE TABLE owner_actions (
    id INTEGER PRIMARY KEY,
    task INTEGER NOT NULL,
    inbox_item INTEGER NOT NULL,
    role TEXT NOT NULL,
    action TEXT NOT NULL,
    next TEXT NOT NULL,
    created_at TEXT NOT NULL,
    done_at TEXT
);

CREATE INDEX owner_actions_inbox_item ON owner_actions (inbox_item);
CREATE INDEX owner_actions_task ON owner_actions (task);

DROP TRIGGER task_states_add;

CREATE TABLE tasks_new (
    id INTEGER PRIMARY KEY,
    repository TEXT NOT NULL,
    issue INTEGER NOT NULL,
    workstream INTEGER NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('dispatched', 'queued', 'working', 'reviewed', 'needs_human', 'ready_for_review', 'stopped', 'ended', 'checks', 'approval', 'asked', 'answered', 'owner_action')),
    dispatched_at TEXT NOT NULL,
    branch TEXT,
    queued_at TEXT,
    fix_rounds INTEGER NOT NULL DEFAULT 0,
    pull_request INTEGER,
    judged_at TEXT,
    worker_restarts INTEGER NOT NULL DEFAULT 0,
    worker TEXT,
    worker_input TEXT,
    review_rounds INTEGER NOT NULL DEFAULT 0,
    review_comment INTEGER,
    check_head TEXT,
    approved_review TEXT,
    refused_review TEXT,
    needs_human_at TEXT,
    conflict_head TEXT,
    ci_failed_head TEXT,
    state_at TEXT NOT NULL DEFAULT '',
    long_wait_at TEXT,
    check_errors INTEGER NOT NULL DEFAULT 0,
    lead_findings TEXT NOT NULL DEFAULT '',
    recoveries INTEGER NOT NULL DEFAULT 0,
    break_wait_head TEXT,
    rerun_head TEXT,
    rerun_runs TEXT,
    workstream_repository TEXT NOT NULL DEFAULT '',
    branch_checked INTEGER NOT NULL DEFAULT 0,
    auto_merge INTEGER NOT NULL DEFAULT 0,
    approved_head TEXT,
    report TEXT NOT NULL DEFAULT '',
    approved_report TEXT,
    approved_again_head TEXT,
    approved_again_comment TEXT,
    approved_report_head TEXT
);

INSERT INTO tasks_new (id, repository, issue, workstream, state, dispatched_at, branch, queued_at, fix_rounds, pull_request, judged_at, worker_restarts, worker, worker_input, review_rounds, review_comment, check_head, approved_review, refused_review, needs_human_at, conflict_head, ci_failed_head, state_at, long_wait_at, check_errors, lead_findings, recoveries, break_wait_head, rerun_head, rerun_runs, workstream_repository, branch_checked, auto_merge, approved_head, report, approved_report, approved_again_head, approved_again_comment, approved_report_head)
SELECT id, repository, issue, workstream, state, dispatched_at, branch, queued_at, fix_rounds, pull_request, judged_at, worker_restarts, worker, worker_input, review_rounds, review_comment, check_head, approved_review, refused_review, needs_human_at, conflict_head, ci_failed_head, state_at, long_wait_at, check_errors, lead_findings, recoveries, break_wait_head, rerun_head, rerun_runs, workstream_repository, branch_checked, auto_merge, approved_head, report, approved_report, approved_again_head, approved_again_comment, approved_report_head FROM tasks;

DROP TABLE tasks;

ALTER TABLE tasks_new RENAME TO tasks;

CREATE INDEX tasks_issue ON tasks (repository, issue);

CREATE TRIGGER task_states_add AFTER UPDATE OF state_at ON tasks
WHEN OLD.state <> 'ended'
BEGIN
    INSERT INTO task_states (task, state, issue, workstream, organization, repository, session, role, harness, model, effort, started_at, ended_at)
    SELECT NEW.id, OLD.state, NEW.issue, NEW.workstream, substr(NEW.repository, 1, instr(NEW.repository, '/') - 1), NEW.repository,
        s.id, s.role, s.harness, s.model, s.effort, OLD.state_at, NEW.state_at
    FROM (SELECT 1)
    LEFT JOIN sessions s ON s.id = (
        SELECT id FROM sessions
        WHERE organization = substr(NEW.repository, 1, instr(NEW.repository, '/') - 1) AND repository = NEW.repository
            AND workstream = NEW.workstream AND issue = NEW.issue AND role = 'implementer'
        ORDER BY id DESC LIMIT 1
    );
END;
