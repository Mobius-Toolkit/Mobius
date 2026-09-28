use std::error::Error;

use mobius_domain::Harness;
use sqlx::sqlite::SqlitePool;
use time::OffsetDateTime;

pub struct HarnessPauses<'a> {
    pub(crate) pool: &'a SqlitePool,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Pause {
    pub harness: Harness,
    pub until: OffsetDateTime,
    pub inbox_item: i64,
}

struct Row {
    harness: String,
    paused_until: OffsetDateTime,
    inbox_item: i64,
}

impl Row {
    fn pause(self) -> Result<Pause, Box<dyn Error + Send + Sync>> {
        let harness = Harness::ALL
            .into_iter()
            .find(|harness| harness.name() == self.harness)
            .ok_or_else(|| format!("unknown harness `{}`", self.harness))?;
        Ok(Pause {
            harness,
            until: self.paused_until,
            inbox_item: self.inbox_item,
        })
    }
}

impl HarnessPauses<'_> {
    pub async fn get(
        &self,
        harness: Harness,
    ) -> Result<Option<Pause>, Box<dyn Error + Send + Sync>> {
        let name = harness.name();
        let row = sqlx::query_as!(
            Row,
            r#"SELECT harness AS "harness!", paused_until AS "paused_until: OffsetDateTime", inbox_item
               FROM harness_pauses WHERE harness = ?"#,
            name
        )
        .fetch_optional(self.pool)
        .await?;
        row.map(Row::pause).transpose()
    }

    pub async fn list(&self) -> Result<Vec<Pause>, Box<dyn Error + Send + Sync>> {
        let rows = sqlx::query_as!(
            Row,
            r#"SELECT harness AS "harness!", paused_until AS "paused_until: OffsetDateTime", inbox_item
               FROM harness_pauses"#
        )
        .fetch_all(self.pool)
        .await?;
        rows.into_iter().map(Row::pause).collect()
    }

    pub async fn set(&self, pause: &Pause) -> Result<(), Box<dyn Error + Send + Sync>> {
        let name = pause.harness.name();
        sqlx::query!(
            "INSERT INTO harness_pauses (harness, paused_until, inbox_item) VALUES (?, ?, ?)
             ON CONFLICT (harness) DO UPDATE SET paused_until = excluded.paused_until,
                                                 inbox_item = excluded.inbox_item",
            name,
            pause.until,
            pause.inbox_item
        )
        .execute(self.pool)
        .await?;
        Ok(())
    }

    pub async fn remove(&self, harness: Harness) -> Result<(), Box<dyn Error + Send + Sync>> {
        let name = harness.name();
        sqlx::query!("DELETE FROM harness_pauses WHERE harness = ?", name)
            .execute(self.pool)
            .await?;
        Ok(())
    }
}
