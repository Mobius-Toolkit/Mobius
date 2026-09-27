use std::error::Error;

use mobius_domain::{AgentNode, Session};

use crate::{Engine, chat};

pub(crate) fn node(session: Session) -> AgentNode {
    let (role, title) = match session.role.as_str() {
        chat::ROLE => ("Lead".to_string(), "chat session".to_string()),
        role => (role.to_string(), String::new()),
    };
    AgentNode {
        session,
        role,
        title,
    }
}

pub async fn tree(
    engine: &Engine,
    repository: &str,
    workstream: i64,
) -> Result<Vec<AgentNode>, Box<dyn Error + Send + Sync>> {
    let sessions = engine.store.sessions().list(repository, workstream).await?;
    Ok(sessions.into_iter().map(node).collect())
}
