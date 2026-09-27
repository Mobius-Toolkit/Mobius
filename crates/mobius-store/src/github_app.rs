use std::error::Error;

use sqlx::sqlite::SqlitePool;
use time::OffsetDateTime;

pub struct GitHubApp<'a> {
    pub(crate) pool: &'a SqlitePool,
}

pub struct GitHubAppRow {
    pub app_id: i64,
    pub slug: String,
    pub private_key: String,
    pub client_id: String,
    pub client_secret: String,
    pub user_token: Option<String>,
    pub refresh_token: Option<String>,
    pub user_token_expires_at: Option<OffsetDateTime>,
}

impl GitHubApp<'_> {
    pub async fn add(
        &self,
        app_id: i64,
        slug: &str,
        private_key: &str,
        client_id: &str,
        client_secret: &str,
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        sqlx::query!(
            "INSERT INTO github_app (id, app_id, slug, private_key, client_id, client_secret)
             VALUES (1, ?, ?, ?, ?, ?)",
            app_id,
            slug,
            private_key,
            client_id,
            client_secret
        )
        .execute(self.pool)
        .await?;
        Ok(())
    }

    pub async fn get(&self) -> Result<Option<GitHubAppRow>, Box<dyn Error + Send + Sync>> {
        let row = sqlx::query_as!(
            GitHubAppRow,
            r#"SELECT app_id, slug, private_key, client_id, client_secret, user_token, refresh_token,
                      user_token_expires_at AS "user_token_expires_at: OffsetDateTime"
               FROM github_app"#
        )
        .fetch_optional(self.pool)
        .await?;
        Ok(row)
    }

    pub async fn set_user_tokens(
        &self,
        user_token: &str,
        refresh_token: &str,
        user_token_expires_at: OffsetDateTime,
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        sqlx::query!(
            "UPDATE github_app SET user_token = ?, refresh_token = ?, user_token_expires_at = ?",
            user_token,
            refresh_token,
            user_token_expires_at
        )
        .execute(self.pool)
        .await?;
        Ok(())
    }
}
