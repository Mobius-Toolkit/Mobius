mod device_logins;
mod events;
mod github_app;
mod sync_cursors;

use std::error::Error;
use std::fs;
use std::path::Path;

use sqlx::sqlite::{SqliteConnectOptions, SqliteJournalMode, SqlitePool};

pub use device_logins::DeviceLogins;
pub use events::Events;
pub use github_app::{GitHubApp, GitHubAppRow};
pub use sync_cursors::{SyncCursor, SyncCursors};

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

    pub fn events(&self) -> Events<'_> {
        Events { pool: &self.pool }
    }

    pub fn github_app(&self) -> GitHubApp<'_> {
        GitHubApp { pool: &self.pool }
    }

    pub fn sync_cursors(&self) -> SyncCursors<'_> {
        SyncCursors { pool: &self.pool }
    }
}
