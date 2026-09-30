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
    organization: String,
    repository: String,
    workstream: i64,
    acp_session_id: Option<String>,
    started_at: OffsetDateTime,
    ended_at: Option<OffsetDateTime>,
    end_reason: Option<String>,
    queue_reason: Option<String>,
    issue: Option<i64>,
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
            organization: self.organization,
            repository: self.repository,
            workstream: self.workstream,
            acp_session_id: self.acp_session_id,
            started_at: self.started_at,
            ended_at: self.ended_at,
            end_reason: self.end_reason,
            queue_reason: self.queue_reason,
            issue: self.issue,
        })
    }
}

pub struct NewSession<'a> {
    pub role: &'a str,
    pub harness: Harness,
    pub model: &'a str,
    pub organization: &'a str,
    pub repository: &'a str,
    pub workstream: i64,
    pub issue: Option<i64>,
}

impl Sessions<'_> {
    pub async fn add(
        &self,
        session: NewSession<'_>,
    ) -> Result<Session, Box<dyn Error + Send + Sync>> {
        let harness = session.harness.name();
        let started_at = OffsetDateTime::now_utc();
        sqlx::query_as!(
            Row,
            r#"INSERT INTO sessions (role, harness, model, organization, repository, workstream, issue, started_at)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?)
               RETURNING id AS "id!", role, harness, model, organization, repository, workstream, acp_session_id,
                         started_at AS "started_at: OffsetDateTime",
                         ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason, issue"#,
            session.role,
            harness,
            session.model,
            session.organization,
            session.repository,
            session.workstream,
            session.issue,
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
               RETURNING id AS "id!", role, harness, model, organization, repository, workstream, acp_session_id,
                         started_at AS "started_at: OffsetDateTime",
                         ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason, issue"#,
            reason,
            id
        )
        .fetch_one(self.pool)
        .await?
        .session()
    }

    pub async fn clear_queue_reason(
        &self,
        id: i64,
    ) -> Result<Session, Box<dyn Error + Send + Sync>> {
        sqlx::query_as!(
            Row,
            r#"UPDATE sessions SET queue_reason = NULL WHERE id = ?
               RETURNING id AS "id!", role, harness, model, organization, repository, workstream, acp_session_id,
                         started_at AS "started_at: OffsetDateTime",
                         ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason, issue"#,
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
               RETURNING id AS "id!", role, harness, model, organization, repository, workstream, acp_session_id,
                         started_at AS "started_at: OffsetDateTime",
                         ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason, issue"#,
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
               RETURNING id AS "id!", role, harness, model, organization, repository, workstream, acp_session_id,
                         started_at AS "started_at: OffsetDateTime",
                         ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason, issue"#,
            ended_at,
            reason,
            id
        )
        .fetch_one(self.pool)
        .await?
        .session()
    }

    pub async fn open_ids(&self) -> Result<Vec<i64>, Box<dyn Error + Send + Sync>> {
        let ids = sqlx::query_scalar!("SELECT id FROM sessions WHERE ended_at IS NULL")
            .fetch_all(self.pool)
            .await?;
        Ok(ids)
    }

    // All open sessions of all organizations, for the "Agents" page.
    pub async fn open(&self) -> Result<Vec<Session>, Box<dyn Error + Send + Sync>> {
        let rows = sqlx::query_as!(
            Row,
            r#"SELECT id, role, harness, model, organization, repository, workstream, acp_session_id,
                      started_at AS "started_at: OffsetDateTime",
                      ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason, issue
               FROM sessions WHERE ended_at IS NULL ORDER BY id"#,
        )
        .fetch_all(self.pool)
        .await?;
        rows.into_iter().map(Row::session).collect()
    }

    pub async fn with_role(
        &self,
        role: &str,
    ) -> Result<Vec<Session>, Box<dyn Error + Send + Sync>> {
        let rows = sqlx::query_as!(
            Row,
            r#"SELECT id, role, harness, model, organization, repository, workstream, acp_session_id,
                      started_at AS "started_at: OffsetDateTime",
                      ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason, issue
               FROM sessions WHERE role = ? ORDER BY id"#,
            role
        )
        .fetch_all(self.pool)
        .await?;
        rows.into_iter().map(Row::session).collect()
    }

    pub async fn list(
        &self,
        organization: &str,
        repository: &str,
        workstream: i64,
    ) -> Result<Vec<Session>, Box<dyn Error + Send + Sync>> {
        let rows = sqlx::query_as!(
            Row,
            r#"SELECT id, role, harness, model, organization, repository, workstream, acp_session_id,
                      started_at AS "started_at: OffsetDateTime",
                      ended_at AS "ended_at: OffsetDateTime", end_reason, queue_reason, issue
               FROM sessions WHERE organization = ? AND repository = ? AND workstream = ? ORDER BY id"#,
            organization,
            repository,
            workstream
        )
        .fetch_all(self.pool)
        .await?;
        rows.into_iter().map(Row::session).collect()
    }
}
