use std::error::Error;

use mobius_domain::{Author, ChatMessage, Unread};
use sqlx::sqlite::SqlitePool;
use time::OffsetDateTime;

pub struct ChatMessages<'a> {
    pub(crate) pool: &'a SqlitePool,
}

struct Row {
    id: i64,
    repository: String,
    workstream: i64,
    author: String,
    time: OffsetDateTime,
    text: String,
}

impl Row {
    fn message(self) -> Result<ChatMessage, Box<dyn Error + Send + Sync>> {
        let author = Author::ALL
            .into_iter()
            .find(|author| author.name() == self.author)
            .ok_or_else(|| format!("unknown chat author `{}`", self.author))?;
        Ok(ChatMessage {
            id: self.id,
            repository: self.repository,
            workstream: self.workstream,
            author,
            time: self.time,
            text: self.text,
        })
    }
}

impl ChatMessages<'_> {
    pub async fn add(
        &self,
        repository: &str,
        workstream: i64,
        author: Author,
        text: &str,
    ) -> Result<ChatMessage, Box<dyn Error + Send + Sync>> {
        let author = author.name();
        let time = OffsetDateTime::now_utc();
        let row = sqlx::query_as!(
            Row,
            r#"INSERT INTO chat_messages (repository, workstream, author, time, text)
               VALUES (?, ?, ?, ?, ?)
               RETURNING id, repository, workstream, author, time AS "time: OffsetDateTime", text"#,
            repository,
            workstream,
            author,
            time,
            text
        )
        .fetch_one(self.pool)
        .await?;
        row.message()
    }

    pub async fn append(
        &self,
        id: i64,
        text: &str,
    ) -> Result<ChatMessage, Box<dyn Error + Send + Sync>> {
        let row = sqlx::query_as!(
            Row,
            r#"UPDATE chat_messages SET text = text || ? WHERE id = ?
               RETURNING id, repository, workstream, author, time AS "time: OffsetDateTime", text"#,
            text,
            id
        )
        .fetch_one(self.pool)
        .await?;
        row.message()
    }

    pub async fn list(
        &self,
        repository: &str,
        workstream: i64,
    ) -> Result<Vec<ChatMessage>, Box<dyn Error + Send + Sync>> {
        let rows = sqlx::query_as!(
            Row,
            r#"SELECT id, repository, workstream, author, time AS "time: OffsetDateTime", text
               FROM chat_messages WHERE repository = ? AND workstream = ? ORDER BY id"#,
            repository,
            workstream
        )
        .fetch_all(self.pool)
        .await?;
        rows.into_iter().map(Row::message).collect()
    }

    pub async fn before(
        &self,
        repository: &str,
        workstream: i64,
        id: i64,
        limit: i64,
    ) -> Result<Vec<ChatMessage>, Box<dyn Error + Send + Sync>> {
        let rows = sqlx::query_as!(
            Row,
            r#"SELECT id, repository, workstream, author, time AS "time: OffsetDateTime", text
               FROM chat_messages WHERE repository = ? AND workstream = ? AND id < ?
               ORDER BY id DESC LIMIT ?"#,
            repository,
            workstream,
            id,
            limit
        )
        .fetch_all(self.pool)
        .await?;
        rows.into_iter().rev().map(Row::message).collect()
    }

    pub async fn after(
        &self,
        repository: &str,
        workstream: i64,
        author: Author,
        id: i64,
    ) -> Result<Vec<ChatMessage>, Box<dyn Error + Send + Sync>> {
        let author = author.name();
        let rows = sqlx::query_as!(
            Row,
            r#"SELECT id, repository, workstream, author, time AS "time: OffsetDateTime", text
               FROM chat_messages WHERE repository = ? AND workstream = ? AND author = ? AND id > ?
               ORDER BY id"#,
            repository,
            workstream,
            author,
            id
        )
        .fetch_all(self.pool)
        .await?;
        rows.into_iter().map(Row::message).collect()
    }

    pub async fn set_seen(
        &self,
        repository: &str,
        workstream: i64,
        message: i64,
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        sqlx::query!(
            "INSERT INTO chat_seen (repository, workstream, message) VALUES (?, ?, ?)
             ON CONFLICT (repository, workstream) DO UPDATE SET message = max(message, excluded.message)",
            repository,
            workstream,
            message
        )
        .execute(self.pool)
        .await?;
        Ok(())
    }

    pub async fn unread(&self) -> Result<Vec<Unread>, Box<dyn Error + Send + Sync>> {
        let unread = sqlx::query_as!(
            Unread,
            r#"SELECT chat_messages.repository, chat_messages.workstream, count(*) AS "count!: i64"
               FROM chat_messages
               LEFT JOIN chat_seen ON chat_seen.repository = chat_messages.repository
                                  AND chat_seen.workstream = chat_messages.workstream
               WHERE chat_messages.author <> 'Owner' AND chat_messages.id > coalesce(chat_seen.message, 0)
               GROUP BY chat_messages.repository, chat_messages.workstream"#
        )
        .fetch_all(self.pool)
        .await?;
        Ok(unread)
    }

    pub async fn unread_of(
        &self,
        repository: &str,
        workstream: i64,
    ) -> Result<Unread, Box<dyn Error + Send + Sync>> {
        let count = sqlx::query_scalar!(
            r#"SELECT count(*) AS "count!: i64" FROM chat_messages
               WHERE repository = ? AND workstream = ? AND author <> 'Owner'
                 AND id > coalesce((SELECT message FROM chat_seen WHERE repository = ? AND workstream = ?), 0)"#,
            repository,
            workstream,
            repository,
            workstream
        )
        .fetch_one(self.pool)
        .await?;
        Ok(Unread {
            repository: repository.to_string(),
            workstream,
            count,
        })
    }
}
