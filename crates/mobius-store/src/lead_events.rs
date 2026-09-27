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
