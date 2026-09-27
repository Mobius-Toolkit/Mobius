use std::error::Error;

use sqlx::sqlite::SqlitePool;
use time::OffsetDateTime;

pub struct SyncCursors<'a> {
    pub(crate) pool: &'a SqlitePool,
}

#[derive(Default)]
pub struct SyncCursor {
    pub since: Option<OffsetDateTime>,
    pub etag: Option<String>,
}

impl SyncCursors<'_> {
    pub async fn get(
        &self,
        repository: &str,
        endpoint: &str,
    ) -> Result<SyncCursor, Box<dyn Error + Send + Sync>> {
        let cursor = sqlx::query_as!(
            SyncCursor,
            r#"SELECT since AS "since: OffsetDateTime", etag
               FROM sync_cursors WHERE repository = ? AND endpoint = ?"#,
            repository,
            endpoint
        )
        .fetch_optional(self.pool)
        .await?;
        Ok(cursor.unwrap_or_default())
    }

    pub async fn set(
        &self,
        repository: &str,
        endpoint: &str,
        since: Option<OffsetDateTime>,
        etag: Option<&str>,
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        sqlx::query!(
            "INSERT INTO sync_cursors (repository, endpoint, since, etag) VALUES (?, ?, ?, ?)
             ON CONFLICT (repository, endpoint) DO UPDATE SET since = excluded.since, etag = excluded.etag",
            repository,
            endpoint,
            since,
            etag
        )
        .execute(self.pool)
        .await?;
        Ok(())
    }
}
