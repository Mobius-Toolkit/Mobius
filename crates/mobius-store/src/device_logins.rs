use std::error::Error;

use mobius_domain::DeviceLogin;
use sqlx::sqlite::SqlitePool;
use time::OffsetDateTime;

pub struct DeviceLogins<'a> {
    pub(crate) pool: &'a SqlitePool,
}

impl DeviceLogins<'_> {
    pub async fn add(
        &self,
        token_hash: &[u8],
        password_fingerprint: &[u8],
        user_agent: &str,
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        let created_at = OffsetDateTime::now_utc();
        sqlx::query!(
            "INSERT INTO device_logins (token_hash, password_fingerprint, user_agent, created_at)
             VALUES (?, ?, ?, ?)",
            token_hash,
            password_fingerprint,
            user_agent,
            created_at
        )
        .execute(self.pool)
        .await?;
        Ok(())
    }

    pub async fn find(
        &self,
        token_hash: &[u8],
    ) -> Result<Option<i64>, Box<dyn Error + Send + Sync>> {
        let id = sqlx::query_scalar!(
            "SELECT id FROM device_logins WHERE token_hash = ?",
            token_hash
        )
        .fetch_optional(self.pool)
        .await?;
        Ok(id)
    }

    pub async fn list(&self) -> Result<Vec<DeviceLogin>, Box<dyn Error + Send + Sync>> {
        let logins = sqlx::query_as!(
            DeviceLogin,
            r#"SELECT id, user_agent, created_at AS "created_at: OffsetDateTime"
               FROM device_logins ORDER BY id DESC"#
        )
        .fetch_all(self.pool)
        .await?;
        Ok(logins)
    }

    pub async fn delete(&self, id: i64) -> Result<(), Box<dyn Error + Send + Sync>> {
        sqlx::query!("DELETE FROM device_logins WHERE id = ?", id)
            .execute(self.pool)
            .await?;
        Ok(())
    }

    pub async fn delete_other_passwords(
        &self,
        password_fingerprint: &[u8],
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        sqlx::query!(
            "DELETE FROM device_logins WHERE password_fingerprint != ?",
            password_fingerprint
        )
        .execute(self.pool)
        .await?;
        Ok(())
    }
}
