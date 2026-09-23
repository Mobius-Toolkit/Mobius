CREATE TABLE organizations (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE repositories (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id),
    owner TEXT NOT NULL,
    name TEXT NOT NULL,
    provider TEXT NOT NULL,
    default_branch TEXT NOT NULL DEFAULT 'main',
    local_path TEXT,
    created_at TEXT NOT NULL,
    UNIQUE (owner, name)
);

CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    scope TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (repository_id, slug)
);

CREATE TABLE harnesses (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    command TEXT NOT NULL,
    args TEXT NOT NULL DEFAULT '[]',
    env TEXT NOT NULL DEFAULT '{}',
    default_permission_policy TEXT NOT NULL,
    model_arg_template TEXT NOT NULL DEFAULT '[]',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
);

CREATE TABLE model_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    harness_id TEXT NOT NULL REFERENCES harnesses(id),
    model TEXT,
    effort TEXT,
    config TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);

CREATE TABLE agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL,
    profiles TEXT NOT NULL,
    project_id TEXT REFERENCES projects(id),
    repository_id TEXT REFERENCES repositories(id),
    instructions TEXT NOT NULL DEFAULT '',
    permission_policy TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE tasks (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    agent_id TEXT NOT NULL REFERENCES agents(id),
    parent_task_id TEXT REFERENCES tasks(id),
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    origin TEXT NOT NULL DEFAULT '{}',
    priority TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id),
    agent_id TEXT NOT NULL REFERENCES agents(id),
    activity TEXT NOT NULL,
    model_profile_id TEXT NOT NULL,
    harness_id TEXT NOT NULL,
    worktree_path TEXT,
    branch TEXT,
    acp_session_id TEXT,
    status TEXT NOT NULL,
    summary TEXT,
    started_at TEXT NOT NULL,
    finished_at TEXT
);

CREATE TABLE signals (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    kind TEXT NOT NULL,
    repository_id TEXT,
    project_id TEXT,
    dedupe_key TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL DEFAULT 'null',
    occurred_at TEXT NOT NULL,
    ingested_at TEXT NOT NULL
);

CREATE TABLE memory_entries (
    id TEXT PRIMARY KEY,
    scope TEXT NOT NULL,
    kind TEXT NOT NULL,
    content TEXT NOT NULL,
    source_run_id TEXT,
    superseded_by TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL REFERENCES agents(id),
    activity TEXT NOT NULL,
    model_profile_id TEXT NOT NULL,
    repository_id TEXT,
    workdir TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    acp_session_id TEXT,
    status TEXT NOT NULL,
    config_options TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    author TEXT NOT NULL,
    blocks TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL
);

CREATE TABLE permission_requests (
    id TEXT PRIMARY KEY,
    conversation_id TEXT,
    run_id TEXT,
    tool_call TEXT NOT NULL,
    options TEXT NOT NULL DEFAULT '[]',
    status TEXT NOT NULL,
    resolved_option_id TEXT,
    created_at TEXT NOT NULL,
    resolved_at TEXT
);
