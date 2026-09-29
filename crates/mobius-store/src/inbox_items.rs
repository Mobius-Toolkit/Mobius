use std::error::Error;

use mobius_domain::{InboxItem, InboxKind};
use sqlx::sqlite::SqlitePool;
use time::OffsetDateTime;

pub struct InboxItems<'a> {
    pub(crate) pool: &'a SqlitePool,
}

struct Row {
    id: i64,
    kind: String,
    organization: String,
    repository: String,
    workstream: i64,
    issue: i64,
    text: String,
    link: String,
    time: OffsetDateTime,
    dismissed_at: Option<OffsetDateTime>,
}

impl Row {
    fn item(self) -> Result<InboxItem, Box<dyn Error + Send + Sync>> {
        let kind = InboxKind::ALL
            .into_iter()
            .find(|kind| kind.name() == self.kind)
            .ok_or_else(|| format!("unknown Inbox kind `{}`", self.kind))?;
        Ok(InboxItem {
            id: self.id,
            kind,
            organization: self.organization,
            repository: self.repository,
            workstream: self.workstream,
            issue: self.issue,
            text: self.text,
            link: self.link,
            time: self.time,
            dismissed_at: self.dismissed_at,
        })
    }
}

pub struct NewInboxItem<'a> {
    pub kind: InboxKind,
    pub organization: &'a str,
    pub repository: &'a str,
    pub workstream: i64,
    pub issue: i64,
    pub text: &'a str,
    pub link: &'a str,
}

impl InboxItems<'_> {
    pub async fn add(
        &self,
        item: NewInboxItem<'_>,
    ) -> Result<InboxItem, Box<dyn Error + Send + Sync>> {
        let kind = item.kind.name();
        let time = OffsetDateTime::now_utc();
        sqlx::query_as!(
            Row,
            r#"INSERT INTO inbox_items (kind, organization, repository, workstream, issue, text, link, time)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?)
               RETURNING id AS "id!", kind, organization, repository, workstream, issue, text, link,
                         time AS "time: OffsetDateTime",
                         dismissed_at AS "dismissed_at: OffsetDateTime""#,
            kind,
            item.organization,
            item.repository,
            item.workstream,
            item.issue,
            item.text,
            item.link,
            time
        )
        .fetch_one(self.pool)
        .await?
        .item()
    }

    pub async fn open(&self) -> Result<Vec<InboxItem>, Box<dyn Error + Send + Sync>> {
        let rows = sqlx::query_as!(
            Row,
            r#"SELECT id, kind, organization, repository, workstream, issue, text, link,
                      time AS "time: OffsetDateTime",
                      dismissed_at AS "dismissed_at: OffsetDateTime"
               FROM inbox_items WHERE dismissed_at IS NULL ORDER BY id"#
        )
        .fetch_all(self.pool)
        .await?;
        rows.into_iter().map(Row::item).collect()
    }

    pub async fn dismiss(&self, id: i64) -> Result<InboxItem, Box<dyn Error + Send + Sync>> {
        let dismissed_at = OffsetDateTime::now_utc();
        sqlx::query_as!(
            Row,
            r#"UPDATE inbox_items SET dismissed_at = ? WHERE id = ?
               RETURNING id AS "id!", kind, organization, repository, workstream, issue, text, link,
                         time AS "time: OffsetDateTime",
                         dismissed_at AS "dismissed_at: OffsetDateTime""#,
            dismissed_at,
            id
        )
        .fetch_one(self.pool)
        .await?
        .item()
    }
}
