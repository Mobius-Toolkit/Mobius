use std::error::Error;

use mobius_domain::{LabelCheck, LabelStatus, RepositoryCheckup, organization};
use mobius_github::Repository;

use crate::{Engine, labels};

// The managed repositories of the organization, in name order.
fn managed(engine: &Engine, name: &str) -> Vec<Repository> {
    let mut repositories: Vec<Repository> = engine
        .repositories
        .read()
        .unwrap()
        .iter()
        .filter(|repository| organization(&repository.full_name) == name)
        .cloned()
        .collect();
    repositories.sort_by(|left, right| left.full_name.cmp(&right.full_name));
    repositories
}

// The status of each Mobius label in each managed repository of the organization.
pub async fn status(
    engine: &Engine,
    name: &str,
) -> Result<Vec<RepositoryCheckup>, Box<dyn Error + Send + Sync>> {
    let mut checkup = Vec::new();
    for repository in managed(engine, name) {
        let checked = labels::status(&repository)
            .await?
            .into_iter()
            .map(|entry| LabelCheck {
                name: entry.label.name.to_string(),
                color: entry.label.color.to_string(),
                status: match entry.status {
                    labels::LabelStatus::Present => LabelStatus::Present,
                    labels::LabelStatus::WrongColor(color) => LabelStatus::WrongColor(color),
                    labels::LabelStatus::WrongCase(name) => LabelStatus::WrongCase(name),
                    labels::LabelStatus::Missing => LabelStatus::Missing,
                },
            })
            .collect();
        checkup.push(RepositoryCheckup {
            repository: repository.full_name,
            labels: checked,
        });
    }
    Ok(checkup)
}

// Creates the missing Mobius labels and fixes the wrong colors in each managed repository of the organization.
pub async fn fix(engine: &Engine, name: &str) -> Result<(), Box<dyn Error + Send + Sync>> {
    for repository in managed(engine, name) {
        labels::fix(&repository).await?;
    }
    Ok(())
}
