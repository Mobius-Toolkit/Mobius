mod device_logins;
mod github_app;

use std::error::Error;
use std::fs;
use std::path::Path;

use sqlx::sqlite::{SqliteConnectOptions, SqliteJournalMode, SqlitePool};

pub use device_logins::DeviceLogins;
pub use github_app::{GitHubApp, GitHubAppRow};

#[derive(Clone)]
pub struct Store {
    pub pool: SqlitePool,
}

impl Store {
    pub async fn open(data_dir: &Path) -> Result<Store, Box<dyn Error + Send + Sync>> {
        fs::create_dir_all(data_dir)?;
        let options = SqliteConnectOptions::new()
            .filename(data_dir.join("mobius.db"))
            .create_if_missing(true)
            .journal_mode(SqliteJournalMode::Wal);
        let pool = SqlitePool::connect_with(options).await?;
        sqlx::migrate!().run(&pool).await?;
        Ok(Store { pool })
    }

    pub fn device_logins(&self) -> DeviceLogins<'_> {
        DeviceLogins { pool: &self.pool }
    }

    pub fn github_app(&self) -> GitHubApp<'_> {
        GitHubApp { pool: &self.pool }
    }
}
