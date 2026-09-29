use std::collections::{BTreeMap, HashMap};
use std::error::Error;
use std::sync::{Arc, Mutex};

use mobius_domain::Live;
use time::OffsetDateTime;
use tokio::sync::Notify;

use crate::config::{Config, RoleBinding};
use crate::{Engine, agents, limits};

#[derive(Default)]
pub(crate) struct Workers {
    counts: Mutex<Counts>,
    // Each change of the counts or of the queue wakes all queued agents.
    pub(crate) changed: Notify,
}

#[derive(Default)]
struct Counts {
    running: BTreeMap<Role, u32>,
    // The key is the task id, and the value is the role of its session.
    queued: HashMap<i64, Role>,
    // The waiting sessions with no task, ordered by their time in the queue.
    waiting: BTreeMap<(OffsetDateTime, i64), Role>,
}

// The limit group of a session. The lead_chat and lead_event sessions share the group `lead`.
#[derive(Clone, Copy, Hash, PartialEq, Eq, PartialOrd, Ord)]
pub(crate) enum Role {
    Lead,
    Triager,
    Implementer,
    Researcher,
    Reviewer,
    Judge,
}

impl Role {
    fn name(self) -> &'static str {
        match self {
            Self::Lead => "lead",
            Self::Triager => "triager",
            Self::Implementer => "implementer",
            Self::Researcher => "researcher",
            Self::Reviewer => "reviewer",
            Self::Judge => "judge",
        }
    }

    fn binding(self, config: &Config) -> &RoleBinding {
        let roles = &config.roles;
        match self {
            Self::Lead => &roles.lead,
            Self::Triager => &roles.triager,
            Self::Implementer => &roles.implementer,
            Self::Researcher => &roles.researcher,
            Self::Reviewer => &roles.reviewer,
            Self::Judge => &roles.judge,
        }
    }

    // The sessions of these roles count toward `max_agents`.
    fn global(self) -> bool {
        matches!(
            self,
            Self::Implementer | Self::Researcher | Self::Reviewer | Self::Judge
        )
    }

    // A pause of the Harness of the role holds the session in the queue.
    fn pauses(self) -> bool {
        matches!(self, Self::Implementer | Self::Researcher | Self::Reviewer)
    }
}

pub(crate) struct Slot {
    workers: Arc<Workers>,
    role: Role,
}

impl Drop for Slot {
    fn drop(&mut self) {
        *self
            .workers
            .counts
            .lock()
            .unwrap()
            .running
            .entry(self.role)
            .or_default() -= 1;
        self.workers.changed.notify_waiters();
    }
}

// Gives `None` when the task leaves the queue before it gets a slot, for example after a decline of the Lead.
pub(crate) async fn slot(
    engine: &Engine,
    task: i64,
    session: i64,
    role: Role,
) -> Result<Option<Slot>, Box<dyn Error + Send + Sync>> {
    engine
        .workers
        .counts
        .lock()
        .unwrap()
        .queued
        .insert(task, role);
    let slot = wait(engine, Place::Task(task), session, role).await;
    engine.workers.counts.lock().unwrap().queued.remove(&task);
    engine.workers.changed.notify_waiters();
    let Some(slot) = slot? else {
        return Ok(None);
    };
    if !engine
        .store
        .tasks()
        .set_state(task, "queued", "working")
        .await?
    {
        return Ok(None);
    }
    let started = engine.store.sessions().start(session).await?;
    engine.broadcast(Live::Agent(agents::node(started)));
    Ok(Some(slot))
}

// A session with no task waits behind each session that queued before it.
pub(crate) async fn session_slot(
    engine: &Engine,
    session: i64,
    role: Role,
) -> Result<Slot, Box<dyn Error + Send + Sync>> {
    let since = OffsetDateTime::now_utc();
    engine
        .workers
        .counts
        .lock()
        .unwrap()
        .waiting
        .insert((since, session), role);
    let slot = wait(engine, Place::Since(since), session, role).await;
    engine
        .workers
        .counts
        .lock()
        .unwrap()
        .waiting
        .remove(&(since, session));
    engine.workers.changed.notify_waiters();
    let slot = slot?.expect("a session with no task keeps its place in the queue");
    let started = engine.store.sessions().start(session).await?;
    engine.broadcast(Live::Agent(agents::node(started)));
    Ok(slot)
}

enum Place {
    Task(i64),
    Since(OffsetDateTime),
}

async fn wait(
    engine: &Engine,
    place: Place,
    session: i64,
    role: Role,
) -> Result<Option<Slot>, Box<dyn Error + Send + Sync>> {
    let harness = role.binding(&engine.config).harness;
    let mut shown = None;
    loop {
        let changed = engine.workers.changed.notified();
        tokio::pin!(changed);
        changed.as_mut().enable();
        let queue = engine.store.tasks().queued().await?;
        // A paused Harness takes no slot, so a pause does not count toward a limit.
        let pause = match role.pauses() {
            true => engine.store.harness_pauses().get(harness).await?,
            false => None,
        };
        let (position, at) = match place {
            Place::Task(task) => {
                let Some(position) = queue.iter().position(|(id, _)| *id == task) else {
                    return Ok(None);
                };
                (position, queue[position].1)
            }
            Place::Since(since) => (queue.iter().filter(|(_, at)| *at <= since).count(), since),
        };
        let text = {
            let mut counts = engine.workers.counts.lock().unwrap();
            let bound = match place {
                Place::Task(_) => (at, i64::MIN),
                Place::Since(_) => (at, session),
            };
            let mut earlier: Vec<(OffsetDateTime, Role)> = queue[..position]
                .iter()
                .filter_map(|(id, at)| counts.queued.get(id).map(|role| (*at, *role)))
                .collect();
            earlier.extend(
                counts
                    .waiting
                    .range(..bound)
                    .map(|(key, role)| (key.0, *role)),
            );
            earlier.sort_unstable_by_key(|(at, _)| *at);
            let earlier: Vec<Role> = earlier.into_iter().map(|(_, role)| role).collect();
            let text = match &pause {
                Some(pause) => Some(limits::reason(pause)?),
                None => reason(&engine.config, &counts.running, &earlier, role),
            };
            let Some(text) = text else {
                *counts.running.entry(role).or_default() += 1;
                match place {
                    Place::Task(task) => {
                        counts.queued.remove(&task);
                    }
                    Place::Since(since) => {
                        counts.waiting.remove(&(since, session));
                    }
                }
                return Ok(Some(Slot {
                    workers: engine.workers.clone(),
                    role,
                }));
            };
            text
        };
        if shown.as_ref() != Some(&text) {
            let queued = engine
                .store
                .sessions()
                .set_queue_reason(session, &text)
                .await?;
            engine.broadcast(Live::Agent(agents::node(queued)));
            shown = Some(text);
        }
        changed.await;
    }
}

// Gives the reason why a session of `role` must wait. Each `earlier` session that fits starts before it.
fn reason(
    config: &Config,
    running: &BTreeMap<Role, u32>,
    earlier: &[Role],
    role: Role,
) -> Option<String> {
    let mut running = running.clone();
    for &other in earlier {
        if limit_reason(config, &running, other).is_none() {
            *running.entry(other).or_default() += 1;
        }
    }
    limit_reason(config, &running, role)
}

fn limit_reason(config: &Config, running: &BTreeMap<Role, u32>, role: Role) -> Option<String> {
    if role.global() {
        let total: u32 = running
            .iter()
            .filter(|(other, _)| other.global())
            .map(|(_, count)| *count)
            .sum();
        if total >= config.max_agents {
            return Some(format!(
                "no free agent slot ({total}/{})",
                config.max_agents
            ));
        }
    }
    let count = running.get(&role).copied().unwrap_or_default();
    let max = role.binding(config).max;
    (count >= max).then(|| format!("no free {} slot ({count}/{max})", role.name()))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn config() -> Config {
        crate::config::parse(
            r#"
access_password = "correct horse"
trusted_users = ["owner"]
max_agents = 4

[roles]
lead        = { harness = "claude-code", model = "opus",    effort = "high" }
triager     = { harness = "claude-code", model = "sonnet",  effort = "medium" }
implementer = { harness = "devin",       model = "swe-1.5", effort = "high" }
researcher  = { harness = "antigravity", model = "gemini-3-pro" }
reviewer    = { harness = "claude-code", model = "opus",    effort = "high" }
judge       = { harness = "claude-code", model = "haiku",   effort = "low" }
"#,
        )
        .unwrap()
    }

    #[test]
    fn an_agent_with_free_slots_starts() {
        let running = BTreeMap::from([(Role::Implementer, 1)]);

        assert_eq!(reason(&config(), &running, &[], Role::Implementer), None);
    }

    #[test]
    fn a_full_role_gives_its_count() {
        let running = BTreeMap::from([(Role::Implementer, 2)]);

        assert_eq!(
            reason(&config(), &running, &[], Role::Implementer).as_deref(),
            Some("no free implementer slot (2/2)")
        );
    }

    #[test]
    fn a_full_global_limit_counts_the_counted_roles() {
        let running = BTreeMap::from([(Role::Implementer, 2), (Role::Reviewer, 2)]);

        assert_eq!(
            reason(&config(), &running, &[], Role::Judge).as_deref(),
            Some("no free agent slot (4/4)")
        );
    }

    #[test]
    fn the_lead_and_the_triager_do_not_count_toward_the_global_limit() {
        let running = BTreeMap::from([(Role::Implementer, 2), (Role::Reviewer, 2)]);

        assert_eq!(reason(&config(), &running, &[], Role::Lead), None);
        assert_eq!(reason(&config(), &running, &[], Role::Triager), None);
    }

    #[test]
    fn a_full_lead_limit_blocks_a_lead_session() {
        let running = BTreeMap::from([(Role::Lead, 2)]);

        assert_eq!(
            reason(&config(), &running, &[], Role::Lead).as_deref(),
            Some("no free lead slot (2/2)")
        );
    }

    #[test]
    fn an_earlier_agent_that_fits_takes_the_last_slot() {
        let running = BTreeMap::from([(Role::Implementer, 1)]);

        assert_eq!(
            reason(&config(), &running, &[Role::Implementer], Role::Implementer).as_deref(),
            Some("no free implementer slot (2/2)")
        );
    }

    #[test]
    fn an_earlier_agent_of_a_full_role_does_not_block_a_later_agent() {
        let running = BTreeMap::from([(Role::Implementer, 2)]);

        assert_eq!(
            reason(&config(), &running, &[Role::Implementer], Role::Reviewer),
            None
        );
    }
}
