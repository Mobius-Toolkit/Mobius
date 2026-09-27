use std::error::Error;

use mobius_domain::FeedRow;
use sqlx::sqlite::SqlitePool;
use time::OffsetDateTime;

pub struct Events<'a> {
    pub(crate) pool: &'a SqlitePool,
}

impl Events<'_> {
    pub async fn add(
        &self,
        repository: &str,
        workstream: i64,
        issue: i64,
        actor: &str,
        text: &str,
        link: &str,
    ) -> Result<FeedRow, Box<dyn Error + Send + Sync>> {
        let time = OffsetDateTime::now_utc();
        let row = sqlx::query_as!(
            FeedRow,
            r#"INSERT INTO events (time, repository, workstream, issue, actor, text, link)
               VALUES (?, ?, ?, ?, ?, ?, ?)
               RETURNING id, time AS "time: OffsetDateTime", repository, workstream, issue, actor, text, link"#,
            time,
            repository,
            workstream,
            issue,
            actor,
            text,
            link
        )
        .fetch_one(self.pool)
        .await?;
        Ok(row)
    }

    pub async fn latest(&self, limit: i64) -> Result<Vec<FeedRow>, Box<dyn Error + Send + Sync>> {
        let mut rows = sqlx::query_as!(
            FeedRow,
            r#"SELECT id, time AS "time: OffsetDateTime", repository, workstream, issue, actor, text, link
               FROM events ORDER BY id DESC LIMIT ?"#,
            limit
        )
        .fetch_all(self.pool)
        .await?;
        rows.reverse();
        Ok(rows)
    }

    pub async fn after(&self, id: i64) -> Result<Vec<FeedRow>, Box<dyn Error + Send + Sync>> {
        let rows = sqlx::query_as!(
            FeedRow,
            r#"SELECT id, time AS "time: OffsetDateTime", repository, workstream, issue, actor, text, link
               FROM events WHERE id > ? ORDER BY id"#,
            id
        )
        .fetch_all(self.pool)
        .await?;
        Ok(rows)
    }
}
