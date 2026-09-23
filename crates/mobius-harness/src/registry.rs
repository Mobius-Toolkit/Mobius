use mobius_core::Harness;
use std::collections::HashMap;

/// Config-driven registry of available harnesses, keyed by name.
#[derive(Debug, Clone, Default)]
pub struct HarnessRegistry {
    harnesses: HashMap<String, Harness>,
}

impl HarnessRegistry {
    pub fn new(harnesses: Vec<Harness>) -> Self {
        Self {
            harnesses: harnesses.into_iter().map(|h| (h.name.clone(), h)).collect(),
        }
    }

    pub fn get(&self, name: &str) -> Option<&Harness> {
        self.harnesses.get(name)
    }

    pub fn list(&self) -> Vec<&Harness> {
        let mut v: Vec<_> = self.harnesses.values().collect();
        v.sort_by(|a, b| a.name.cmp(&b.name));
        v
    }
}
