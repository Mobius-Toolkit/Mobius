use std::error::Error;

use mobius_domain::TranscriptRow;
use sqlx::sqlite::SqlitePool;
use time::OffsetDateTime;

pub struct Transcript<'a> {
    pub(crate) pool: &'a SqlitePool,
}

impl Transcript<'_> {
    pub async fn add(
        &self,
        session: i64,
        kind: &str,
        json: &str,
    ) -> Result<i64, Box<dyn Error + Send + Sync>> {
        let time = OffsetDateTime::now_utc();
        let id = sqlx::query_scalar!(
            r#"INSERT INTO transcript (session, time, kind, json)
               VALUES (?, ?, ?, ?) RETURNING id AS "id!""#,
            session,
            time,
            kind,
            json
        )
        .fetch_one(self.pool)
        .await?;
        Ok(id)
    }

    pub async fn set_json(&self, id: i64, json: &str) -> Result<(), Box<dyn Error + Send + Sync>> {
        sqlx::query!("UPDATE transcript SET json = ? WHERE id = ?", json, id)
            .execute(self.pool)
            .await?;
        Ok(())
    }

    pub async fn list(
        &self,
        session: i64,
    ) -> Result<Vec<TranscriptRow>, Box<dyn Error + Send + Sync>> {
        let rows = sqlx::query_as!(
            TranscriptRow,
            r#"SELECT id, session, time AS "time: OffsetDateTime", kind, json
               FROM transcript WHERE session = ? ORDER BY id"#,
            session
        )
        .fetch_all(self.pool)
        .await?;
        Ok(rows)
    }
}
