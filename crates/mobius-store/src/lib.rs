pub mod dumper;
pub mod seed;
pub mod store;

pub use dumper::MemoryDumper;
pub use seed::{DevSeed, default_harnesses, dev_fixture, ensure_default_harnesses};
pub use store::SqliteStore;

#[cfg(test)]
mod tests {
    use super::*;
    use mobius_core::*;

    fn test_task(project_id: ProjectId, agent_id: AgentId) -> Task {
        let now = chrono::Utc::now();
        Task {
            id: TaskId::new(),
            project_id,
            agent_id,
            parent_task_id: None,
            title: "test task".to_string(),
            description: "desc".to_string(),
            repository_id: None,
            kind: TaskKind::Feature,
            status: TaskStatus::Proposed,
            origin: TaskOrigin::default(),
            priority: Priority::Normal,
            created_at: now,
            updated_at: now,
        }
    }

    async fn seed_task_parents(
        store: &SqliteStore,
    ) -> (
        Organization,
        Repository,
        Project,
        Harness,
        ModelProfile,
        Agent,
    ) {
        let org = Organization {
            id: OrganizationId::new(),
            name: "Org".into(),
            slug: "org".into(),
            created_at: chrono::Utc::now(),
        };
        store.insert_organization(&org).await.expect("insert org");
        let repo = Repository {
            id: RepositoryId::new(),
            organization_id: org.id,
            owner: "o".into(),
            name: "r".into(),
            provider: RepoProvider::GitHub,
            default_branch: "main".into(),
            local_path: None,
            created_at: chrono::Utc::now(),
        };
        store.insert_repository(&repo).await.expect("insert repo");
        let project = Project {
            id: ProjectId::new(),
            organization_id: org.id,
            repository_ids: vec![repo.id],
            name: "P".into(),
            slug: "p".into(),
            description: String::new(),
            scope: ProjectScope::default(),
            status: ProjectStatus::Active,
            created_at: chrono::Utc::now(),
        };
        store
            .insert_project(&project)
            .await
            .expect("insert project");
        let harness = Harness {
            id: HarnessId::new(),
            name: "h".into(),
            command: "echo".into(),
            args: vec![],
            env: Default::default(),
            default_permission_policy: PermissionPolicy::Auto,
            model_arg_template: vec![],
            enabled: true,
            created_at: chrono::Utc::now(),
        };
        store
            .insert_harness(&harness)
            .await
            .expect("insert harness");
        let profile = ModelProfile {
            id: ModelProfileId::new(),
            name: "p1".into(),
            harness_id: harness.id,
            model: None,
            effort: None,
            config: Default::default(),
            created_at: chrono::Utc::now(),
        };
        store
            .insert_model_profile(&profile)
            .await
            .expect("insert profile");
        let agent = Agent {
            id: AgentId::new(),
            name: "a".into(),
            role: AgentRole::Project,
            organization_id: org.id,
            profiles: ActivityProfiles {
                default: profile.id,
                overrides: Default::default(),
            },
            project_id: Some(project.id),
            repository_id: Some(repo.id),
            instructions: String::new(),
            permission_policy: PermissionPolicy::Auto,
            status: AgentStatus::Idle,
            created_at: chrono::Utc::now(),
        };
        store.insert_agent(&agent).await.expect("insert agent");
        (org, repo, project, harness, profile, agent)
    }

    #[tokio::test]
    async fn task_roundtrip_and_status_update() {
        let store = SqliteStore::open_memory().await.expect("open");
        let (_org, _repo, project, _h, _p, agent) = seed_task_parents(&store).await;
        let task = test_task(project.id, agent.id);
        store.insert_task(&task).await.expect("insert task");

        let fetched = store.get_task(task.id).await.expect("get");
        assert_eq!(fetched, Some(task.clone()));

        let mut updated = task.clone();
        updated.status = TaskStatus::Approved;
        assert!(task.status.can_transition_to(&updated.status));
        updated.updated_at = chrono::Utc::now();
        store.update_task(&updated).await.expect("update task");
        let fetched = store.get_task(task.id).await.expect("get2");
        assert_eq!(fetched.map(|t| t.status), Some(TaskStatus::Approved));
    }

    #[tokio::test]
    async fn memory_entry_roundtrip_and_scope_query() {
        let store = SqliteStore::open_memory().await.expect("open");
        let org = Organization {
            id: OrganizationId::new(),
            name: "Org".into(),
            slug: "org".into(),
            created_at: chrono::Utc::now(),
        };
        store.insert_organization(&org).await.expect("insert org");
        let entry = MemoryEntry {
            id: MemoryEntryId::new(),
            scope: MemoryScope::Organization(org.id),
            kind: MemoryKind::Convention,
            content: "use typed ids".into(),
            source_run_id: None,
            source_conversation_id: None,
            superseded_by: None,
            created_at: chrono::Utc::now(),
        };
        store.insert_memory_entry(&entry).await.expect("insert");
        let fetched = store.get_memory_entry(entry.id).await.expect("get");
        assert_eq!(fetched, Some(entry.clone()));
        let by_scope = store
            .list_memory_by_scope(&MemoryScope::Organization(org.id))
            .await
            .expect("by scope");
        assert_eq!(by_scope.len(), 1);
    }

    #[tokio::test]
    async fn signal_dedupe_lookup() {
        let store = SqliteStore::open_memory().await.expect("open");
        let signal = Signal {
            id: SignalId::new(),
            source: SourceKind::GitHub,
            kind: SignalKind::IssueOpened,
            repository_id: None,
            project_id: None,
            dedupe_key: "github:o/r:issue:1".into(),
            title: "t".into(),
            body: "b".into(),
            payload: serde_json::json!({}),
            occurred_at: chrono::Utc::now(),
            ingested_at: chrono::Utc::now(),
        };
        store.insert_signal(&signal).await.expect("insert");
        let found = store
            .find_signal_by_dedupe_key("github:o/r:issue:1")
            .await
            .expect("find");
        assert_eq!(found.map(|s| s.id), Some(signal.id));
        let dup = store.insert_signal(&signal).await;
        assert!(matches!(dup, Err(StoreError::Conflict(_))));
    }

    #[tokio::test]
    async fn seed_is_idempotent() {
        let store = SqliteStore::open_memory().await.expect("open");
        let dir = tempfile::tempdir().expect("tempdir");
        std::process::Command::new("git")
            .args(["init"])
            .current_dir(dir.path())
            .output()
            .expect("git init");
        for _ in 0..2 {
            ensure_default_harnesses(&store).await.expect("harnesses");
            dev_fixture(&store, dir.path()).await.expect("fixture");
        }
        assert_eq!(store.list_harnesses().await.expect("h").len(), 5);
        assert_eq!(store.list_organizations().await.expect("o").len(), 1);
        assert_eq!(store.list_repositories().await.expect("r").len(), 1);
        assert_eq!(store.list_projects().await.expect("p").len(), 3);
        assert_eq!(store.list_model_profiles().await.expect("mp").len(), 3);
        assert_eq!(store.list_agents().await.expect("a").len(), 1);
    }

    #[tokio::test]
    async fn memory_dumper_writes_markdown() {
        let store = SqliteStore::open_memory().await.expect("open");
        let org = Organization {
            id: OrganizationId::new(),
            name: "My Org".into(),
            slug: "my-org".into(),
            created_at: chrono::Utc::now(),
        };
        store.insert_organization(&org).await.expect("insert org");
        let entry = MemoryEntry {
            id: MemoryEntryId::new(),
            scope: MemoryScope::Organization(org.id),
            kind: MemoryKind::Fact,
            content: "the sky is blue".into(),
            source_run_id: None,
            source_conversation_id: None,
            superseded_by: None,
            created_at: chrono::Utc::now(),
        };
        store.insert_memory_entry(&entry).await.expect("insert");
        let dir = tempfile::tempdir().expect("tempdir");
        MemoryDumper::dump(&store, dir.path()).await.expect("dump");
        let path = dir
            .path()
            .join("memory")
            .join("my-org")
            .join("organization.md");
        let content = std::fs::read_to_string(&path).expect("read dump");
        assert!(content.contains("the sky is blue"));
        assert!(content.contains("## facts"));
        // The catalogue is written even with no projects.
        let catalogue = dir.path().join("memory").join("my-org").join("projects.md");
        assert!(
            std::fs::read_to_string(&catalogue)
                .expect("catalogue")
                .contains("projects")
        );
    }

    /// Create a database with only migration 0001 applied, insert
    /// old-schema rows, run the rest of the migrations, and assert the
    /// 0002 backfills.
    #[tokio::test]
    async fn migration_0002_backfills_scopes() {
        let dir = tempfile::tempdir().expect("tempdir");
        let db_path = dir.path().join("migrate.db");
        let options = sqlx::sqlite::SqliteConnectOptions::new()
            .filename(&db_path)
            .create_if_missing(true);
        let pool = sqlx::sqlite::SqlitePool::connect_with(options)
            .await
            .expect("connect");
        let migrator = sqlx::migrate!("./migrations");
        let migrations: Vec<_> = migrator.iter().collect();
        assert!(migrations.len() >= 2);

        // Apply 0001 verbatim and record it in _sqlx_migrations so the
        // migrator only runs 0002+ afterwards.
        let m1 = migrations[0];
        sqlx::raw_sql(sqlx::AssertSqlSafe(m1.sql.as_str()))
            .execute(&pool)
            .await
            .expect("apply 0001");
        sqlx::query(
            "CREATE TABLE IF NOT EXISTS _sqlx_migrations (\
             version BIGINT PRIMARY KEY, description TEXT NOT NULL, \
             installed_on TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, \
             success BOOLEAN NOT NULL, checksum BLOB NOT NULL, \
             execution_time BIGINT NOT NULL)",
        )
        .execute(&pool)
        .await
        .expect("migrations table");
        sqlx::query(
            "INSERT INTO _sqlx_migrations (version, description, success, \
             checksum, execution_time) VALUES (?, ?, 1, ?, 0)",
        )
        .bind(m1.version)
        .bind(&*m1.description)
        .bind(&*m1.checksum)
        .execute(&pool)
        .await
        .expect("record 0001");

        // Old-schema rows.
        let org_id = OrganizationId::new().to_string();
        let repo_id = RepositoryId::new().to_string();
        let project_id = ProjectId::new().to_string();
        let agent_id = AgentId::new().to_string();
        let profile_id = ModelProfileId::new().to_string();
        let harness_id = HarnessId::new().to_string();
        let ts = "2025-01-01T00:00:00Z";
        sqlx::query(
            "INSERT INTO organizations (id, name, slug, created_at) VALUES (?, 'O', 'o', ?)",
        )
        .bind(&org_id)
        .bind(ts)
        .execute(&pool)
        .await
        .expect("org");
        sqlx::query(
            "INSERT INTO repositories (id, organization_id, owner, name, \
             provider, default_branch, local_path, created_at) \
             VALUES (?, ?, 'o', 'r', 'git_hub', 'main', '/tmp/r', ?)",
        )
        .bind(&repo_id)
        .bind(&org_id)
        .bind(ts)
        .execute(&pool)
        .await
        .expect("repo");
        sqlx::query(
            "INSERT INTO projects (id, repository_id, name, slug, \
             description, scope, status, created_at) \
             VALUES (?, ?, 'P', 'p', '', '{}', 'active', ?)",
        )
        .bind(&project_id)
        .bind(&repo_id)
        .bind(ts)
        .execute(&pool)
        .await
        .expect("project");
        sqlx::query(
            "INSERT INTO harnesses (id, name, command, args, env, \
             default_permission_policy, model_arg_template, enabled, \
             created_at) VALUES (?, 'devin', 'devin', '[]', '{}', \
             'ask_human', '[]', 1, ?)",
        )
        .bind(&harness_id)
        .bind(ts)
        .execute(&pool)
        .await
        .expect("harness");
        sqlx::query(
            "INSERT INTO model_profiles (id, name, harness_id, model, \
             effort, config, created_at) VALUES (?, 'p1', ?, NULL, NULL, \
             '{}', ?)",
        )
        .bind(&profile_id)
        .bind(&harness_id)
        .bind(ts)
        .execute(&pool)
        .await
        .expect("profile");
        let profiles = format!("{{\"default\":\"{profile_id}\",\"overrides\":{{}}}}");
        sqlx::query(
            "INSERT INTO agents (id, name, role, profiles, project_id, \
             repository_id, instructions, permission_policy, status, \
             created_at) VALUES (?, 'a', 'project', ?, ?, ?, '', \
             'auto', 'idle', ?)",
        )
        .bind(&agent_id)
        .bind(&profiles)
        .bind(&project_id)
        .bind(&repo_id)
        .bind(ts)
        .execute(&pool)
        .await
        .expect("agent");
        for kind in ["implement", "research"] {
            sqlx::query(
                "INSERT INTO tasks (id, project_id, agent_id, title, \
                 description, kind, status, origin, priority, created_at, \
                 updated_at) VALUES (?, ?, ?, 't', '', ?, 'proposed', \
                 '{}', 'normal', ?, ?)",
            )
            .bind(TaskId::new().to_string())
            .bind(&project_id)
            .bind(&agent_id)
            .bind(kind)
            .bind(ts)
            .bind(ts)
            .execute(&pool)
            .await
            .expect("task");
        }
        sqlx::query(
            "INSERT INTO conversations (id, agent_id, activity, \
             model_profile_id, workdir, title, status, config_options, \
             created_at, updated_at) VALUES (?, ?, 'chat', ?, '/tmp', \
             'c', 'idle', '[]', ?, ?)",
        )
        .bind(ConversationId::new().to_string())
        .bind(&agent_id)
        .bind(&profile_id)
        .bind(ts)
        .bind(ts)
        .execute(&pool)
        .await
        .expect("conversation");

        // Run the remaining migrations through the store opener.
        let store = SqliteStore::open(&db_path).await.expect("migrate");

        let project = store
            .list_projects()
            .await
            .expect("projects")
            .into_iter()
            .next()
            .expect("one project");
        assert_eq!(project.organization_id.to_string(), org_id);
        assert_eq!(
            project
                .repository_ids
                .iter()
                .map(|r| r.to_string())
                .collect::<Vec<_>>(),
            vec![repo_id.clone()]
        );

        let agent = store
            .list_agents()
            .await
            .expect("agents")
            .into_iter()
            .next()
            .expect("one agent");
        assert_eq!(agent.organization_id.to_string(), org_id);

        let mut kinds: Vec<String> = store
            .list_tasks()
            .await
            .expect("tasks")
            .iter()
            .map(|t| t.kind.to_string())
            .collect();
        kinds.sort();
        assert_eq!(kinds, ["feature", "triage"]);
        for task in store.list_tasks().await.expect("tasks") {
            assert_eq!(
                task.repository_id.map(|r| r.to_string()),
                Some(repo_id.clone())
            );
        }

        let conv = store
            .list_conversations()
            .await
            .expect("conversations")
            .into_iter()
            .next()
            .expect("one conversation");
        assert_eq!(conv.organization_id.to_string(), org_id);
        assert_eq!(conv.project_id.map(|p| p.to_string()), Some(project_id));
        assert_eq!(conv.kind, ConversationKind::Chat);

        assert!(store.list_research().await.expect("research").is_empty());
    }
}
