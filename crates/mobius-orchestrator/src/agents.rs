//! Provisions the coordinator agents chats run as: the organization agent
//! (seeded, required) and per-project agents (created on first use by
//! cloning the org agent's profiles and policy).

use crate::error::OrchestratorError;
use mobius_core::*;

/// Instructions given to auto-provisioned project coordinator agents.
/// Coordinators never touch code: they research and create tasks.
pub const PROJECT_COORDINATOR_INSTRUCTIONS: &str = "\
You are the coordinator for this project. You do NOT edit code yourself, \
and you do not have filesystem access to repository checkouts. For code \
facts, use `mobius research start`. For anything that must physically \
change, create a task with `mobius task create` — a coding agent will \
execute it in a fresh worktree. Write durable knowledge to memory \
explicitly with `mobius memory add --scope project`.";

pub struct AgentProvisioner;

impl AgentProvisioner {
    /// The organization coordinator template must exist (it is seeded);
    /// absence is a configuration error surfaced as HTTP 422.
    pub async fn ensure_org_agent<S: AgentRepo>(
        store: &S,
        org: &Organization,
    ) -> Result<Agent, OrchestratorError> {
        store
            .find_agent_by_role_and_org(org.id, AgentRole::Organization)
            .await?
            .ok_or_else(|| {
                OrchestratorError::Config(format!(
                    "organization {:?} has no organization agent; run `seed-dev`",
                    org.slug
                ))
            })
    }

    /// Find the project's coordinator (`role = Project`, `project_id = id`);
    /// create it on first use by cloning the org agent's profiles and
    /// permission policy.
    pub async fn ensure_project_agent<S: AgentRepo + ProjectRepo + OrganizationRepo>(
        store: &S,
        project: &Project,
    ) -> Result<Agent, OrchestratorError> {
        if let Some(agent) = store
            .list_agents_by_project(project.id)
            .await?
            .into_iter()
            .find(|a| a.role == AgentRole::Project)
        {
            return Ok(agent);
        }
        let org = store
            .get_organization(project.organization_id)
            .await?
            .ok_or_else(|| {
                OrchestratorError::Config(format!(
                    "project {:?} references missing organization {}",
                    project.slug, project.organization_id
                ))
            })?;
        let template = Self::ensure_org_agent(store, &org).await?;
        let agent = Agent {
            id: AgentId::new(),
            name: format!("{}-agent", project.slug),
            role: AgentRole::Project,
            organization_id: org.id,
            profiles: template.profiles.clone(),
            project_id: Some(project.id),
            repository_id: None,
            instructions: PROJECT_COORDINATOR_INSTRUCTIONS.to_string(),
            permission_policy: template.permission_policy,
            status: AgentStatus::Idle,
            created_at: chrono::Utc::now(),
        };
        store.insert_agent(&agent).await?;
        Ok(agent)
    }
}
