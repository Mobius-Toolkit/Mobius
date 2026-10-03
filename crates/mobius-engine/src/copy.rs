use std::error::Error;

use mobius_github::{Issue, Repository};
use mobius_store::{CopiedBlocker, CopiedIssue, CopiedWorkstream};

use crate::labels::WORKSTREAM_LABEL;
use crate::{Engine, ends, tasks, workstreams};

pub(crate) async fn sync(
    engine: &Engine,
    repository: &Repository,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let mut workstreams = Vec::new();
    for workstream in repository.open_issues_with_label(WORKSTREAM_LABEL).await? {
        workstreams.push(CopiedWorkstream {
            number: workstream.number,
            autopilot: workstreams::issue_autopilot(engine, repository, &workstream).await?,
            issues: tree(repository, workstream.number).await?,
            title: workstream.title,
            body: workstream.body.unwrap_or_default(),
        });
    }
    engine
        .store
        .workstream_copy()
        .replace(&repository.full_name, &workstreams)
        .await
}

// The walk follows the rules of `tasks::list`, but it keeps each issue.
// A nested Workstream and an issue of another repository are leaves.
async fn tree(
    repository: &Repository,
    workstream: i64,
) -> Result<Vec<CopiedIssue>, Box<dyn Error + Send + Sync>> {
    let mut issues = Vec::new();
    let mut frames = vec![(
        workstream,
        repository.sub_issues(workstream).await?.into_iter(),
    )];
    while let Some((parent, issue)) = tasks::next_issue(&mut frames) {
        let leaf = issue.has_label(WORKSTREAM_LABEL)
            || ends::in_other_repository(&issue, &repository.full_name);
        let mut blockers = Vec::new();
        if !leaf {
            if issue.issue_dependencies_summary.blocked_by > 0 {
                blockers = blockers_of(repository, issue.number).await?;
            }
            frames.push((
                issue.number,
                repository.sub_issues(issue.number).await?.into_iter(),
            ));
        }
        issues.push(copied_issue(issue, parent, blockers));
    }
    Ok(issues)
}

async fn blockers_of(
    repository: &Repository,
    number: i64,
) -> Result<Vec<CopiedBlocker>, Box<dyn Error + Send + Sync>> {
    let mut blockers = Vec::new();
    for blocker in repository.blocked_by(number).await? {
        if blocker.state != "open" || ends::in_other_repository(&blocker, &repository.full_name) {
            continue;
        }
        let workstream = workstreams::workstream_of(repository, blocker.number).await?;
        let workstream_title = match workstream {
            Some(workstream) => repository.issue(workstream).await?.map(|issue| issue.title),
            None => None,
        };
        blockers.push(CopiedBlocker {
            number: blocker.number,
            workstream,
            workstream_title,
        });
    }
    Ok(blockers)
}

fn copied_issue(issue: Issue, parent: i64, blockers: Vec<CopiedBlocker>) -> CopiedIssue {
    CopiedIssue {
        number: issue.number,
        parent,
        title: issue.title,
        body: issue.body.unwrap_or_default(),
        state: issue.state,
        labels: issue.labels.into_iter().map(|label| label.name).collect(),
        author: issue.user.login,
        html_url: issue.html_url,
        repository_url: issue.repository_url,
        blockers,
    }
}
