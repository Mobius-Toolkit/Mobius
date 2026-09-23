//! Resolves an agent's `ActivityProfiles` to a concrete `(ModelProfile,
//! Harness)` pair for a given activity.

use crate::error::OrchestratorError;
use mobius_core::{Activity, Agent, Harness, HarnessRepo, ModelProfile, ModelProfileRepo};

pub struct ProfileResolver;

impl ProfileResolver {
    pub async fn resolve<S: ModelProfileRepo + HarnessRepo>(
        store: &S,
        agent: &Agent,
        activity: Activity,
    ) -> Result<(ModelProfile, Harness), OrchestratorError> {
        let profile_id = agent.profiles.profile_for(activity);
        let profile = store.get_model_profile(profile_id).await?.ok_or_else(|| {
            OrchestratorError::NotFound(format!(
                "agent {:?} profile {profile_id} for {activity} does not exist",
                agent.name
            ))
        })?;
        let harness = store
            .get_harness(profile.harness_id)
            .await?
            .ok_or_else(|| {
                OrchestratorError::NotFound(format!(
                    "profile {:?} references missing harness {}",
                    profile.name, profile.harness_id
                ))
            })?;
        if !harness.enabled {
            return Err(OrchestratorError::NotFound(format!(
                "profile {:?} references disabled harness {:?}",
                profile.name, harness.name
            )));
        }
        Ok((profile, harness))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use mobius_core::*;
    use std::collections::BTreeMap;

    async fn fixture() -> (InMemoryStore, Agent, ModelProfile, ModelProfile) {
        let store = InMemoryStore::new();
        let harness = Harness {
            id: HarnessId::new(),
            name: "devin".into(),
            command: "devin".into(),
            args: vec!["acp".into()],
            env: Default::default(),
            default_permission_policy: PermissionPolicy::Auto,
            model_arg_template: vec![],
            enabled: true,
            created_at: chrono::Utc::now(),
        };
        store.insert_harness(&harness).await.expect("h");
        let default_p = ModelProfile {
            id: ModelProfileId::new(),
            name: "default".into(),
            harness_id: harness.id,
            model: None,
            effort: None,
            config: Default::default(),
            created_at: chrono::Utc::now(),
        };
        let review_p = ModelProfile {
            id: ModelProfileId::new(),
            name: "review".into(),
            harness_id: harness.id,
            model: None,
            effort: None,
            config: Default::default(),
            created_at: chrono::Utc::now(),
        };
        store.insert_model_profile(&default_p).await.expect("p1");
        store.insert_model_profile(&review_p).await.expect("p2");
        let mut overrides = BTreeMap::new();
        overrides.insert(Activity::Review, review_p.id);
        let agent = Agent {
            id: AgentId::new(),
            name: "a".into(),
            role: AgentRole::Project,
            profiles: ActivityProfiles {
                default: default_p.id,
                overrides,
            },
            project_id: None,
            repository_id: None,
            instructions: String::new(),
            permission_policy: PermissionPolicy::Auto,
            status: AgentStatus::Idle,
            created_at: chrono::Utc::now(),
        };
        (store, agent, default_p, review_p)
    }

    #[tokio::test]
    async fn override_hit_and_default_fallback() {
        let (store, agent, default_p, review_p) = fixture().await;
        let (p, _h) = ProfileResolver::resolve(&store, &agent, Activity::Review)
            .await
            .expect("review");
        assert_eq!(p.id, review_p.id);
        let (p, _h) = ProfileResolver::resolve(&store, &agent, Activity::Implement)
            .await
            .expect("impl");
        assert_eq!(p.id, default_p.id);
    }

    #[tokio::test]
    async fn dangling_profile_id_errors() {
        let (store, mut agent, _d, _r) = fixture().await;
        agent.profiles.default = ModelProfileId::new();
        let err = ProfileResolver::resolve(&store, &agent, Activity::Chat).await;
        assert!(matches!(err, Err(OrchestratorError::NotFound(_))));
    }

    #[tokio::test]
    async fn disabled_harness_errors() {
        let (store, agent, _d, _r) = fixture().await;
        let mut harness = store
            .find_harness_by_name("devin")
            .await
            .expect("h")
            .expect("some");
        harness.enabled = false;
        store.update_harness(&harness).await.expect("u");
        let err = ProfileResolver::resolve(&store, &agent, Activity::Chat).await;
        assert!(matches!(err, Err(OrchestratorError::NotFound(_))));
    }
}
