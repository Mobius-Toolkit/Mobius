CREATE TABLE task_states (
    id INTEGER PRIMARY KEY,
    task INTEGER NOT NULL,
    state TEXT NOT NULL,
    issue INTEGER NOT NULL,
    workstream INTEGER NOT NULL,
    organization TEXT NOT NULL,
    repository TEXT NOT NULL,
    session INTEGER REFERENCES sessions (id),
    role TEXT,
    harness TEXT,
    model TEXT,
    effort TEXT,
    started_at TEXT NOT NULL,
    ended_at TEXT NOT NULL
);

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
