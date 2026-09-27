use serde::{Deserialize, Serialize};
use time::OffsetDateTime;

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Harness {
    ClaudeCode,
    Antigravity,
    Devin,
}

impl Harness {
    pub const ALL: [Harness; 3] = [Harness::ClaudeCode, Harness::Antigravity, Harness::Devin];

    pub fn name(self) -> &'static str {
        match self {
            Harness::ClaudeCode => "claude-code",
            Harness::Antigravity => "antigravity",
            Harness::Devin => "devin",
        }
    }
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct DeviceLogin {
    pub id: i64,
    pub user_agent: String,
    pub created_at: OffsetDateTime,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Devices {
    pub this_device: i64,
    pub logins: Vec<DeviceLogin>,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct ManifestForm {
    pub url: String,
    pub manifest: String,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Workstream {
    pub repository: String,
    pub number: i64,
    pub title: String,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct FeedRow {
    pub id: i64,
    pub time: OffsetDateTime,
    pub repository: String,
    pub workstream: i64,
    pub issue: i64,
    pub actor: String,
    pub text: String,
    pub link: String,
}
