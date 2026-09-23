//! `ResearchService`: a read-only agent answering a question inside a
//! repository checkout (single repo) or the org memory dir (zero/multiple
//! repos). Research is its own entity — not a task kind — and produces
//! findings, no artifacts.

use crate::dispatcher::EmitFn;
use crate::error::OrchestratorError;
use crate::prompt::{MemoryGroup, ResearchContext, research_prompt};
use crate::session_manager::{OpenConversation, SessionManager};
use mobius_core::*;
use std::path::PathBuf;
use std::sync::Arc;

pub struct StartResearch {
    /// Optional: derived from `project_id` or the single organization.
    pub organization_id: Option<OrganizationId>,
    pub project_id: Option<ProjectId>,
    /// Repositories the researcher may read. Each must exist and have a
    /// `local_path` (they are listed with absolute paths in the prompt).
    pub repository_ids: Vec<RepositoryId>,
    pub question: String,
    /// The chat that spawned this research (drives the updates block).
    pub origin_conversation_id: Option<ConversationId>,
    pub model_profile_id: Option<ModelProfileId>,
}

pub struct ResearchService<S: Store> {
    store: Arc<S>,
    emit: EmitFn,
    sessions: Arc<SessionManager<S>>,
    data_dir: PathBuf,
}

impl<S: Store + 'static> ResearchService<S> {
    pub fn new(
        store: Arc<S>,
        emit: EmitFn,
        sessions: Arc<SessionManager<S>>,
        data_dir: PathBuf,
    ) -> Self {
        Self {
            store,
            emit,
            sessions,
            data_dir,
        }
    }

    /// Create the `Research` row, open its `Research` conversation (policy
    /// `ReadOnly`), emit `ResearchStarted`, and drive `run_turn` in the
    /// background: findings = the `## Findings` section (or the full reply).
    pub async fn start(&self, spec: StartResearch) -> Result<Research, OrchestratorError> {
        let project = match spec.project_id {
            Some(id) => Some(
                self.store
                    .get_project(id)
                    .await?
                    .ok_or_else(|| OrchestratorError::NotFound(format!("project {id}")))?,
            ),
            None => None,
        };
        let org = resolve_org(self.store.as_ref(), spec.organization_id, project.as_ref()).await?;

        let repositories = resolve_repositories(
            self.store.as_ref(),
            &spec.repository_ids,
            project.as_ref(),
            &org,
        )
        .await?;

        // cwd: the checkout when exactly one repo, else the org memory dir
        // (paths for every repo are still listed in the prompt).
        let cwd = match repositories.as_slice() {
            [single] => single
                .local_path
                .clone()
                .expect("local_path validated above"),
            _ => {
                let dir = memory_paths::org_dir(&self.data_dir, &org.slug);
                std::fs::create_dir_all(&dir)?;
                dir
            }
        };

        let research_id = ResearchId::new();
        let title = format!("research: {}", first_line(&spec.question, 60));
        // `conversations.research_id` FKs to `research`, which doesn't exist
        // yet — link it after the research row is inserted.
        let mut conversation = self
            .sessions
            .open_conversation(OpenConversation {
                organization_id: Some(org.id),
                project_id: project.as_ref().map(|p| p.id),
                title: Some(title),
                model_profile_id: spec.model_profile_id,
                kind: ConversationKind::Research,
                activity: Activity::Research,
                run_id: None,
                research_id: None,
                task_id: None,
                repository_id: repositories
                    .first()
                    .map(|r| r.id)
                    .filter(|_| repositories.len() == 1),
                workdir: Some(cwd),
                permission_policy: Some(PermissionPolicy::ReadOnly),
            })
            .await?;

        let now = chrono::Utc::now();
        let research = Research {
            id: research_id,
            organization_id: org.id,
            project_id: project.as_ref().map(|p| p.id),
            // The *resolved* set — defaults included — is what persists.
            repository_ids: repositories.iter().map(|r| r.id).collect(),
            question: spec.question.clone(),
            status: ResearchStatus::Running,
            findings: None,
            conversation_id: Some(conversation.id),
            origin_conversation_id: spec.origin_conversation_id,
            model_profile_id: spec
                .model_profile_id
                .or(Some(conversation.model_profile_id)),
            created_at: now,
            finished_at: None,
        };
        self.store.insert_research(&research).await?;
        conversation.research_id = Some(research.id);
        self.store.update_conversation(&conversation).await?;
        (self.emit)(DomainEvent::ResearchStarted {
            research: research.clone(),
        });

        // Memory groups: org → repos (owner/name label) → project.
        let mut memory = vec![MemoryGroup::new(
            "Organization",
            self.store
                .list_memory_by_scope(&MemoryScope::Organization(org.id))
                .await?,
        )];
        for r in &repositories {
            memory.push(MemoryGroup::new(
                format!("Repository {}/{}", r.owner, r.name),
                self.store
                    .list_memory_by_scope(&MemoryScope::Repository(r.id))
                    .await?,
            ));
        }
        if let Some(p) = &project {
            memory.push(MemoryGroup::new(
                format!("Project {}", p.slug),
                self.store
                    .list_memory_by_scope(&MemoryScope::Project(p.id))
                    .await?,
            ));
        }
        let prompt = research_prompt(&ResearchContext {
            org,
            project,
            repositories,
            memory,
            research: research.clone(),
        });

        let sessions = self.sessions.clone();
        let store = self.store.clone();
        let emit = self.emit.clone();
        let mut running = research.clone();
        tokio::spawn(async move {
            let outcome = sessions.run_turn(conversation.id, &prompt).await;
            match outcome {
                Ok(msg) => {
                    running.findings = Some(extract_findings(&message_text(&msg)));
                    running.status = ResearchStatus::Done;
                }
                Err(e) => {
                    running.status = ResearchStatus::Failed;
                    running.findings = Some(format!("research failed: {e}"));
                }
            }
            running.finished_at = Some(chrono::Utc::now());
            if let Err(e) = store.update_research(&running).await {
                tracing::warn!(error = %e, "failed to update research");
            }
            emit(DomainEvent::ResearchFinished { research: running });
        });
        Ok(research)
    }

    /// Cancel a pending/running research: stop its live turn (if any) and
    /// mark it cancelled.
    pub async fn cancel(&self, id: ResearchId) -> Result<Research, OrchestratorError> {
        let mut research = self
            .store
            .get_research(id)
            .await?
            .ok_or_else(|| OrchestratorError::NotFound(format!("research {id}")))?;
        if matches!(
            research.status,
            ResearchStatus::Pending | ResearchStatus::Running
        ) {
            if let Some(conv_id) = research.conversation_id {
                let _ = self.sessions.cancel(conv_id).await;
            }
            research.status = ResearchStatus::Cancelled;
            research.finished_at = Some(chrono::Utc::now());
            self.store.update_research(&research).await?;
            (self.emit)(DomainEvent::ResearchFinished {
                research: research.clone(),
            });
        }
        Ok(research)
    }
}

/// Explicit `requested` ids are validated strictly — each must exist and
/// have a local checkout. An empty list defaults to the project's hinted
/// repositories (project research) or every organization repository (org
/// research), keeping only those with a `local_path`.
async fn resolve_repositories<S: RepositoryRepo>(
    store: &S,
    requested: &[RepositoryId],
    project: Option<&Project>,
    org: &Organization,
) -> Result<Vec<Repository>, OrchestratorError> {
    if !requested.is_empty() {
        let mut repositories = Vec::new();
        for id in requested {
            let repo = store
                .get_repository(*id)
                .await?
                .ok_or_else(|| OrchestratorError::NotFound(format!("repository {id}")))?;
            if repo.local_path.is_none() {
                return Err(OrchestratorError::Config(format!(
                    "repository {}/{} has no local checkout",
                    repo.owner, repo.name
                )));
            }
            repositories.push(repo);
        }
        return Ok(repositories);
    }

    let candidates: Vec<Repository> = match project {
        Some(p) => {
            let mut v = Vec::new();
            for id in &p.repository_ids {
                if let Some(r) = store.get_repository(*id).await? {
                    v.push(r);
                }
            }
            v
        }
        None => store
            .list_repositories()
            .await?
            .into_iter()
            .filter(|r| r.organization_id == org.id)
            .collect(),
    };
    Ok(candidates
        .into_iter()
        .filter(|r| r.local_path.is_some())
        .collect())
}

async fn resolve_org<S: OrganizationRepo + ProjectRepo>(
    store: &S,
    organization_id: Option<OrganizationId>,
    project: Option<&Project>,
) -> Result<Organization, OrchestratorError> {
    if let Some(id) = organization_id {
        return store
            .get_organization(id)
            .await?
            .ok_or_else(|| OrchestratorError::NotFound(format!("organization {id}")));
    }
    if let Some(p) = project {
        return store
            .get_organization(p.organization_id)
            .await?
            .ok_or_else(|| OrchestratorError::Config("missing organization".into()));
    }
    let orgs = store.list_organizations().await?;
    match orgs.as_slice() {
        [single] => Ok(single.clone()),
        _ => Err(OrchestratorError::Config(format!(
            "cannot resolve organization ({} registered); specify one",
            orgs.len()
        ))),
    }
}

fn first_line(text: &str, max_chars: usize) -> String {
    text.trim()
        .lines()
        .next()
        .unwrap_or("")
        .chars()
        .take(max_chars)
        .collect()
}

/// The concatenated `Text` blocks of a message.
pub(crate) fn message_text(msg: &Message) -> String {
    msg.blocks
        .iter()
        .filter_map(|b| match b {
            ContentBlock::Text { text } => Some(text.as_str()),
            _ => None,
        })
        .collect::<Vec<_>>()
        .join("\n")
}

/// Everything from `## Findings` to the end of the message (trailing
/// `## Open questions` is part of the deliverable), or the full text when
/// no `## Findings` heading exists.
pub(crate) fn extract_findings(text: &str) -> String {
    if let Some(start) = text.find("## Findings") {
        return text[start + "## Findings".len()..].trim().to_string();
    }
    text.trim().to_string()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    #[test]
    fn findings_extracts_section() {
        let text =
            "preamble\n\n## Findings\n\n- fact one\n- fact two\n\n## Open questions\n\n- q\n";
        assert_eq!(
            extract_findings(text),
            "- fact one\n- fact two\n\n## Open questions\n\n- q"
        );
    }

    #[test]
    fn findings_falls_back_to_full_text() {
        assert_eq!(extract_findings("  just an answer  "), "just an answer");
    }

    fn org() -> Organization {
        Organization {
            id: OrganizationId::new(),
            name: "Org".into(),
            slug: "org".into(),
            created_at: chrono::Utc::now(),
        }
    }

    fn repo(org_id: OrganizationId, name: &str, checked_out: bool) -> Repository {
        Repository {
            id: RepositoryId::new(),
            organization_id: org_id,
            owner: "o".into(),
            name: name.into(),
            provider: RepoProvider::GitHub,
            default_branch: "main".into(),
            local_path: checked_out.then(|| PathBuf::from(format!("/tmp/{name}"))),
            created_at: chrono::Utc::now(),
        }
    }

    fn project(org_id: OrganizationId, repository_ids: Vec<RepositoryId>) -> Project {
        Project {
            id: ProjectId::new(),
            organization_id: org_id,
            repository_ids,
            name: "p".into(),
            slug: "p".into(),
            description: String::new(),
            scope: ProjectScope::default(),
            status: ProjectStatus::Active,
            created_at: chrono::Utc::now(),
        }
    }

    #[tokio::test]
    async fn empty_repo_list_defaults_to_project_hints_with_checkouts() {
        let store = InMemoryStore::new();
        let org = org();
        let with = repo(org.id, "with", true);
        let without = repo(org.id, "without", false);
        store.insert_repository(&with).await.expect("r1");
        store.insert_repository(&without).await.expect("r2");
        let p = project(org.id, vec![with.id, without.id]);

        let repos = resolve_repositories(&store, &[], Some(&p), &org)
            .await
            .expect("resolve");
        // Only the hinted repo that has a local checkout.
        assert_eq!(repos.len(), 1);
        assert_eq!(repos[0].id, with.id);
    }

    #[tokio::test]
    async fn empty_repo_list_defaults_to_org_repositories() {
        let store = InMemoryStore::new();
        let org = org();
        let a = repo(org.id, "a", true);
        let b = repo(org.id, "b", true);
        let remote_only = repo(org.id, "remote", false);
        let other_org = repo(OrganizationId::new(), "foreign", true);
        for r in [&a, &b, &remote_only, &other_org] {
            store.insert_repository(r).await.expect("r");
        }

        let repos = resolve_repositories(&store, &[], None, &org)
            .await
            .expect("resolve");
        // Org-scoped: every org repo with a checkout; foreign and
        // remote-only repos excluded.
        assert_eq!(repos.len(), 2);
        assert!(repos.iter().all(|r| r.organization_id == org.id));
    }

    #[tokio::test]
    async fn explicit_repo_without_checkout_errors() {
        let store = InMemoryStore::new();
        let org = org();
        let remote = repo(org.id, "remote", false);
        store.insert_repository(&remote).await.expect("r");

        let err = resolve_repositories(&store, &[remote.id], None, &org)
            .await
            .expect_err("must fail");
        assert!(matches!(err, OrchestratorError::Config(_)));
    }
}
