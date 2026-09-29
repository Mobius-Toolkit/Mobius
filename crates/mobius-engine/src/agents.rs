use std::error::Error;

use mobius_domain::{AgentNode, Session, organization};

use crate::{Engine, chat, lead_events, triager};

pub(crate) fn node(session: Session) -> AgentNode {
    let (role, title) = match session.role.as_str() {
        chat::ROLE => ("Lead".to_string(), "chat session".to_string()),
        lead_events::ROLE => ("Lead".to_string(), "event session".to_string()),
        triager::ROLE if session.repository.is_empty() => {
            ("Triager".to_string(), "chat session".to_string())
        }
        triager::ROLE => ("Triager".to_string(), session.repository.clone()),
        role => (role.to_string(), String::new()),
    };
    AgentNode {
        session,
        role,
        title,
    }
}

pub async fn triager_tree(engine: &Engine) -> Result<Vec<AgentNode>, Box<dyn Error + Send + Sync>> {
    let sessions = engine.store.sessions().with_role(triager::ROLE).await?;
    Ok(sessions.into_iter().map(node).collect())
}

pub async fn tree(
    engine: &Engine,
    repository: &str,
    workstream: i64,
) -> Result<Vec<AgentNode>, Box<dyn Error + Send + Sync>> {
    let sessions = engine
        .store
        .sessions()
        .list(organization(repository), repository, workstream)
        .await?;
    Ok(sessions.into_iter().map(node).collect())
}
