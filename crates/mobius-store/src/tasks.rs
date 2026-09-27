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
    pub state: String,
    pub branch: Option<String>,
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
               RETURNING id AS "id!", workstream, state, branch"#,
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
            r#"SELECT id, workstream, state, branch FROM tasks
               WHERE repository = ? AND issue = ? AND state <> 'ended'"#,
            repository,
            issue
        )
        .fetch_optional(self.pool)
        .await?;
        Ok(task)
    }

    // Gives `false` when the task is not in the state `from`.
    pub async fn queue(&self, id: i64, from: &str) -> Result<bool, Box<dyn Error + Send + Sync>> {
        let queued_at = OffsetDateTime::now_utc();
        let result = sqlx::query!(
            "UPDATE tasks SET state = 'queued', queued_at = ? WHERE id = ? AND state = ?",
            queued_at,
            id,
            from
        )
        .execute(self.pool)
        .await?;
        Ok(result.rows_affected() == 1)
    }

    pub async fn queued(&self) -> Result<Vec<i64>, Box<dyn Error + Send + Sync>> {
        let ids = sqlx::query_scalar!(
            "SELECT id FROM tasks WHERE state = 'queued' ORDER BY queued_at, id"
        )
        .fetch_all(self.pool)
        .await?;
        Ok(ids)
    }

    // Gives `false` when the task is not in the state `from`.
    pub async fn set_state(
        &self,
        id: i64,
        from: &str,
        to: &str,
    ) -> Result<bool, Box<dyn Error + Send + Sync>> {
        let result = sqlx::query!(
            "UPDATE tasks SET state = ? WHERE id = ? AND state = ?",
            to,
            id,
            from
        )
        .execute(self.pool)
        .await?;
        Ok(result.rows_affected() == 1)
    }

    pub async fn set_branch(
        &self,
        id: i64,
        branch: &str,
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        sqlx::query!("UPDATE tasks SET branch = ? WHERE id = ?", branch, id)
            .execute(self.pool)
            .await?;
        Ok(())
    }

    pub async fn end(&self, id: i64) -> Result<(), Box<dyn Error + Send + Sync>> {
        sqlx::query!("UPDATE tasks SET state = 'ended' WHERE id = ?", id)
            .execute(self.pool)
            .await?;
        Ok(())
    }
}
