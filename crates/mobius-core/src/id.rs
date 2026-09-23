use serde::{Deserialize, Serialize};
use std::fmt;
use std::str::FromStr;
use uuid::Uuid;

macro_rules! typed_id {
    ($($name:ident),* $(,)?) => {
        $(
            #[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
            #[serde(transparent)]
            pub struct $name(Uuid);

            impl $name {
                pub fn new() -> Self {
                    Self(Uuid::new_v4())
                }

                pub fn from_uuid(uuid: Uuid) -> Self {
                    Self(uuid)
                }

                pub fn as_uuid(&self) -> Uuid {
                    self.0
                }
            }

            impl Default for $name {
                fn default() -> Self {
                    Self::new()
                }
            }

            impl fmt::Display for $name {
                fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                    self.0.fmt(f)
                }
            }

            impl FromStr for $name {
                type Err = uuid::Error;

                fn from_str(s: &str) -> Result<Self, Self::Err> {
                    Uuid::from_str(s).map(Self)
                }
            }

            impl From<Uuid> for $name {
                fn from(uuid: Uuid) -> Self {
                    Self(uuid)
                }
            }
        )*
    };
}

typed_id!(
    OrganizationId,
    RepositoryId,
    ProjectId,
    AgentId,
    TaskId,
    RunId,
    SignalId,
    MemoryEntryId,
    HarnessId,
    ModelProfileId,
    ConversationId,
    MessageId,
    PermissionRequestId,
    ResearchId,
);
