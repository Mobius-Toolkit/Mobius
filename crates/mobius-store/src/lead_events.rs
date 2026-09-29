use std::error::Error;

use sqlx::sqlite::SqlitePool;
use time::OffsetDateTime;

pub struct LeadEvents<'a> {
    pub(crate) pool: &'a SqlitePool,
}

#[derive(Debug, PartialEq)]
pub struct LeadEvent {
    pub id: i64,
    pub payload: String,
}

impl LeadEvents<'_> {
    pub async fn add(
        &self,
        repository: &str,
        workstream: i64,
        kind: &str,
        payload: &str,
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        let time = OffsetDateTime::now_utc();
        sqlx::query!(
            "INSERT INTO lead_events (repository, workstream, kind, payload, time) VALUES (?, ?, ?, ?, ?)",
            repository,
            workstream,
            kind,
            payload,
            time
        )
        .execute(self.pool)
        .await?;
        Ok(())
    }

    // Gives the oldest event that no turn delivered.
    pub async fn next(
        &self,
        repository: &str,
        workstream: i64,
    ) -> Result<Option<LeadEvent>, Box<dyn Error + Send + Sync>> {
        let event = sqlx::query_as!(
            LeadEvent,
            r#"SELECT id, payload FROM lead_events
               WHERE repository = ? AND workstream = ? AND delivered_at IS NULL
               ORDER BY id LIMIT 1"#,
            repository,
            workstream
        )
        .fetch_optional(self.pool)
        .await?;
        Ok(event)
    }

    pub async fn waiting_workstreams(
        &self,
        repository: &str,
    ) -> Result<Vec<i64>, Box<dyn Error + Send + Sync>> {
        let workstreams = sqlx::query_scalar!(
            "SELECT DISTINCT workstream FROM lead_events
             WHERE repository = ? AND delivered_at IS NULL",
            repository
        )
        .fetch_all(self.pool)
        .await?;
        Ok(workstreams)
    }

    // Each Workstream with an undelivered event, with its repository.
    pub async fn waiting(&self) -> Result<Vec<(String, i64)>, Box<dyn Error + Send + Sync>> {
        let rows = sqlx::query!(
            "SELECT DISTINCT repository, workstream FROM lead_events
             WHERE delivered_at IS NULL"
        )
        .fetch_all(self.pool)
        .await?;
        Ok(rows
            .into_iter()
            .map(|row| (row.repository, row.workstream))
            .collect())
    }

    pub async fn undelivered(
        &self,
        repository: &str,
        workstream: i64,
    ) -> Result<Vec<LeadEvent>, Box<dyn Error + Send + Sync>> {
        let events = sqlx::query_as!(
            LeadEvent,
            r#"SELECT id, payload FROM lead_events
               WHERE repository = ? AND workstream = ? AND delivered_at IS NULL
               ORDER BY id"#,
            repository,
            workstream
        )
        .fetch_all(self.pool)
        .await?;
        Ok(events)
    }

    pub async fn deliver_all(
        &self,
        repository: &str,
        workstream: i64,
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        let delivered_at = OffsetDateTime::now_utc();
        sqlx::query!(
            "UPDATE lead_events SET delivered_at = ?
             WHERE repository = ? AND workstream = ? AND delivered_at IS NULL",
            delivered_at,
            repository,
            workstream
        )
        .execute(self.pool)
        .await?;
        Ok(())
    }

    pub async fn deliver(&self, id: i64) -> Result<(), Box<dyn Error + Send + Sync>> {
        let delivered_at = OffsetDateTime::now_utc();
        sqlx::query!(
            "UPDATE lead_events SET delivered_at = ? WHERE id = ?",
            delivered_at,
            id
        )
        .execute(self.pool)
        .await?;
        Ok(())
    }
}
