use std::error::Error;

use mobius_domain::{Harness, Session};
use sqlx::sqlite::SqlitePool;
use time::OffsetDateTime;

pub struct Sessions<'a> {
    pub(crate) pool: &'a SqlitePool,
}

struct Row {
    id: i64,
    role: String,
    harness: String,
    model: String,
    repository: String,
    workstream: i64,
    acp_session_id: Option<String>,
    started_at: OffsetDateTime,
    ended_at: Option<OffsetDateTime>,
    end_reason: Option<String>,
    queue_reason: Option<String>,
}

impl Row {
    fn session(self) -> Result<Session, Box<dyn Error + Send + Sync>> {
        let harness = Harness::ALL
            .into_iter()
            .find(|harness| harness.name() == self.harness)
            .ok_or_else(|| format!("unknown harness `{}`", self.harness))?;
        Ok(Session {
            id: self.id,
            role: self.role,
            harness,
            model: self.model,
            repository: self.repository,
            workstream: self.workstream,
            acp_session_id: self.acp_session_id,
            started_at: self.started_at,
            ended_at: self.ended_at,
            end_reason: self.end_reason,
            queue_reason: self.queue_reason,
        })
    }
}

impl Sessions<'_> {
    pub async fn add(
        &self,
        role: &str,
        harness: Harness,
        model: &str,
        repository: &str,
        workstream: i64,
    ) -> Result<Session, Box<dyn Error + Send + Sync>> {
        let harness = harness.name();
        let started_at = OffsetDateTime::now_utc();
        sqlx::query_as!(
            Row,
            r#"INSERT INTO sessions (role, harness, model, repository, workstream, started_at)
               VALUES (?, ?, ?, ?, ?, ?)
               RETURNING id AS "id!", role, harness, model, repository, workstream, acp_session_id,
                         started_at AS "started_at: OffsetDateTime",
                         ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason"#,
            role,
            harness,
            model,
            repository,
            workstream,
            started_at
        )
        .fetch_one(self.pool)
        .await?
        .session()
    }

    pub async fn set_acp_session_id(
        &self,
        id: i64,
        acp_session_id: &str,
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        sqlx::query!(
            "UPDATE sessions SET acp_session_id = ? WHERE id = ?",
            acp_session_id,
            id
        )
        .execute(self.pool)
        .await?;
        Ok(())
    }

    pub async fn set_queue_reason(
        &self,
        id: i64,
        reason: &str,
    ) -> Result<Session, Box<dyn Error + Send + Sync>> {
        sqlx::query_as!(
            Row,
            r#"UPDATE sessions SET queue_reason = ? WHERE id = ?
               RETURNING id AS "id!", role, harness, model, repository, workstream, acp_session_id,
                         started_at AS "started_at: OffsetDateTime",
                         ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason"#,
            reason,
            id
        )
        .fetch_one(self.pool)
        .await?
        .session()
    }

    pub async fn start(&self, id: i64) -> Result<Session, Box<dyn Error + Send + Sync>> {
        let started_at = OffsetDateTime::now_utc();
        sqlx::query_as!(
            Row,
            r#"UPDATE sessions SET started_at = ?, queue_reason = NULL WHERE id = ?
               RETURNING id AS "id!", role, harness, model, repository, workstream, acp_session_id,
                         started_at AS "started_at: OffsetDateTime",
                         ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason"#,
            started_at,
            id
        )
        .fetch_one(self.pool)
        .await?
        .session()
    }

    pub async fn end(
        &self,
        id: i64,
        reason: &str,
    ) -> Result<Session, Box<dyn Error + Send + Sync>> {
        let ended_at = OffsetDateTime::now_utc();
        sqlx::query_as!(
            Row,
            r#"UPDATE sessions SET ended_at = ?, end_reason = ?, queue_reason = NULL WHERE id = ?
               RETURNING id AS "id!", role, harness, model, repository, workstream, acp_session_id,
                         started_at AS "started_at: OffsetDateTime",
                         ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason"#,
            ended_at,
            reason,
            id
        )
        .fetch_one(self.pool)
        .await?
        .session()
    }

    pub async fn with_role(
        &self,
        role: &str,
    ) -> Result<Vec<Session>, Box<dyn Error + Send + Sync>> {
        let rows = sqlx::query_as!(
            Row,
            r#"SELECT id, role, harness, model, repository, workstream, acp_session_id,
                      started_at AS "started_at: OffsetDateTime",
                      ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason
               FROM sessions WHERE role = ? ORDER BY id"#,
            role
        )
        .fetch_all(self.pool)
        .await?;
        rows.into_iter().map(Row::session).collect()
    }

    pub async fn list(
        &self,
        repository: &str,
        workstream: i64,
    ) -> Result<Vec<Session>, Box<dyn Error + Send + Sync>> {
        let rows = sqlx::query_as!(
            Row,
            r#"SELECT id, role, harness, model, repository, workstream, acp_session_id,
                      started_at AS "started_at: OffsetDateTime",
                      ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason
               FROM sessions WHERE repository = ? AND workstream = ? ORDER BY id"#,
            repository,
            workstream
        )
        .fetch_all(self.pool)
        .await?;
        rows.into_iter().map(Row::session).collect()
    }
}
