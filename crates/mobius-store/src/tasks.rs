use std::error::Error;

use sqlx::sqlite::SqlitePool;
use time::OffsetDateTime;

pub struct Tasks<'a> {
    pub(crate) pool: &'a SqlitePool,
}

#[derive(Debug, PartialEq)]
pub struct Task {
    pub id: i64,
    pub workstream: i64,
}

impl Tasks<'_> {
    pub async fn add(
        &self,
        repository: &str,
        issue: i64,
        workstream: i64,
    ) -> Result<Task, Box<dyn Error + Send + Sync>> {
        let dispatched_at = OffsetDateTime::now_utc();
        let task = sqlx::query_as!(
            Task,
            r#"INSERT INTO tasks (repository, issue, workstream, state, dispatched_at)
               VALUES (?, ?, ?, 'dispatched', ?)
               RETURNING id AS "id!", workstream"#,
            repository,
            issue,
            workstream,
            dispatched_at
        )
        .fetch_one(self.pool)
        .await?;
        Ok(task)
    }

    pub async fn live(
        &self,
        repository: &str,
        issue: i64,
    ) -> Result<Option<Task>, Box<dyn Error + Send + Sync>> {
        let task = sqlx::query_as!(
            Task,
            r#"SELECT id, workstream FROM tasks
               WHERE repository = ? AND issue = ? AND state <> 'ended'"#,
            repository,
            issue
        )
        .fetch_optional(self.pool)
        .await?;
        Ok(task)
    }

    pub async fn end(&self, id: i64) -> Result<(), Box<dyn Error + Send + Sync>> {
        sqlx::query!("UPDATE tasks SET state = 'ended' WHERE id = ?", id)
            .execute(self.pool)
            .await?;
        Ok(())
    }
}
