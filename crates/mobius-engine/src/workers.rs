use std::collections::{BTreeMap, HashMap};
use std::error::Error;
use std::sync::{Arc, Mutex};

use mobius_domain::{Harness, Live};
use time::OffsetDateTime;
use tokio::sync::Notify;

use crate::config::Config;
use crate::{Engine, agents, drain, limits};

#[derive(Default)]
pub(crate) struct Workers {
    counts: Mutex<Counts>,
    // Each change of the counts or of the queue wakes all queued Workers.
    pub(crate) changed: Notify,
}

#[derive(Default)]
struct Counts {
    running: BTreeMap<Harness, u32>,
    // The key is the task id, and the value is the Harness of its Worker.
    queued: HashMap<i64, Harness>,
}

pub(crate) struct Slot {
    workers: Arc<Workers>,
    harness: Harness,
    // The slot holder is one agent that the drain waits for.
    _drain: drain::Guard,
}

impl Drop for Slot {
    fn drop(&mut self) {
        *self
            .workers
            .counts
            .lock()
            .unwrap()
            .running
            .entry(self.harness)
            .or_default() -= 1;
        self.workers.changed.notify_waiters();
    }
}

// Gives `None` when the task leaves the queue before it gets a slot, for example after a decline of the Lead.
pub(crate) async fn slot(
    engine: &Engine,
    task: i64,
    session: i64,
    harness: Harness,
) -> Result<Option<Slot>, Box<dyn Error + Send + Sync>> {
    engine
        .workers
        .counts
        .lock()
        .unwrap()
        .queued
        .insert(task, harness);
    let slot = wait(engine, Place::Task(task), session, harness).await;
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

pub(crate) async fn research_slot(
    engine: &Engine,
    session: i64,
    harness: Harness,
) -> Result<Option<Slot>, Box<dyn Error + Send + Sync>> {
    let since = OffsetDateTime::now_utc();
    let slot = wait(engine, Place::Since(since), session, harness).await?;
    let started = engine.store.sessions().start(session).await?;
    engine.broadcast(Live::Agent(agents::node(started)));
    Ok(slot)
}

// A Researcher has no task, so it waits behind each task that queued before it.
enum Place {
    Task(i64),
    Since(OffsetDateTime),
}

async fn wait(
    engine: &Engine,
    place: Place,
    session: i64,
    harness: Harness,
) -> Result<Option<Slot>, Box<dyn Error + Send + Sync>> {
    let mut shown = None;
    loop {
        let changed = engine.workers.changed.notified();
        tokio::pin!(changed);
        changed.as_mut().enable();
        let queue = engine.store.tasks().queued().await?;
        // A paused Harness takes no slot, so a pause does not count toward a limit.
        let pause = engine.store.harness_pauses().get(harness).await?;
        let position = match place {
            Place::Task(task) => {
                let Some(position) = queue.iter().position(|(id, _)| *id == task) else {
                    return Ok(None);
                };
                position
            }
            Place::Since(since) => queue.iter().filter(|(_, at)| *at <= since).count(),
        };
        let text = {
            let mut counts = engine.workers.counts.lock().unwrap();
            let earlier: Vec<Harness> = queue[..position]
                .iter()
                .filter_map(|(id, _)| counts.queued.get(id).copied())
                .collect();
            let text = if engine.drain.on() {
                Some(drain::REASON.to_string())
            } else {
                match &pause {
                    Some(pause) => Some(limits::reason(pause)?),
                    None => reason(&engine.config, &counts.running, &earlier, harness),
                }
            };
            match text {
                Some(text) => text,
                // A drain that starts while the Worker waits keeps it in the queue.
                None => match drain::try_track(engine) {
                    Some(guard) => {
                        *counts.running.entry(harness).or_default() += 1;
                        if let Place::Task(task) = place {
                            counts.queued.remove(&task);
                        }
                        return Ok(Some(Slot {
                            workers: engine.workers.clone(),
                            harness,
                            _drain: guard,
                        }));
                    }
                    None => drain::REASON.to_string(),
                },
            }
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

// Gives the reason why a Worker of `harness` must wait. Each `earlier` Worker that fits starts before it.
fn reason(
    config: &Config,
    running: &BTreeMap<Harness, u32>,
    earlier: &[Harness],
    harness: Harness,
) -> Option<String> {
    let mut running = running.clone();
    for &other in earlier {
        if limit_reason(config, &running, other).is_none() {
            *running.entry(other).or_default() += 1;
        }
    }
    limit_reason(config, &running, harness)
}

fn limit_reason(
    config: &Config,
    running: &BTreeMap<Harness, u32>,
    harness: Harness,
) -> Option<String> {
    let total: u32 = running.values().sum();
    if total >= config.max_workers_total {
        return Some(format!(
            "no free Worker slot ({total}/{})",
            config.max_workers_total
        ));
    }
    let count = running.get(&harness).copied().unwrap_or_default();
    let max = config.max_workers[&harness];
    (count >= max).then(|| format!("no free {} slot ({count}/{max})", harness.name()))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn config() -> Config {
        crate::config::parse(
            r#"
access_password = "correct horse"
trusted_users = ["owner"]
max_workers_total = 4

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
    fn a_worker_with_free_slots_starts() {
        let running = BTreeMap::from([(Harness::Devin, 1)]);

        assert_eq!(reason(&config(), &running, &[], Harness::Devin), None);
    }

    #[test]
    fn a_full_harness_gives_its_count() {
        let running = BTreeMap::from([(Harness::Devin, 2)]);

        assert_eq!(
            reason(&config(), &running, &[], Harness::Devin).as_deref(),
            Some("no free devin slot (2/2)")
        );
    }

    #[test]
    fn a_full_total_gives_the_worker_count() {
        let running = BTreeMap::from([(Harness::Devin, 2), (Harness::ClaudeCode, 2)]);

        assert_eq!(
            reason(&config(), &running, &[], Harness::Antigravity).as_deref(),
            Some("no free Worker slot (4/4)")
        );
    }

    #[test]
    fn an_earlier_worker_that_fits_takes_the_last_slot() {
        let running = BTreeMap::from([(Harness::Devin, 1)]);

        assert_eq!(
            reason(&config(), &running, &[Harness::Devin], Harness::Devin).as_deref(),
            Some("no free devin slot (2/2)")
        );
    }

    #[test]
    fn an_earlier_worker_of_a_full_harness_does_not_block_a_later_worker() {
        let running = BTreeMap::from([(Harness::Devin, 2)]);

        assert_eq!(
            reason(&config(), &running, &[Harness::Devin], Harness::ClaudeCode),
            None
        );
    }
}
