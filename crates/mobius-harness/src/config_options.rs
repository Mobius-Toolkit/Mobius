//! Pure matching helpers for ACP `configOptions`: mapping Mobius model/effort
//! profile fields onto whatever options a harness actually advertises.
//! Matching is fuzzy and never fails — callers `warn!` and move on when a
//! match isn't found.

use mobius_core::{Effort, SessionConfigOption, SessionConfigSelectOption};

/// Convert the protocol crate's config option into our wasm-safe mirror type.
/// Grouped select options are flattened into a single list.
pub fn to_core(
    opt: &agent_client_protocol::schema::v1::SessionConfigOption,
) -> SessionConfigOption {
    use agent_client_protocol::schema::v1 as acp;
    let (kind, current_value, options) = match &opt.kind {
        acp::SessionConfigKind::Select(select) => {
            let options = match &select.options {
                acp::SessionConfigSelectOptions::Ungrouped(list) => list.clone(),
                acp::SessionConfigSelectOptions::Grouped(groups) => {
                    groups.iter().flat_map(|g| g.options.clone()).collect()
                }
                _ => vec![],
            };
            (
                Some("select".to_string()),
                Some(select.current_value.to_string()),
                options
                    .iter()
                    .map(|o| SessionConfigSelectOption {
                        value: o.value.to_string(),
                        name: o.name.clone(),
                        description: o.description.clone(),
                    })
                    .collect(),
            )
        }
        acp::SessionConfigKind::Boolean(b) => (
            Some("boolean".to_string()),
            Some(b.current_value.to_string()),
            vec![],
        ),
        other => (
            serde_json::to_value(other)
                .ok()
                .and_then(|v| v.get("type").and_then(|t| t.as_str()).map(String::from)),
            serde_json::to_value(other).ok().and_then(|v| {
                v.get("currentValue")
                    .and_then(|c| c.as_str().map(String::from))
            }),
            vec![],
        ),
    };
    SessionConfigOption {
        id: opt.id.to_string(),
        category: opt.category.as_ref().map(|c| {
            serde_json::to_value(c)
                .ok()
                .and_then(|v| v.as_str().map(String::from))
                .unwrap_or_default()
        }),
        name: opt.name.clone(),
        description: opt.description.clone(),
        kind,
        current_value,
        options,
    }
}

/// First option with the given `category` (e.g. `"model"`, `"thought_level"`).
pub fn find_by_category<'a>(
    options: &'a [SessionConfigOption],
    category: &str,
) -> Option<&'a SessionConfigOption> {
    options
        .iter()
        .find(|o| o.category.as_deref() == Some(category))
}

/// The effort control: first a `thought_level`-category option, else an option
/// whose id or name contains "effort" (e.g. the agy adapter's
/// `reasoningEffort`).
pub fn find_effort_option(options: &[SessionConfigOption]) -> Option<&SessionConfigOption> {
    find_by_category(options, "thought_level").or_else(|| {
        options.iter().find(|o| {
            o.id.to_lowercase().contains("effort") || o.name.to_lowercase().contains("effort")
        })
    })
}

/// Case-insensitive substring match of `model` against option value or name.
/// Returns the option's `value` (the id to pass to `session/set_config_option`).
pub fn match_model_option(opt: &SessionConfigOption, model: &str) -> Option<String> {
    let needle = model.to_lowercase();
    opt.options
        .iter()
        .find(|o| {
            o.value.to_lowercase().contains(&needle) || o.name.to_lowercase().contains(&needle)
        })
        .map(|o| o.value.clone())
}

/// Effort → select-option heuristic:
/// - `Max` → any option whose name or value contains "max", "xhigh" or "ultra"
///   (case-insensitive); falls back to the last option.
/// - `Low`/`Medium`/`High` → the option at that ordinal position
///   (Low = first, Medium = second, High = third), clamped to the list.
///
/// Returns the option `value` to send.
pub fn match_effort_option(opt: &SessionConfigOption, effort: Effort) -> Option<String> {
    if opt.options.is_empty() {
        return None;
    }
    if effort == Effort::Max {
        let special = opt.options.iter().find(|o| {
            let hay = format!("{} {}", o.value, o.name).to_lowercase();
            hay.contains("max") || hay.contains("xhigh") || hay.contains("ultra")
        });
        return Some(
            special
                .or_else(|| opt.options.last())
                .map(|o| o.value.clone())
                .unwrap_or_default(),
        );
    }
    let index = match effort {
        Effort::Low => 0,
        Effort::Medium => 1,
        Effort::High => 2,
        // `Max` is handled above; this arm only exists for exhaustiveness.
        Effort::Max => opt.options.len() - 1,
    };
    let idx = index.min(opt.options.len() - 1);
    Some(opt.options[idx].value.clone())
}

/// Whether a tool kind counts as "read-only" for [`PermissionPolicy::ReadOnly`]
/// sessions. Only `Read` qualifies; unknown kinds are rejected.
pub fn is_read_tool_kind(kind: Option<agent_client_protocol::schema::v1::ToolKind>) -> bool {
    matches!(
        kind,
        Some(agent_client_protocol::schema::v1::ToolKind::Read)
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    fn opt(value: &str, name: &str) -> SessionConfigSelectOption {
        SessionConfigSelectOption {
            value: value.to_string(),
            name: name.to_string(),
            description: None,
        }
    }

    fn select_option(values: &[(&str, &str)], category: &str) -> SessionConfigOption {
        SessionConfigOption {
            id: "id".into(),
            category: Some(category.to_string()),
            name: "n".into(),
            description: None,
            kind: Some("select".into()),
            current_value: None,
            options: values.iter().map(|(v, n)| opt(v, n)).collect(),
        }
    }

    #[test]
    fn model_fuzzy_match() {
        let o = select_option(
            &[("swe-1.6", "SWE 1.6"), ("opus-4.5", "Claude Opus")],
            "model",
        );
        assert_eq!(match_model_option(&o, "SWE"), Some("swe-1.6".into()));
        assert_eq!(match_model_option(&o, "opus"), Some("opus-4.5".into()));
        assert_eq!(match_model_option(&o, "nonexistent"), None);
    }

    #[test]
    fn effort_matching() {
        let o = select_option(
            &[
                ("low", "Low"),
                ("medium", "Medium"),
                ("high", "High"),
                ("max", "Max"),
            ],
            "thought_level",
        );
        assert_eq!(match_effort_option(&o, Effort::Low), Some("low".into()));
        assert_eq!(
            match_effort_option(&o, Effort::Medium),
            Some("medium".into())
        );
        assert_eq!(match_effort_option(&o, Effort::High), Some("high".into()));
        assert_eq!(match_effort_option(&o, Effort::Max), Some("max".into()));
    }

    #[test]
    fn effort_option_found_by_effort_in_id() {
        // The agy adapter exposes `reasoningEffort` with no `thought_level`
        // category.
        let mut o = select_option(&[("low", "Low"), ("high", "High")], "other");
        o.id = "reasoningEffort".into();
        o.name = "Reasoning Effort".into();
        let opts = vec![o];
        let found = find_effort_option(&opts).expect("found");
        assert_eq!(
            match_effort_option(found, Effort::High),
            Some("high".into())
        );
    }

    #[test]
    fn effort_option_prefers_thought_level_category() {
        let tl = select_option(&[("a", "A")], "thought_level");
        let mut eff = select_option(&[("b", "B")], "other");
        eff.id = "effort".into();
        let opts = vec![eff, tl];
        assert_eq!(
            find_effort_option(&opts).map(|o| o.category.as_deref()),
            Some(Some("thought_level"))
        );
    }

    #[test]
    fn effort_max_falls_back_to_last() {
        let o = select_option(&[("l", "Low"), ("h", "High")], "thought_level");
        assert_eq!(match_effort_option(&o, Effort::Max), Some("h".into()));
    }

    #[test]
    fn effort_clamps_when_fewer_options() {
        let o = select_option(&[("l", "Low")], "thought_level");
        assert_eq!(match_effort_option(&o, Effort::High), Some("l".into()));
    }

    #[test]
    fn read_only_tool_kinds() {
        use agent_client_protocol::schema::v1::ToolKind;
        assert!(is_read_tool_kind(Some(ToolKind::Read)));
        assert!(!is_read_tool_kind(Some(ToolKind::Edit)));
        assert!(!is_read_tool_kind(Some(ToolKind::Execute)));
        assert!(!is_read_tool_kind(None));
    }
}
