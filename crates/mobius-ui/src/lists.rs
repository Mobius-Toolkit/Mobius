//! Read-only pages: Tasks and Runs.

use crate::Data;
use dioxus::prelude::*;

fn fmt_dt(d: &chrono::DateTime<chrono::Utc>) -> String {
    d.format("%Y-%m-%d %H:%M").to_string()
}

#[component]
pub fn TasksPage() -> Element {
    let data: Data = use_context();
    rsx! {
        h2 { "Tasks" }
        table {
            thead {
                tr { th { "Title" }, th { "Kind" }, th { "Status" }, th { "Priority" }, th { "Updated" } }
            }
            tbody {
                for t in data.tasks.read().iter() {
                    tr { key: "{t.id}",
                        td { "{t.title}" }
                        td { "{t.kind}" }
                        td { "{t.status}" }
                        td { "{t.priority}" }
                        td { class: "muted", "{fmt_dt(&t.updated_at)}" }
                    }
                }
            }
        }
    }
}

#[component]
pub fn RunsPage() -> Element {
    let data: Data = use_context();
    rsx! {
        h2 { "Runs" }
        table {
            thead {
                tr { th { "Run" }, th { "Activity" }, th { "Status" }, th { "Summary" }, th { "Started" } }
            }
            tbody {
                for r in data.runs.read().iter() {
                    tr { key: "{r.id}",
                        td { class: "mono", "{r.id}" }
                        td { "{r.activity}" }
                        td { "{r.status}" }
                        td { "{r.summary.clone().unwrap_or_default()}" }
                        td { class: "muted", "{fmt_dt(&r.started_at)}" }
                    }
                }
            }
        }
    }
}
