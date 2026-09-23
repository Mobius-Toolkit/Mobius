//! Builds the prompt for a task run: agent instructions + project memory +
//! task description.

use mobius_core::{MemoryEntry, Task};

pub struct PromptBuilder;

impl PromptBuilder {
    pub fn build(instructions: &str, memory: &[MemoryEntry], task: &Task) -> String {
        let mut out = String::new();
        if !instructions.trim().is_empty() {
            out.push_str(instructions.trim());
            out.push_str("\n\n");
        }
        if !memory.is_empty() {
            out.push_str("## Project memory\n\n");
            for entry in memory.iter().filter(|e| e.superseded_by.is_none()) {
                out.push_str(&format!("- ({})\t{}\n", entry.kind, entry.content));
            }
            out.push('\n');
        }
        out.push_str(&format!("## Task: {}\n\n", task.title));
        if !task.description.trim().is_empty() {
            out.push_str(task.description.trim());
            out.push('\n');
        }
        out
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use mobius_core::*;

    #[test]
    fn prompt_includes_memory_entries() {
        let task = Task {
            id: TaskId::new(),
            project_id: ProjectId::new(),
            agent_id: AgentId::new(),
            parent_task_id: None,
            title: "Fix thing".into(),
            description: "details".into(),
            kind: TaskKind::Implement,
            status: TaskStatus::Queued,
            origin: TaskOrigin::default(),
            priority: Priority::Normal,
            created_at: chrono::Utc::now(),
            updated_at: chrono::Utc::now(),
        };
        let memory = vec![
            MemoryEntry {
                id: MemoryEntryId::new(),
                scope: MemoryScope::Project(ProjectId::new()),
                kind: MemoryKind::Gotcha,
                content: "never unwrap".into(),
                source_run_id: None,
                superseded_by: None,
                created_at: chrono::Utc::now(),
            },
            MemoryEntry {
                id: MemoryEntryId::new(),
                scope: MemoryScope::Project(ProjectId::new()),
                kind: MemoryKind::Fact,
                content: "stale".into(),
                source_run_id: None,
                superseded_by: Some(MemoryEntryId::new()),
                created_at: chrono::Utc::now(),
            },
        ];
        let prompt = PromptBuilder::build("be careful", &memory, &task);
        assert!(prompt.contains("be careful"));
        assert!(prompt.contains("never unwrap"));
        assert!(!prompt.contains("stale"));
        assert!(prompt.contains("Fix thing"));
        assert!(prompt.contains("details"));
    }
}
