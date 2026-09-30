use std::error::Error;

use mobius_github::Repository;

pub const WORKSTREAM_LABEL: &str = "mobius:workstream";
pub const AUTOPILOT_LABEL: &str = "mobius:autopilot";
pub const READY_LABEL: &str = "mobius:ready";
pub const WORKING_LABEL: &str = "mobius:working";
pub const NEEDS_HUMAN_LABEL: &str = "mobius:needs-human";
pub const NO_WORKSTREAM_LABEL: &str = "mobius:no-workstream";

pub struct MobiusLabel {
    pub name: &'static str,
    pub color: &'static str,
    pub description: &'static str,
}

pub const MOBIUS_LABELS: [MobiusLabel; 6] = [
    MobiusLabel {
        name: WORKSTREAM_LABEL,
        color: "5319E7",
        description: "Mobius Workstream: a parent issue for a group of tasks",
    },
    MobiusLabel {
        name: AUTOPILOT_LABEL,
        color: "1D76DB",
        description: "Mobius dispatches the ready tasks of this Workstream",
    },
    MobiusLabel {
        name: READY_LABEL,
        color: "0E8A16",
        description: "Mobius can dispatch this task",
    },
    MobiusLabel {
        name: WORKING_LABEL,
        color: "FBCA04",
        description: "A Mobius agent works on this task",
    },
    MobiusLabel {
        name: NEEDS_HUMAN_LABEL,
        color: "D93F0B",
        description: "Mobius waits for an answer from a human",
    },
    MobiusLabel {
        name: NO_WORKSTREAM_LABEL,
        color: "BFD4F2",
        description: "The Triager found no Workstream for this issue",
    },
];

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum LabelStatus {
    Present,
    // The label exists with this different color.
    WrongColor(String),
    Missing,
}

pub struct RepositoryLabel {
    pub label: &'static MobiusLabel,
    pub status: LabelStatus,
}

// The status of each Mobius label in the repository, in the order of `MOBIUS_LABELS`.
pub async fn status(
    repository: &Repository,
) -> Result<Vec<RepositoryLabel>, Box<dyn Error + Send + Sync>> {
    let labels = repository.labels().await?;
    Ok(MOBIUS_LABELS
        .iter()
        .map(|label| RepositoryLabel {
            label,
            status: match labels
                .iter()
                // GitHub compares label names without regard to case.
                .find(|found| found.name.eq_ignore_ascii_case(label.name))
            {
                // GitHub gives colors in lowercase.
                Some(found) if found.color.eq_ignore_ascii_case(label.color) => {
                    LabelStatus::Present
                }
                Some(found) => LabelStatus::WrongColor(found.color.clone()),
                None => LabelStatus::Missing,
            },
        })
        .collect())
}

// The description and the color of a correct label stay.
pub async fn fix(repository: &Repository) -> Result<(), Box<dyn Error + Send + Sync>> {
    for entry in status(repository).await? {
        match entry.status {
            LabelStatus::Present => {}
            LabelStatus::WrongColor(_) => {
                repository
                    .set_label_color(entry.label.name, entry.label.color)
                    .await?;
            }
            LabelStatus::Missing => {
                repository
                    .create_label(entry.label.name, entry.label.color, entry.label.description)
                    .await?;
            }
        }
    }
    Ok(())
}
