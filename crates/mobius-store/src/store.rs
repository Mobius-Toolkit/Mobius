use mobius_core::*;
use sqlx::Row;
use sqlx::sqlite::{SqliteConnectOptions, SqlitePool};
use std::path::Path;
use std::str::FromStr;

fn db_err(e: sqlx::Error) -> StoreError {
    match &e {
        sqlx::Error::Database(dbe) if dbe.is_unique_violation() => {
            StoreError::Conflict(dbe.message().to_string())
        }
        sqlx::Error::RowNotFound => StoreError::NotFound(e.to_string()),
        _ => StoreError::Database(e.to_string()),
    }
}

fn ser_err(e: impl std::fmt::Display) -> StoreError {
    StoreError::Serialization(e.to_string())
}

fn ts(s: &str) -> StoreResult<chrono::DateTime<chrono::Utc>> {
    chrono::DateTime::parse_from_rfc3339(s)
        .map(|d| d.with_timezone(&chrono::Utc))
        .map_err(ser_err)
}

fn opt_ts(s: Option<&str>) -> StoreResult<Option<chrono::DateTime<chrono::Utc>>> {
    s.map(ts).transpose()
}

fn fmt_ts(t: &chrono::DateTime<chrono::Utc>) -> String {
    t.to_rfc3339()
}

fn parse_id<T: FromStr>(s: &str) -> StoreResult<T>
where
    T::Err: std::fmt::Display,
{
    s.parse().map_err(ser_err)
}

fn opt_id<T: FromStr>(s: Option<&str>) -> StoreResult<Option<T>>
where
    T::Err: std::fmt::Display,
{
    s.map(parse_id).transpose()
}

fn en<T: FromStr>(s: &str) -> StoreResult<T>
where
    T::Err: std::fmt::Display,
{
    T::from_str(s).map_err(ser_err)
}

fn json_in<T: serde::Serialize>(v: &T) -> StoreResult<String> {
    serde_json::to_string(v).map_err(ser_err)
}

fn json_out<T: serde::de::DeserializeOwned>(s: &str) -> StoreResult<T> {
    serde_json::from_str(s).map_err(ser_err)
}

#[derive(Clone)]
pub struct SqliteStore {
    pool: SqlitePool,
}

impl SqliteStore {
    /// Open (creating if needed) the SQLite database at `path` and run
    /// embedded migrations.
    pub async fn open(path: impl AsRef<Path>) -> Result<Self, StoreError> {
        let url = format!("sqlite://{}", path.as_ref().display());
        let options = SqliteConnectOptions::from_str(&url)
            .map_err(db_err)?
            .create_if_missing(true);
        let pool = SqlitePool::connect_with(options).await.map_err(db_err)?;
        sqlx::migrate!("./migrations")
            .run(&pool)
            .await
            .map_err(|e| StoreError::Database(e.to_string()))?;
        Ok(Self { pool })
    }

    /// In-memory database, mostly for tests.
    pub async fn open_memory() -> Result<Self, StoreError> {
        let pool = SqlitePool::connect("sqlite::memory:")
            .await
            .map_err(db_err)?;
        sqlx::migrate!("./migrations")
            .run(&pool)
            .await
            .map_err(|e| StoreError::Database(e.to_string()))?;
        Ok(Self { pool })
    }

    pub fn pool(&self) -> &SqlitePool {
        &self.pool
    }
}

fn row_to_organization(row: &sqlx::sqlite::SqliteRow) -> StoreResult<Organization> {
    Ok(Organization {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        name: row.get("name"),
        slug: row.get("slug"),
        created_at: ts(row.get::<String, _>("created_at").as_str())?,
    })
}

fn row_to_repository(row: &sqlx::sqlite::SqliteRow) -> StoreResult<Repository> {
    Ok(Repository {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        organization_id: parse_id(row.get::<String, _>("organization_id").as_str())?,
        owner: row.get("owner"),
        name: row.get("name"),
        provider: en(row.get::<String, _>("provider").as_str())?,
        default_branch: row.get("default_branch"),
        local_path: row
            .get::<Option<String>, _>("local_path")
            .map(std::path::PathBuf::from),
        created_at: ts(row.get::<String, _>("created_at").as_str())?,
    })
}

fn row_to_project(row: &sqlx::sqlite::SqliteRow) -> StoreResult<Project> {
    Ok(Project {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        organization_id: parse_id(row.get::<String, _>("organization_id").as_str())?,
        repository_ids: json_out(row.get::<String, _>("repository_ids").as_str())?,
        name: row.get("name"),
        slug: row.get("slug"),
        description: row.get("description"),
        scope: json_out(row.get::<String, _>("scope").as_str())?,
        status: en(row.get::<String, _>("status").as_str())?,
        created_at: ts(row.get::<String, _>("created_at").as_str())?,
    })
}

fn row_to_harness(row: &sqlx::sqlite::SqliteRow) -> StoreResult<Harness> {
    Ok(Harness {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        name: row.get("name"),
        command: row.get("command"),
        args: json_out(row.get::<String, _>("args").as_str())?,
        env: json_out(row.get::<String, _>("env").as_str())?,
        default_permission_policy: en(row.get::<String, _>("default_permission_policy").as_str())?,
        model_arg_template: json_out(row.get::<String, _>("model_arg_template").as_str())?,
        enabled: row.get::<bool, _>("enabled"),
        created_at: ts(row.get::<String, _>("created_at").as_str())?,
    })
}

fn row_to_model_profile(row: &sqlx::sqlite::SqliteRow) -> StoreResult<ModelProfile> {
    Ok(ModelProfile {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        name: row.get("name"),
        harness_id: parse_id(row.get::<String, _>("harness_id").as_str())?,
        model: row.get("model"),
        effort: row
            .get::<Option<String>, _>("effort")
            .as_deref()
            .map(en)
            .transpose()?,
        config: json_out(row.get::<String, _>("config").as_str())?,
        created_at: ts(row.get::<String, _>("created_at").as_str())?,
    })
}

fn row_to_agent(row: &sqlx::sqlite::SqliteRow) -> StoreResult<Agent> {
    Ok(Agent {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        name: row.get("name"),
        role: en(row.get::<String, _>("role").as_str())?,
        organization_id: opt_id(row.get::<Option<String>, _>("organization_id").as_deref())?
            .ok_or_else(|| StoreError::Serialization("agent with NULL organization_id".into()))?,
        profiles: json_out(row.get::<String, _>("profiles").as_str())?,
        project_id: opt_id(row.get::<Option<String>, _>("project_id").as_deref())?,
        repository_id: opt_id(row.get::<Option<String>, _>("repository_id").as_deref())?,
        instructions: row.get("instructions"),
        permission_policy: en(row.get::<String, _>("permission_policy").as_str())?,
        status: en(row.get::<String, _>("status").as_str())?,
        created_at: ts(row.get::<String, _>("created_at").as_str())?,
    })
}

fn row_to_task(row: &sqlx::sqlite::SqliteRow) -> StoreResult<Task> {
    Ok(Task {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        project_id: parse_id(row.get::<String, _>("project_id").as_str())?,
        agent_id: parse_id(row.get::<String, _>("agent_id").as_str())?,
        parent_task_id: opt_id(row.get::<Option<String>, _>("parent_task_id").as_deref())?,
        title: row.get("title"),
        description: row.get("description"),
        repository_id: opt_id(row.get::<Option<String>, _>("repository_id").as_deref())?,
        kind: en(row.get::<String, _>("kind").as_str())?,
        status: en(row.get::<String, _>("status").as_str())?,
        origin: json_out(row.get::<String, _>("origin").as_str())?,
        priority: en(row.get::<String, _>("priority").as_str())?,
        created_at: ts(row.get::<String, _>("created_at").as_str())?,
        updated_at: ts(row.get::<String, _>("updated_at").as_str())?,
    })
}

fn row_to_run(row: &sqlx::sqlite::SqliteRow) -> StoreResult<Run> {
    Ok(Run {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        task_id: parse_id(row.get::<String, _>("task_id").as_str())?,
        agent_id: parse_id(row.get::<String, _>("agent_id").as_str())?,
        activity: en(row.get::<String, _>("activity").as_str())?,
        model_profile_id: parse_id(row.get::<String, _>("model_profile_id").as_str())?,
        harness_id: parse_id(row.get::<String, _>("harness_id").as_str())?,
        repository_id: opt_id(row.get::<Option<String>, _>("repository_id").as_deref())?,
        conversation_id: opt_id(row.get::<Option<String>, _>("conversation_id").as_deref())?,
        worktree_path: row
            .get::<Option<String>, _>("worktree_path")
            .map(std::path::PathBuf::from),
        branch: row.get("branch"),
        acp_session_id: row.get("acp_session_id"),
        status: en(row.get::<String, _>("status").as_str())?,
        summary: row.get("summary"),
        started_at: ts(row.get::<String, _>("started_at").as_str())?,
        finished_at: opt_ts(row.get::<Option<String>, _>("finished_at").as_deref())?,
    })
}

fn row_to_signal(row: &sqlx::sqlite::SqliteRow) -> StoreResult<Signal> {
    Ok(Signal {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        source: en(row.get::<String, _>("source").as_str())?,
        kind: json_out(row.get::<String, _>("kind").as_str())?,
        repository_id: opt_id(row.get::<Option<String>, _>("repository_id").as_deref())?,
        project_id: opt_id(row.get::<Option<String>, _>("project_id").as_deref())?,
        dedupe_key: row.get("dedupe_key"),
        title: row.get("title"),
        body: row.get("body"),
        payload: json_out(row.get::<String, _>("payload").as_str())?,
        occurred_at: ts(row.get::<String, _>("occurred_at").as_str())?,
        ingested_at: ts(row.get::<String, _>("ingested_at").as_str())?,
    })
}

fn row_to_memory_entry(row: &sqlx::sqlite::SqliteRow) -> StoreResult<MemoryEntry> {
    Ok(MemoryEntry {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        scope: json_out(row.get::<String, _>("scope").as_str())?,
        kind: en(row.get::<String, _>("kind").as_str())?,
        content: row.get("content"),
        source_run_id: opt_id(row.get::<Option<String>, _>("source_run_id").as_deref())?,
        source_conversation_id: opt_id(
            row.get::<Option<String>, _>("source_conversation_id")
                .as_deref(),
        )?,
        superseded_by: opt_id(row.get::<Option<String>, _>("superseded_by").as_deref())?,
        created_at: ts(row.get::<String, _>("created_at").as_str())?,
    })
}

fn row_to_conversation(row: &sqlx::sqlite::SqliteRow) -> StoreResult<Conversation> {
    Ok(Conversation {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        agent_id: parse_id(row.get::<String, _>("agent_id").as_str())?,
        activity: en(row.get::<String, _>("activity").as_str())?,
        model_profile_id: parse_id(row.get::<String, _>("model_profile_id").as_str())?,
        organization_id: opt_id(row.get::<Option<String>, _>("organization_id").as_deref())?
            .ok_or_else(|| {
                StoreError::Serialization("conversation with NULL organization_id".into())
            })?,
        project_id: opt_id(row.get::<Option<String>, _>("project_id").as_deref())?,
        kind: en(row.get::<String, _>("kind").as_str())?,
        run_id: opt_id(row.get::<Option<String>, _>("run_id").as_deref())?,
        research_id: opt_id(row.get::<Option<String>, _>("research_id").as_deref())?,
        repository_id: opt_id(row.get::<Option<String>, _>("repository_id").as_deref())?,
        workdir: std::path::PathBuf::from(row.get::<String, _>("workdir")),
        title: row.get("title"),
        acp_session_id: row.get("acp_session_id"),
        status: en(row.get::<String, _>("status").as_str())?,
        config_options: json_out(row.get::<String, _>("config_options").as_str())?,
        created_at: ts(row.get::<String, _>("created_at").as_str())?,
        updated_at: ts(row.get::<String, _>("updated_at").as_str())?,
    })
}

fn row_to_message(row: &sqlx::sqlite::SqliteRow) -> StoreResult<Message> {
    Ok(Message {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        conversation_id: parse_id(row.get::<String, _>("conversation_id").as_str())?,
        author: en(row.get::<String, _>("author").as_str())?,
        blocks: json_out(row.get::<String, _>("blocks").as_str())?,
        created_at: ts(row.get::<String, _>("created_at").as_str())?,
    })
}

fn row_to_permission_request(row: &sqlx::sqlite::SqliteRow) -> StoreResult<PermissionRequest> {
    let status_s: String = row.get("status");
    let status = match status_s.as_str() {
        "pending" => PermissionRequestStatus::Pending,
        "resolved" => PermissionRequestStatus::Resolved {
            option_id: row
                .get::<Option<String>, _>("resolved_option_id")
                .unwrap_or_default(),
        },
        "expired" => PermissionRequestStatus::Expired,
        other => {
            return Err(StoreError::Serialization(format!(
                "unknown permission request status {other:?}"
            )));
        }
    };
    Ok(PermissionRequest {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        conversation_id: opt_id(row.get::<Option<String>, _>("conversation_id").as_deref())?,
        run_id: opt_id(row.get::<Option<String>, _>("run_id").as_deref())?,
        tool_call: json_out(row.get::<String, _>("tool_call").as_str())?,
        options: json_out(row.get::<String, _>("options").as_str())?,
        status,
        created_at: ts(row.get::<String, _>("created_at").as_str())?,
        resolved_at: opt_ts(row.get::<Option<String>, _>("resolved_at").as_deref())?,
    })
}

fn permission_status_parts(status: &PermissionRequestStatus) -> (&'static str, Option<String>) {
    match status {
        PermissionRequestStatus::Pending => ("pending", None),
        PermissionRequestStatus::Resolved { option_id } => ("resolved", Some(option_id.clone())),
        PermissionRequestStatus::Expired => ("expired", None),
    }
}

impl OrganizationRepo for SqliteStore {
    async fn get_organization(&self, id: OrganizationId) -> StoreResult<Option<Organization>> {
        let rows = sqlx::query("SELECT * FROM organizations WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_organization).transpose()
    }

    async fn find_organization_by_slug(&self, slug: &str) -> StoreResult<Option<Organization>> {
        let rows = sqlx::query("SELECT * FROM organizations WHERE slug = ?")
            .bind(slug)
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_organization).transpose()
    }

    async fn list_organizations(&self) -> StoreResult<Vec<Organization>> {
        let rows = sqlx::query("SELECT * FROM organizations ORDER BY created_at")
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_organization).collect()
    }

    async fn insert_organization(&self, org: &Organization) -> StoreResult<()> {
        sqlx::query("INSERT INTO organizations (id, name, slug, created_at) VALUES (?, ?, ?, ?)")
            .bind(org.id.to_string())
            .bind(&org.name)
            .bind(&org.slug)
            .bind(fmt_ts(&org.created_at))
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }

    async fn update_organization(&self, org: &Organization) -> StoreResult<()> {
        sqlx::query("UPDATE organizations SET name = ?, slug = ? WHERE id = ?")
            .bind(&org.name)
            .bind(&org.slug)
            .bind(org.id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }

    async fn delete_organization(&self, id: OrganizationId) -> StoreResult<()> {
        sqlx::query("DELETE FROM organizations WHERE id = ?")
            .bind(id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }
}

impl RepositoryRepo for SqliteStore {
    async fn get_repository(&self, id: RepositoryId) -> StoreResult<Option<Repository>> {
        let rows = sqlx::query("SELECT * FROM repositories WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_repository).transpose()
    }

    async fn find_repository_by_owner_name(
        &self,
        owner: &str,
        name: &str,
    ) -> StoreResult<Option<Repository>> {
        let rows = sqlx::query("SELECT * FROM repositories WHERE owner = ? AND name = ?")
            .bind(owner)
            .bind(name)
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_repository).transpose()
    }

    async fn list_repositories(&self) -> StoreResult<Vec<Repository>> {
        let rows = sqlx::query("SELECT * FROM repositories ORDER BY created_at")
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_repository).collect()
    }

    async fn insert_repository(&self, repo: &Repository) -> StoreResult<()> {
        sqlx::query(
            "INSERT INTO repositories (id, organization_id, owner, name, provider, \
             default_branch, local_path, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(repo.id.to_string())
        .bind(repo.organization_id.to_string())
        .bind(&repo.owner)
        .bind(&repo.name)
        .bind(repo.provider.to_string())
        .bind(&repo.default_branch)
        .bind(repo.local_path.as_ref().map(|p| p.display().to_string()))
        .bind(fmt_ts(&repo.created_at))
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn update_repository(&self, repo: &Repository) -> StoreResult<()> {
        sqlx::query(
            "UPDATE repositories SET organization_id = ?, owner = ?, name = ?, \
             provider = ?, default_branch = ?, local_path = ? WHERE id = ?",
        )
        .bind(repo.organization_id.to_string())
        .bind(&repo.owner)
        .bind(&repo.name)
        .bind(repo.provider.to_string())
        .bind(&repo.default_branch)
        .bind(repo.local_path.as_ref().map(|p| p.display().to_string()))
        .bind(repo.id.to_string())
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn delete_repository(&self, id: RepositoryId) -> StoreResult<()> {
        sqlx::query("DELETE FROM repositories WHERE id = ?")
            .bind(id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }
}

impl ProjectRepo for SqliteStore {
    async fn get_project(&self, id: ProjectId) -> StoreResult<Option<Project>> {
        let rows = sqlx::query("SELECT * FROM projects WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_project).transpose()
    }

    async fn find_project_by_slug(
        &self,
        organization_id: OrganizationId,
        slug: &str,
    ) -> StoreResult<Option<Project>> {
        let rows = sqlx::query("SELECT * FROM projects WHERE organization_id = ? AND slug = ?")
            .bind(organization_id.to_string())
            .bind(slug)
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_project).transpose()
    }

    async fn list_projects(&self) -> StoreResult<Vec<Project>> {
        let rows = sqlx::query("SELECT * FROM projects ORDER BY created_at")
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_project).collect()
    }

    async fn list_projects_by_organization(
        &self,
        organization_id: OrganizationId,
    ) -> StoreResult<Vec<Project>> {
        let rows =
            sqlx::query("SELECT * FROM projects WHERE organization_id = ? ORDER BY created_at")
                .bind(organization_id.to_string())
                .fetch_all(&self.pool)
                .await
                .map_err(db_err)?;
        rows.iter().map(row_to_project).collect()
    }

    async fn list_projects_by_repository(
        &self,
        repository_id: RepositoryId,
    ) -> StoreResult<Vec<Project>> {
        // repository_ids is a JSON array of quoted UUIDs; match on the
        // quoted id so a LIKE can never produce a false positive.
        let pattern = format!("%\"{repository_id}\"%");
        let rows =
            sqlx::query("SELECT * FROM projects WHERE repository_ids LIKE ? ORDER BY created_at")
                .bind(pattern)
                .fetch_all(&self.pool)
                .await
                .map_err(db_err)?;
        rows.iter().map(row_to_project).collect()
    }

    async fn insert_project(&self, project: &Project) -> StoreResult<()> {
        sqlx::query(
            "INSERT INTO projects (id, organization_id, repository_ids, name, slug, \
             description, scope, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(project.id.to_string())
        .bind(project.organization_id.to_string())
        .bind(json_in(&project.repository_ids)?)
        .bind(&project.name)
        .bind(&project.slug)
        .bind(&project.description)
        .bind(json_in(&project.scope)?)
        .bind(project.status.to_string())
        .bind(fmt_ts(&project.created_at))
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn update_project(&self, project: &Project) -> StoreResult<()> {
        sqlx::query(
            "UPDATE projects SET organization_id = ?, repository_ids = ?, name = ?, \
             slug = ?, description = ?, scope = ?, status = ? WHERE id = ?",
        )
        .bind(project.organization_id.to_string())
        .bind(json_in(&project.repository_ids)?)
        .bind(&project.name)
        .bind(&project.slug)
        .bind(&project.description)
        .bind(json_in(&project.scope)?)
        .bind(project.status.to_string())
        .bind(project.id.to_string())
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn delete_project(&self, id: ProjectId) -> StoreResult<()> {
        sqlx::query("DELETE FROM projects WHERE id = ?")
            .bind(id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }
}

impl HarnessRepo for SqliteStore {
    async fn get_harness(&self, id: HarnessId) -> StoreResult<Option<Harness>> {
        let rows = sqlx::query("SELECT * FROM harnesses WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_harness).transpose()
    }

    async fn find_harness_by_name(&self, name: &str) -> StoreResult<Option<Harness>> {
        let rows = sqlx::query("SELECT * FROM harnesses WHERE name = ?")
            .bind(name)
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_harness).transpose()
    }

    async fn list_harnesses(&self) -> StoreResult<Vec<Harness>> {
        let rows = sqlx::query("SELECT * FROM harnesses ORDER BY name")
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_harness).collect()
    }

    async fn insert_harness(&self, h: &Harness) -> StoreResult<()> {
        sqlx::query(
            "INSERT INTO harnesses (id, name, command, args, env, \
             default_permission_policy, model_arg_template, enabled, created_at) \
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(h.id.to_string())
        .bind(&h.name)
        .bind(&h.command)
        .bind(json_in(&h.args)?)
        .bind(json_in(&h.env)?)
        .bind(h.default_permission_policy.to_string())
        .bind(json_in(&h.model_arg_template)?)
        .bind(h.enabled)
        .bind(fmt_ts(&h.created_at))
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn update_harness(&self, h: &Harness) -> StoreResult<()> {
        sqlx::query(
            "UPDATE harnesses SET name = ?, command = ?, args = ?, env = ?, \
             default_permission_policy = ?, model_arg_template = ?, enabled = ? \
             WHERE id = ?",
        )
        .bind(&h.name)
        .bind(&h.command)
        .bind(json_in(&h.args)?)
        .bind(json_in(&h.env)?)
        .bind(h.default_permission_policy.to_string())
        .bind(json_in(&h.model_arg_template)?)
        .bind(h.enabled)
        .bind(h.id.to_string())
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn delete_harness(&self, id: HarnessId) -> StoreResult<()> {
        sqlx::query("DELETE FROM harnesses WHERE id = ?")
            .bind(id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }
}

impl ModelProfileRepo for SqliteStore {
    async fn get_model_profile(&self, id: ModelProfileId) -> StoreResult<Option<ModelProfile>> {
        let rows = sqlx::query("SELECT * FROM model_profiles WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_model_profile).transpose()
    }

    async fn find_model_profile_by_name(&self, name: &str) -> StoreResult<Option<ModelProfile>> {
        let rows = sqlx::query("SELECT * FROM model_profiles WHERE name = ?")
            .bind(name)
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_model_profile).transpose()
    }

    async fn list_model_profiles(&self) -> StoreResult<Vec<ModelProfile>> {
        let rows = sqlx::query("SELECT * FROM model_profiles ORDER BY name")
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_model_profile).collect()
    }

    async fn insert_model_profile(&self, p: &ModelProfile) -> StoreResult<()> {
        sqlx::query(
            "INSERT INTO model_profiles (id, name, harness_id, model, effort, \
             config, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(p.id.to_string())
        .bind(&p.name)
        .bind(p.harness_id.to_string())
        .bind(&p.model)
        .bind(p.effort.map(|e| e.to_string()))
        .bind(json_in(&p.config)?)
        .bind(fmt_ts(&p.created_at))
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn update_model_profile(&self, p: &ModelProfile) -> StoreResult<()> {
        sqlx::query(
            "UPDATE model_profiles SET name = ?, harness_id = ?, model = ?, \
             effort = ?, config = ? WHERE id = ?",
        )
        .bind(&p.name)
        .bind(p.harness_id.to_string())
        .bind(&p.model)
        .bind(p.effort.map(|e| e.to_string()))
        .bind(json_in(&p.config)?)
        .bind(p.id.to_string())
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn delete_model_profile(&self, id: ModelProfileId) -> StoreResult<()> {
        sqlx::query("DELETE FROM model_profiles WHERE id = ?")
            .bind(id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }
}

impl AgentRepo for SqliteStore {
    async fn get_agent(&self, id: AgentId) -> StoreResult<Option<Agent>> {
        let rows = sqlx::query("SELECT * FROM agents WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_agent).transpose()
    }

    async fn find_agent_by_name(&self, name: &str) -> StoreResult<Option<Agent>> {
        let rows = sqlx::query("SELECT * FROM agents WHERE name = ?")
            .bind(name)
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_agent).transpose()
    }

    async fn find_agent_by_role_and_org(
        &self,
        organization_id: OrganizationId,
        role: AgentRole,
    ) -> StoreResult<Option<Agent>> {
        let rows = sqlx::query(
            "SELECT * FROM agents WHERE organization_id = ? AND role = ? \
             AND project_id IS NULL ORDER BY name LIMIT 1",
        )
        .bind(organization_id.to_string())
        .bind(role.to_string())
        .fetch_all(&self.pool)
        .await
        .map_err(db_err)?;
        rows.first().map(row_to_agent).transpose()
    }

    async fn list_agents(&self) -> StoreResult<Vec<Agent>> {
        let rows = sqlx::query("SELECT * FROM agents ORDER BY name")
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_agent).collect()
    }

    async fn list_agents_by_project(&self, project_id: ProjectId) -> StoreResult<Vec<Agent>> {
        let rows = sqlx::query("SELECT * FROM agents WHERE project_id = ? ORDER BY name")
            .bind(project_id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_agent).collect()
    }

    async fn insert_agent(&self, a: &Agent) -> StoreResult<()> {
        sqlx::query(
            "INSERT INTO agents (id, name, role, organization_id, profiles, project_id, \
             repository_id, instructions, permission_policy, status, created_at) \
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(a.id.to_string())
        .bind(&a.name)
        .bind(a.role.to_string())
        .bind(a.organization_id.to_string())
        .bind(json_in(&a.profiles)?)
        .bind(a.project_id.map(|p| p.to_string()))
        .bind(a.repository_id.map(|r| r.to_string()))
        .bind(&a.instructions)
        .bind(a.permission_policy.to_string())
        .bind(a.status.to_string())
        .bind(fmt_ts(&a.created_at))
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn update_agent(&self, a: &Agent) -> StoreResult<()> {
        sqlx::query(
            "UPDATE agents SET name = ?, role = ?, organization_id = ?, profiles = ?, \
             project_id = ?, repository_id = ?, instructions = ?, permission_policy = ?, \
             status = ? WHERE id = ?",
        )
        .bind(&a.name)
        .bind(a.role.to_string())
        .bind(a.organization_id.to_string())
        .bind(json_in(&a.profiles)?)
        .bind(a.project_id.map(|p| p.to_string()))
        .bind(a.repository_id.map(|r| r.to_string()))
        .bind(&a.instructions)
        .bind(a.permission_policy.to_string())
        .bind(a.status.to_string())
        .bind(a.id.to_string())
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn delete_agent(&self, id: AgentId) -> StoreResult<()> {
        sqlx::query("DELETE FROM agents WHERE id = ?")
            .bind(id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }
}

impl TaskRepo for SqliteStore {
    async fn get_task(&self, id: TaskId) -> StoreResult<Option<Task>> {
        let rows = sqlx::query("SELECT * FROM tasks WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_task).transpose()
    }

    async fn list_tasks(&self) -> StoreResult<Vec<Task>> {
        let rows = sqlx::query("SELECT * FROM tasks ORDER BY created_at")
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_task).collect()
    }

    async fn list_tasks_by_project(&self, project_id: ProjectId) -> StoreResult<Vec<Task>> {
        let rows = sqlx::query("SELECT * FROM tasks WHERE project_id = ? ORDER BY created_at")
            .bind(project_id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_task).collect()
    }

    async fn list_tasks_by_parent(&self, parent_task_id: TaskId) -> StoreResult<Vec<Task>> {
        let rows = sqlx::query("SELECT * FROM tasks WHERE parent_task_id = ? ORDER BY created_at")
            .bind(parent_task_id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_task).collect()
    }

    async fn list_tasks_by_origin_conversation(
        &self,
        conversation_id: ConversationId,
    ) -> StoreResult<Vec<Task>> {
        // origin is a JSON blob; the table is small so filter in Rust.
        let tasks = self.list_tasks().await?;
        Ok(tasks
            .into_iter()
            .filter(|t| t.origin.conversation_id == Some(conversation_id))
            .collect())
    }

    async fn insert_task(&self, t: &Task) -> StoreResult<()> {
        sqlx::query(
            "INSERT INTO tasks (id, project_id, agent_id, parent_task_id, title, \
             description, repository_id, kind, status, origin, priority, \
             created_at, updated_at) \
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(t.id.to_string())
        .bind(t.project_id.to_string())
        .bind(t.agent_id.to_string())
        .bind(t.parent_task_id.map(|p| p.to_string()))
        .bind(&t.title)
        .bind(&t.description)
        .bind(t.repository_id.map(|r| r.to_string()))
        .bind(t.kind.to_string())
        .bind(t.status.to_string())
        .bind(json_in(&t.origin)?)
        .bind(t.priority.to_string())
        .bind(fmt_ts(&t.created_at))
        .bind(fmt_ts(&t.updated_at))
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn update_task(&self, t: &Task) -> StoreResult<()> {
        sqlx::query(
            "UPDATE tasks SET project_id = ?, agent_id = ?, parent_task_id = ?, \
             title = ?, description = ?, repository_id = ?, kind = ?, status = ?, \
             origin = ?, priority = ?, updated_at = ? WHERE id = ?",
        )
        .bind(t.project_id.to_string())
        .bind(t.agent_id.to_string())
        .bind(t.parent_task_id.map(|p| p.to_string()))
        .bind(&t.title)
        .bind(&t.description)
        .bind(t.repository_id.map(|r| r.to_string()))
        .bind(t.kind.to_string())
        .bind(t.status.to_string())
        .bind(json_in(&t.origin)?)
        .bind(t.priority.to_string())
        .bind(fmt_ts(&t.updated_at))
        .bind(t.id.to_string())
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn delete_task(&self, id: TaskId) -> StoreResult<()> {
        sqlx::query("DELETE FROM tasks WHERE id = ?")
            .bind(id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }
}

impl RunRepo for SqliteStore {
    async fn get_run(&self, id: RunId) -> StoreResult<Option<Run>> {
        let rows = sqlx::query("SELECT * FROM runs WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_run).transpose()
    }

    async fn list_runs(&self) -> StoreResult<Vec<Run>> {
        let rows = sqlx::query("SELECT * FROM runs ORDER BY started_at")
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_run).collect()
    }

    async fn list_runs_by_task(&self, task_id: TaskId) -> StoreResult<Vec<Run>> {
        let rows = sqlx::query("SELECT * FROM runs WHERE task_id = ? ORDER BY started_at")
            .bind(task_id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_run).collect()
    }

    async fn insert_run(&self, r: &Run) -> StoreResult<()> {
        sqlx::query(
            "INSERT INTO runs (id, task_id, agent_id, activity, model_profile_id, \
             harness_id, repository_id, conversation_id, worktree_path, branch, \
             acp_session_id, status, summary, started_at, finished_at) \
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(r.id.to_string())
        .bind(r.task_id.to_string())
        .bind(r.agent_id.to_string())
        .bind(r.activity.to_string())
        .bind(r.model_profile_id.to_string())
        .bind(r.harness_id.to_string())
        .bind(r.repository_id.map(|r| r.to_string()))
        .bind(r.conversation_id.map(|c| c.to_string()))
        .bind(r.worktree_path.as_ref().map(|p| p.display().to_string()))
        .bind(&r.branch)
        .bind(&r.acp_session_id)
        .bind(r.status.to_string())
        .bind(&r.summary)
        .bind(fmt_ts(&r.started_at))
        .bind(r.finished_at.as_ref().map(fmt_ts))
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn update_run(&self, r: &Run) -> StoreResult<()> {
        sqlx::query(
            "UPDATE runs SET task_id = ?, agent_id = ?, activity = ?, \
             model_profile_id = ?, harness_id = ?, repository_id = ?, \
             conversation_id = ?, worktree_path = ?, branch = ?, \
             acp_session_id = ?, status = ?, summary = ?, finished_at = ? \
             WHERE id = ?",
        )
        .bind(r.task_id.to_string())
        .bind(r.agent_id.to_string())
        .bind(r.activity.to_string())
        .bind(r.model_profile_id.to_string())
        .bind(r.harness_id.to_string())
        .bind(r.repository_id.map(|r| r.to_string()))
        .bind(r.conversation_id.map(|c| c.to_string()))
        .bind(r.worktree_path.as_ref().map(|p| p.display().to_string()))
        .bind(&r.branch)
        .bind(&r.acp_session_id)
        .bind(r.status.to_string())
        .bind(&r.summary)
        .bind(r.finished_at.as_ref().map(fmt_ts))
        .bind(r.id.to_string())
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn delete_run(&self, id: RunId) -> StoreResult<()> {
        sqlx::query("DELETE FROM runs WHERE id = ?")
            .bind(id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }
}

impl SignalRepo for SqliteStore {
    async fn get_signal(&self, id: SignalId) -> StoreResult<Option<Signal>> {
        let rows = sqlx::query("SELECT * FROM signals WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_signal).transpose()
    }

    async fn find_signal_by_dedupe_key(&self, dedupe_key: &str) -> StoreResult<Option<Signal>> {
        let rows = sqlx::query("SELECT * FROM signals WHERE dedupe_key = ?")
            .bind(dedupe_key)
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_signal).transpose()
    }

    async fn list_signals(&self) -> StoreResult<Vec<Signal>> {
        let rows = sqlx::query("SELECT * FROM signals ORDER BY ingested_at")
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_signal).collect()
    }

    async fn insert_signal(&self, s: &Signal) -> StoreResult<()> {
        sqlx::query(
            "INSERT INTO signals (id, source, kind, repository_id, project_id, \
             dedupe_key, title, body, payload, occurred_at, ingested_at) \
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(s.id.to_string())
        .bind(s.source.to_string())
        .bind(json_in(&s.kind)?)
        .bind(s.repository_id.map(|r| r.to_string()))
        .bind(s.project_id.map(|p| p.to_string()))
        .bind(&s.dedupe_key)
        .bind(&s.title)
        .bind(&s.body)
        .bind(json_in(&s.payload)?)
        .bind(fmt_ts(&s.occurred_at))
        .bind(fmt_ts(&s.ingested_at))
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn delete_signal(&self, id: SignalId) -> StoreResult<()> {
        sqlx::query("DELETE FROM signals WHERE id = ?")
            .bind(id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }
}

impl MemoryRepo for SqliteStore {
    async fn get_memory_entry(&self, id: MemoryEntryId) -> StoreResult<Option<MemoryEntry>> {
        let rows = sqlx::query("SELECT * FROM memory_entries WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_memory_entry).transpose()
    }

    async fn list_memory_entries(&self) -> StoreResult<Vec<MemoryEntry>> {
        let rows = sqlx::query("SELECT * FROM memory_entries ORDER BY created_at")
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_memory_entry).collect()
    }

    async fn list_memory_by_scope(&self, scope: &MemoryScope) -> StoreResult<Vec<MemoryEntry>> {
        let scope_json = json_in(scope)?;
        let rows = sqlx::query("SELECT * FROM memory_entries WHERE scope = ? ORDER BY created_at")
            .bind(scope_json)
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_memory_entry).collect()
    }

    async fn insert_memory_entry(&self, e: &MemoryEntry) -> StoreResult<()> {
        sqlx::query(
            "INSERT INTO memory_entries (id, scope, kind, content, source_run_id, \
             source_conversation_id, superseded_by, created_at) \
             VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(e.id.to_string())
        .bind(json_in(&e.scope)?)
        .bind(e.kind.to_string())
        .bind(&e.content)
        .bind(e.source_run_id.map(|r| r.to_string()))
        .bind(e.source_conversation_id.map(|c| c.to_string()))
        .bind(e.superseded_by.map(|s| s.to_string()))
        .bind(fmt_ts(&e.created_at))
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn update_memory_entry(&self, e: &MemoryEntry) -> StoreResult<()> {
        sqlx::query(
            "UPDATE memory_entries SET scope = ?, kind = ?, content = ?, \
             source_run_id = ?, source_conversation_id = ?, superseded_by = ? \
             WHERE id = ?",
        )
        .bind(json_in(&e.scope)?)
        .bind(e.kind.to_string())
        .bind(&e.content)
        .bind(e.source_run_id.map(|r| r.to_string()))
        .bind(e.source_conversation_id.map(|c| c.to_string()))
        .bind(e.superseded_by.map(|s| s.to_string()))
        .bind(e.id.to_string())
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn delete_memory_entry(&self, id: MemoryEntryId) -> StoreResult<()> {
        sqlx::query("DELETE FROM memory_entries WHERE id = ?")
            .bind(id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }
}

impl ConversationRepo for SqliteStore {
    async fn get_conversation(&self, id: ConversationId) -> StoreResult<Option<Conversation>> {
        let rows = sqlx::query("SELECT * FROM conversations WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_conversation).transpose()
    }

    async fn list_conversations(&self) -> StoreResult<Vec<Conversation>> {
        let rows = sqlx::query("SELECT * FROM conversations ORDER BY created_at DESC")
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_conversation).collect()
    }

    async fn list_conversations_by_project(
        &self,
        project_id: ProjectId,
    ) -> StoreResult<Vec<Conversation>> {
        let rows = sqlx::query(
            "SELECT * FROM conversations WHERE project_id = ? ORDER BY created_at DESC",
        )
        .bind(project_id.to_string())
        .fetch_all(&self.pool)
        .await
        .map_err(db_err)?;
        rows.iter().map(row_to_conversation).collect()
    }

    async fn insert_conversation(&self, c: &Conversation) -> StoreResult<()> {
        sqlx::query(
            "INSERT INTO conversations (id, agent_id, activity, model_profile_id, \
             organization_id, project_id, kind, run_id, research_id, \
             repository_id, workdir, title, acp_session_id, status, \
             config_options, created_at, updated_at) \
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(c.id.to_string())
        .bind(c.agent_id.to_string())
        .bind(c.activity.to_string())
        .bind(c.model_profile_id.to_string())
        .bind(c.organization_id.to_string())
        .bind(c.project_id.map(|p| p.to_string()))
        .bind(c.kind.to_string())
        .bind(c.run_id.map(|r| r.to_string()))
        .bind(c.research_id.map(|r| r.to_string()))
        .bind(c.repository_id.map(|r| r.to_string()))
        .bind(c.workdir.display().to_string())
        .bind(&c.title)
        .bind(&c.acp_session_id)
        .bind(c.status.to_string())
        .bind(json_in(&c.config_options)?)
        .bind(fmt_ts(&c.created_at))
        .bind(fmt_ts(&c.updated_at))
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn update_conversation(&self, c: &Conversation) -> StoreResult<()> {
        sqlx::query(
            "UPDATE conversations SET agent_id = ?, activity = ?, \
             model_profile_id = ?, organization_id = ?, project_id = ?, \
             kind = ?, run_id = ?, research_id = ?, repository_id = ?, \
             workdir = ?, title = ?, acp_session_id = ?, status = ?, \
             config_options = ?, updated_at = ? WHERE id = ?",
        )
        .bind(c.agent_id.to_string())
        .bind(c.activity.to_string())
        .bind(c.model_profile_id.to_string())
        .bind(c.organization_id.to_string())
        .bind(c.project_id.map(|p| p.to_string()))
        .bind(c.kind.to_string())
        .bind(c.run_id.map(|r| r.to_string()))
        .bind(c.research_id.map(|r| r.to_string()))
        .bind(c.repository_id.map(|r| r.to_string()))
        .bind(c.workdir.display().to_string())
        .bind(&c.title)
        .bind(&c.acp_session_id)
        .bind(c.status.to_string())
        .bind(json_in(&c.config_options)?)
        .bind(fmt_ts(&c.updated_at))
        .bind(c.id.to_string())
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn delete_conversation(&self, id: ConversationId) -> StoreResult<()> {
        sqlx::query("DELETE FROM conversations WHERE id = ?")
            .bind(id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }
}

impl MessageRepo for SqliteStore {
    async fn get_message(&self, id: MessageId) -> StoreResult<Option<Message>> {
        let rows = sqlx::query("SELECT * FROM messages WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_message).transpose()
    }

    async fn list_messages_by_conversation(
        &self,
        conversation_id: ConversationId,
    ) -> StoreResult<Vec<Message>> {
        let rows =
            sqlx::query("SELECT * FROM messages WHERE conversation_id = ? ORDER BY created_at")
                .bind(conversation_id.to_string())
                .fetch_all(&self.pool)
                .await
                .map_err(db_err)?;
        rows.iter().map(row_to_message).collect()
    }

    async fn insert_message(&self, m: &Message) -> StoreResult<()> {
        sqlx::query(
            "INSERT INTO messages (id, conversation_id, author, blocks, \
             created_at) VALUES (?, ?, ?, ?, ?)",
        )
        .bind(m.id.to_string())
        .bind(m.conversation_id.to_string())
        .bind(m.author.to_string())
        .bind(json_in(&m.blocks)?)
        .bind(fmt_ts(&m.created_at))
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn update_message(&self, m: &Message) -> StoreResult<()> {
        sqlx::query("UPDATE messages SET author = ?, blocks = ? WHERE id = ?")
            .bind(m.author.to_string())
            .bind(json_in(&m.blocks)?)
            .bind(m.id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }

    async fn delete_message(&self, id: MessageId) -> StoreResult<()> {
        sqlx::query("DELETE FROM messages WHERE id = ?")
            .bind(id.to_string())
            .execute(&self.pool)
            .await
            .map_err(db_err)?;
        Ok(())
    }
}

impl PermissionRequestRepo for SqliteStore {
    async fn get_permission_request(
        &self,
        id: PermissionRequestId,
    ) -> StoreResult<Option<PermissionRequest>> {
        let rows = sqlx::query("SELECT * FROM permission_requests WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_permission_request).transpose()
    }

    async fn list_permission_requests(&self) -> StoreResult<Vec<PermissionRequest>> {
        let rows = sqlx::query("SELECT * FROM permission_requests ORDER BY created_at")
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_permission_request).collect()
    }

    async fn list_pending_permission_requests(&self) -> StoreResult<Vec<PermissionRequest>> {
        let rows = sqlx::query(
            "SELECT * FROM permission_requests WHERE status = 'pending' \
             ORDER BY created_at",
        )
        .fetch_all(&self.pool)
        .await
        .map_err(db_err)?;
        rows.iter().map(row_to_permission_request).collect()
    }

    async fn insert_permission_request(&self, p: &PermissionRequest) -> StoreResult<()> {
        let (status, resolved_option_id) = permission_status_parts(&p.status);
        sqlx::query(
            "INSERT INTO permission_requests (id, conversation_id, run_id, \
             tool_call, options, status, resolved_option_id, created_at, \
             resolved_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(p.id.to_string())
        .bind(p.conversation_id.map(|c| c.to_string()))
        .bind(p.run_id.map(|r| r.to_string()))
        .bind(json_in(&p.tool_call)?)
        .bind(json_in(&p.options)?)
        .bind(status)
        .bind(resolved_option_id)
        .bind(fmt_ts(&p.created_at))
        .bind(p.resolved_at.as_ref().map(fmt_ts))
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn update_permission_request(&self, p: &PermissionRequest) -> StoreResult<()> {
        let (status, resolved_option_id) = permission_status_parts(&p.status);
        sqlx::query(
            "UPDATE permission_requests SET conversation_id = ?, run_id = ?, \
             tool_call = ?, options = ?, status = ?, resolved_option_id = ?, \
             resolved_at = ? WHERE id = ?",
        )
        .bind(p.conversation_id.map(|c| c.to_string()))
        .bind(p.run_id.map(|r| r.to_string()))
        .bind(json_in(&p.tool_call)?)
        .bind(json_in(&p.options)?)
        .bind(status)
        .bind(resolved_option_id)
        .bind(p.resolved_at.as_ref().map(fmt_ts))
        .bind(p.id.to_string())
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }
}

fn row_to_research(row: &sqlx::sqlite::SqliteRow) -> StoreResult<Research> {
    Ok(Research {
        id: parse_id(row.get::<String, _>("id").as_str())?,
        organization_id: parse_id(row.get::<String, _>("organization_id").as_str())?,
        project_id: opt_id(row.get::<Option<String>, _>("project_id").as_deref())?,
        repository_ids: json_out(row.get::<String, _>("repository_ids").as_str())?,
        question: row.get("question"),
        status: en(row.get::<String, _>("status").as_str())?,
        findings: row.get("findings"),
        conversation_id: opt_id(row.get::<Option<String>, _>("conversation_id").as_deref())?,
        origin_conversation_id: opt_id(
            row.get::<Option<String>, _>("origin_conversation_id")
                .as_deref(),
        )?,
        model_profile_id: opt_id(row.get::<Option<String>, _>("model_profile_id").as_deref())?,
        created_at: ts(row.get::<String, _>("created_at").as_str())?,
        finished_at: opt_ts(row.get::<Option<String>, _>("finished_at").as_deref())?,
    })
}

impl ResearchRepo for SqliteStore {
    async fn get_research(&self, id: ResearchId) -> StoreResult<Option<Research>> {
        let rows = sqlx::query("SELECT * FROM research WHERE id = ?")
            .bind(id.to_string())
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.first().map(row_to_research).transpose()
    }

    async fn list_research(&self) -> StoreResult<Vec<Research>> {
        let rows = sqlx::query("SELECT * FROM research ORDER BY created_at DESC")
            .fetch_all(&self.pool)
            .await
            .map_err(db_err)?;
        rows.iter().map(row_to_research).collect()
    }

    async fn list_research_by_project(&self, project_id: ProjectId) -> StoreResult<Vec<Research>> {
        let rows =
            sqlx::query("SELECT * FROM research WHERE project_id = ? ORDER BY created_at DESC")
                .bind(project_id.to_string())
                .fetch_all(&self.pool)
                .await
                .map_err(db_err)?;
        rows.iter().map(row_to_research).collect()
    }

    async fn list_research_by_origin_conversation(
        &self,
        conversation_id: ConversationId,
    ) -> StoreResult<Vec<Research>> {
        let rows = sqlx::query(
            "SELECT * FROM research WHERE origin_conversation_id = ? \
             ORDER BY created_at",
        )
        .bind(conversation_id.to_string())
        .fetch_all(&self.pool)
        .await
        .map_err(db_err)?;
        rows.iter().map(row_to_research).collect()
    }

    async fn insert_research(&self, r: &Research) -> StoreResult<()> {
        sqlx::query(
            "INSERT INTO research (id, organization_id, project_id, repository_ids, \
             question, status, findings, conversation_id, origin_conversation_id, \
             model_profile_id, created_at, finished_at) \
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(r.id.to_string())
        .bind(r.organization_id.to_string())
        .bind(r.project_id.map(|p| p.to_string()))
        .bind(json_in(&r.repository_ids)?)
        .bind(&r.question)
        .bind(r.status.to_string())
        .bind(&r.findings)
        .bind(r.conversation_id.map(|c| c.to_string()))
        .bind(r.origin_conversation_id.map(|c| c.to_string()))
        .bind(r.model_profile_id.map(|m| m.to_string()))
        .bind(fmt_ts(&r.created_at))
        .bind(r.finished_at.as_ref().map(fmt_ts))
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }

    async fn update_research(&self, r: &Research) -> StoreResult<()> {
        sqlx::query(
            "UPDATE research SET organization_id = ?, project_id = ?, \
             repository_ids = ?, question = ?, status = ?, findings = ?, \
             conversation_id = ?, origin_conversation_id = ?, \
             model_profile_id = ?, finished_at = ? WHERE id = ?",
        )
        .bind(r.organization_id.to_string())
        .bind(r.project_id.map(|p| p.to_string()))
        .bind(json_in(&r.repository_ids)?)
        .bind(&r.question)
        .bind(r.status.to_string())
        .bind(&r.findings)
        .bind(r.conversation_id.map(|c| c.to_string()))
        .bind(r.origin_conversation_id.map(|c| c.to_string()))
        .bind(r.model_profile_id.map(|m| m.to_string()))
        .bind(r.finished_at.as_ref().map(fmt_ts))
        .bind(r.id.to_string())
        .execute(&self.pool)
        .await
        .map_err(db_err)?;
        Ok(())
    }
}
