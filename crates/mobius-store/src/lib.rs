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
            kind: TaskKind::Implement,
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
            repository_id: repo.id,
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
        assert_eq!(store.list_projects().await.expect("p").len(), 1);
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
            superseded_by: None,
            created_at: chrono::Utc::now(),
        };
        store.insert_memory_entry(&entry).await.expect("insert");
        let dir = tempfile::tempdir().expect("tempdir");
        MemoryDumper::dump(&store, dir.path()).await.expect("dump");
        let path = dir.path().join("memory").join("my-org.md");
        let content = std::fs::read_to_string(&path).expect("read dump");
        assert!(content.contains("the sky is blue"));
        assert!(content.contains("## facts"));
    }
}
