use std::error::Error;

use sqlx::sqlite::SqlitePool;

pub struct WorkstreamCopy<'a> {
    pub(crate) pool: &'a SqlitePool,
}

pub struct CopiedWorkstream {
    pub number: i64,
    pub title: String,
    pub body: String,
    pub autopilot: bool,
    // The issues of the tree in depth-first order.
    pub issues: Vec<CopiedIssue>,
}

pub struct CopiedIssue {
    pub number: i64,
    pub parent: i64,
    pub title: String,
    pub body: String,
    pub state: String,
    pub labels: Vec<String>,
    pub author: String,
    pub html_url: String,
    pub repository_url: String,
    pub blockers: Vec<CopiedBlocker>,
}

pub struct CopiedBlocker {
    pub number: i64,
    pub workstream: Option<i64>,
    pub workstream_title: Option<String>,
}

impl WorkstreamCopy<'_> {
    pub async fn replace(
        &self,
        repository: &str,
        workstreams: &[CopiedWorkstream],
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        let mut transaction = self.pool.begin().await?;
        sqlx::query!(
            "DELETE FROM copied_workstreams WHERE repository = ?",
            repository
        )
        .execute(&mut *transaction)
        .await?;
        sqlx::query!("DELETE FROM copied_issues WHERE repository = ?", repository)
            .execute(&mut *transaction)
            .await?;
        sqlx::query!(
            "DELETE FROM copied_issue_labels WHERE repository = ?",
            repository
        )
        .execute(&mut *transaction)
        .await?;
        sqlx::query!(
            "DELETE FROM copied_blockers WHERE repository = ?",
            repository
        )
        .execute(&mut *transaction)
        .await?;
        for workstream in workstreams {
            sqlx::query!(
                "INSERT INTO copied_workstreams (repository, number, title, body, autopilot)
                 VALUES (?, ?, ?, ?, ?)",
                repository,
                workstream.number,
                workstream.title,
                workstream.body,
                workstream.autopilot
            )
            .execute(&mut *transaction)
            .await?;
            for (position, issue) in (0_i64..).zip(&workstream.issues) {
                sqlx::query!(
                    "INSERT INTO copied_issues (repository, workstream, position, number, parent, title, body,
                                               state, author, html_url, repository_url)
                     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
                    repository,
                    workstream.number,
                    position,
                    issue.number,
                    issue.parent,
                    issue.title,
                    issue.body,
                    issue.state,
                    issue.author,
                    issue.html_url,
                    issue.repository_url
                )
                .execute(&mut *transaction)
                .await?;
                for label in &issue.labels {
                    sqlx::query!(
                        "INSERT INTO copied_issue_labels (repository, workstream, position, name)
                         VALUES (?, ?, ?, ?)",
                        repository,
                        workstream.number,
                        position,
                        label
                    )
                    .execute(&mut *transaction)
                    .await?;
                }
                for blocker in &issue.blockers {
                    sqlx::query!(
                        "INSERT INTO copied_blockers (repository, workstream, position, number,
                                                      blocker_workstream, blocker_workstream_title)
                         VALUES (?, ?, ?, ?, ?, ?)",
                        repository,
                        workstream.number,
                        position,
                        blocker.number,
                        blocker.workstream,
                        blocker.workstream_title
                    )
                    .execute(&mut *transaction)
                    .await?;
                }
            }
        }
        transaction.commit().await?;
        Ok(())
    }
}
