use std::error::Error;

use mobius_domain::Workstream;

use crate::{Engine, WORKSTREAM_LABEL};

const AUTOPILOT_LABEL: &str = "mobius:autopilot";

pub async fn list(engine: &Engine) -> Result<Vec<Workstream>, Box<dyn Error + Send + Sync>> {
    let repositories = engine.repositories.read().unwrap().clone();
    let mut workstreams = Vec::new();
    for repository in repositories {
        for issue in repository.open_issues_with_label(WORKSTREAM_LABEL).await? {
            workstreams.push(Workstream {
                repository: repository.full_name.clone(),
                number: issue.number,
                autopilot: issue.has_label(AUTOPILOT_LABEL),
                title: issue.title,
            });
        }
    }
    Ok(workstreams)
}
