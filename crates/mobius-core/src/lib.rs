pub mod event;
pub mod id;
pub mod inmem;
pub mod model;
pub mod ports;

pub use event::*;
pub use id::*;
pub use inmem::InMemoryStore;
pub use model::*;
pub use ports::*;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn task_status_transitions() {
        use TaskStatus::*;
        let legal = [
            (Proposed, Approved),
            (Proposed, Cancelled),
            (Approved, Queued),
            (Approved, Cancelled),
            (Queued, Running),
            (Queued, Cancelled),
            (Running, NeedsReview),
            (Running, Failed),
            (Running, Cancelled),
            (NeedsReview, Done),
            (NeedsReview, Queued),
            (NeedsReview, Failed),
            (NeedsReview, Cancelled),
            (Failed, Queued),
        ];
        for (from, to) in legal {
            assert!(
                from.can_transition_to(&to),
                "{from:?} -> {to:?} should be legal"
            );
        }

        let illegal = [
            (Proposed, Running),
            (Proposed, Queued),
            (Done, Running),
            (Done, Queued),
            (Cancelled, Queued),
            (Running, Done),
            (Queued, NeedsReview),
            (Approved, Running),
            (Proposed, Done),
        ];
        for (from, to) in illegal {
            assert!(
                !from.can_transition_to(&to),
                "{from:?} -> {to:?} should be illegal"
            );
        }
    }

    #[test]
    fn activity_profiles_resolution() {
        let default = ModelProfileId::new();
        let research = ModelProfileId::new();
        let mut overrides = std::collections::BTreeMap::new();
        overrides.insert(Activity::Research, research);
        let profiles = ActivityProfiles { default, overrides };

        assert_eq!(profiles.profile_for(Activity::Chat), default);
        assert_eq!(profiles.profile_for(Activity::Research), research);
        assert_eq!(profiles.profile_for(Activity::Implement), default);
    }

    #[test]
    fn task_kind_maps_to_activity() {
        assert_eq!(Activity::from(TaskKind::Implement), Activity::Implement);
        assert_eq!(Activity::from(TaskKind::Review), Activity::Review);
        assert_eq!(Activity::from(TaskKind::Triage), Activity::Triage);
        assert_eq!(
            Activity::from(TaskKind::Housekeeping),
            Activity::Housekeeping
        );
        assert_eq!(Activity::from(TaskKind::Research), Activity::Research);
    }

    #[test]
    fn typed_ids_roundtrip() {
        let id = TaskId::new();
        let s = id.to_string();
        assert!(matches!(s.parse::<TaskId>(), Ok(p) if p == id));
        match serde_json::to_string(&id) {
            Ok(json) => assert_eq!(json, format!("\"{s}\"")),
            Err(e) => panic!("serialize failed: {e}"),
        }
    }
}
