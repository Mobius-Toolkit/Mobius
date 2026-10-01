use std::error::Error;

use mobius_domain::{
    CheckupView, LabelCheck, LabelStatus, PermissionCheck, PermissionStatus, RepositoryCheckup,
    organization,
};
use mobius_github::{REQUIRED_PERMISSIONS, Repository, grants};

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

// The status of the App permissions, and of each Mobius label in each managed repository of the organization.
pub async fn status(
    engine: &Engine,
    name: &str,
) -> Result<CheckupView, Box<dyn Error + Send + Sync>> {
    let repositories = managed(engine, name);
    let permissions = match repositories.first() {
        Some(first) => permissions(engine, first, name).await?,
        None => Vec::new(),
    };
    let mut checkup = Vec::new();
    for repository in repositories {
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
    Ok(CheckupView {
        repositories: checkup,
        permissions,
    })
}

async fn permissions(
    engine: &Engine,
    repository: &Repository,
    name: &str,
) -> Result<Vec<PermissionCheck>, Box<dyn Error + Send + Sync>> {
    let app = engine
        .store
        .github_apps()
        .get(repository.app_id)
        .await?
        .ok_or("The Mobius App does not exist.")?;
    let access = engine
        .github
        .app_access(app.app_id, &app.slug, &app.private_key, name)
        .await?;
    Ok(REQUIRED_PERMISSIONS
        .iter()
        .map(|(permission, level)| PermissionCheck {
            name: permission.to_string(),
            level: level.to_string(),
            status: if grants(&access.installation_permissions, permission, level) {
                PermissionStatus::Present
            } else if grants(&access.app_permissions, permission, level) {
                PermissionStatus::NotAccepted(access.installation_url.clone())
            } else {
                PermissionStatus::Missing(access.app_permissions_url.clone())
            },
        })
        .collect())
}

// Creates the missing Mobius labels and fixes the wrong colors in each managed repository of the organization.
pub async fn fix(engine: &Engine, name: &str) -> Result<(), Box<dyn Error + Send + Sync>> {
    for repository in managed(engine, name) {
        labels::fix(&repository).await?;
    }
    Ok(())
}
