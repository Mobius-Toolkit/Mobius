-- no-transaction
-- M1: org-scoped projects, research entity, conversation kinds.
--
-- `projects` and `agents` are rebuilt (SQLite cannot drop the NOT NULL
-- `projects.repository_id` column in place). The rebuild follows the
-- classic recipe: foreign_keys OFF outside a transaction, explicit
-- transaction, foreign_key_check before COMMIT. `foreign_keys=OFF` also
-- keeps the RENAMEs from rewriting child-table FK references.
PRAGMA foreign_keys = OFF;
BEGIN;

-- tasks gain the repository they execute against; backfilled from the old
-- project's repository BEFORE the projects table is rebuilt.
ALTER TABLE tasks ADD COLUMN repository_id TEXT REFERENCES repositories(id);
UPDATE tasks SET repository_id = (
    SELECT p.repository_id FROM projects p WHERE p.id = tasks.project_id
);
-- kind remap: research is its own entity now; implement -> feature.
UPDATE tasks SET kind = 'feature' WHERE kind = 'implement';
UPDATE tasks SET kind = 'triage' WHERE kind = 'research';

CREATE TABLE projects_new (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    repository_ids TEXT NOT NULL DEFAULT '[]',
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    scope TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (organization_id, slug)
);

INSERT INTO projects_new
    (id, organization_id, repository_ids, name, slug, description, scope,
     status, created_at)
SELECT
    p.id,
    COALESCE(r.organization_id, (SELECT id FROM organizations ORDER BY created_at LIMIT 1)),
    CASE WHEN p.repository_id IS NULL THEN '[]' ELSE json_array(p.repository_id) END,
    p.name, p.slug, p.description, p.scope, p.status, p.created_at
FROM projects p LEFT JOIN repositories r ON r.id = p.repository_id;

-- agents gain required organization ownership, backfilled
-- project -> repo -> first org. Reads the OLD projects table
-- (still has repository_id) so it must run before the swap below.
CREATE TABLE agents_new (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    profiles TEXT NOT NULL,
    project_id TEXT REFERENCES projects(id),
    repository_id TEXT REFERENCES repositories(id),
    instructions TEXT NOT NULL DEFAULT '',
    permission_policy TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL
);

INSERT INTO agents_new
    (id, name, role, organization_id, profiles, project_id, repository_id,
     instructions, permission_policy, status, created_at)
SELECT
    a.id, a.name, a.role,
    COALESCE(
        (SELECT r.organization_id
           FROM projects p JOIN repositories r ON r.id = p.repository_id
          WHERE p.id = a.project_id),
        (SELECT r.organization_id FROM repositories r WHERE r.id = a.repository_id),
        (SELECT id FROM organizations ORDER BY created_at LIMIT 1)
    ),
    a.profiles, a.project_id, a.repository_id, a.instructions,
    a.permission_policy, a.status, a.created_at
FROM agents a;

-- Agents with no attributable org can only exist in zero-org databases
-- (no projects or repositories either); drop them.
DELETE FROM agents_new WHERE organization_id IS NULL;

DROP TABLE agents;
ALTER TABLE agents_new RENAME TO agents;
DROP TABLE projects;
ALTER TABLE projects_new RENAME TO projects;

ALTER TABLE runs ADD COLUMN repository_id TEXT REFERENCES repositories(id);
ALTER TABLE runs ADD COLUMN conversation_id TEXT REFERENCES conversations(id);
UPDATE runs SET repository_id = (
    SELECT t.repository_id FROM tasks t WHERE t.id = runs.task_id
);

CREATE TABLE research (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    project_id TEXT REFERENCES projects(id),
    repository_ids TEXT NOT NULL DEFAULT '[]',
    question TEXT NOT NULL,
    status TEXT NOT NULL,
    findings TEXT,
    conversation_id TEXT REFERENCES conversations(id),
    origin_conversation_id TEXT,
    model_profile_id TEXT,
    created_at TEXT NOT NULL,
    finished_at TEXT
);

ALTER TABLE conversations ADD COLUMN organization_id TEXT;
ALTER TABLE conversations ADD COLUMN project_id TEXT REFERENCES projects(id);
ALTER TABLE conversations ADD COLUMN kind TEXT NOT NULL DEFAULT 'chat';
ALTER TABLE conversations ADD COLUMN run_id TEXT REFERENCES runs(id);
ALTER TABLE conversations ADD COLUMN research_id TEXT REFERENCES research(id);
UPDATE conversations SET
    organization_id = COALESCE(
        (SELECT organization_id FROM agents WHERE agents.id = conversations.agent_id),
        (SELECT id FROM organizations ORDER BY created_at LIMIT 1)
    ),
    project_id = (SELECT project_id FROM agents WHERE agents.id = conversations.agent_id);

ALTER TABLE memory_entries ADD COLUMN source_conversation_id TEXT;

PRAGMA foreign_key_check;
COMMIT;
PRAGMA foreign_keys = ON;
