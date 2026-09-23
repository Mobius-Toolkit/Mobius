//! Server-side event bus: a `broadcast` channel plus a bounded ring buffer so
//! SSE clients can replay missed events via `Last-Event-ID`.

use mobius_core::{DomainEvent, EventEnvelope};
use std::collections::VecDeque;
use std::sync::Mutex;
use std::sync::atomic::{AtomicU64, Ordering};
use tokio::sync::broadcast;

const RING_CAPACITY: usize = 1024;

pub struct EventBus {
    tx: broadcast::Sender<EventEnvelope>,
    ring: Mutex<VecDeque<EventEnvelope>>,
    next_id: AtomicU64,
}

impl Default for EventBus {
    fn default() -> Self {
        Self::new()
    }
}

impl EventBus {
    pub fn new() -> Self {
        let (tx, _) = broadcast::channel(4096);
        Self {
            tx,
            ring: Mutex::new(VecDeque::with_capacity(RING_CAPACITY)),
            next_id: AtomicU64::new(1),
        }
    }

    /// Assign a monotonic id, buffer for replay, broadcast live.
    pub fn publish(&self, event: DomainEvent) -> EventEnvelope {
        let envelope = EventEnvelope {
            id: self.next_id.fetch_add(1, Ordering::SeqCst),
            at: chrono::Utc::now(),
            event,
        };
        if let Ok(mut ring) = self.ring.lock() {
            if ring.len() >= RING_CAPACITY {
                ring.pop_front();
            }
            ring.push_back(envelope.clone());
        }
        let _ = self.tx.send(envelope.clone());
        envelope
    }

    pub fn subscribe(&self) -> broadcast::Receiver<EventEnvelope> {
        self.tx.subscribe()
    }

    /// Buffered events with `id > last_event_id`, in order.
    pub fn replay_after(&self, last_event_id: u64) -> Vec<EventEnvelope> {
        self.ring
            .lock()
            .map(|ring| {
                ring.iter()
                    .filter(|e| e.id > last_event_id)
                    .cloned()
                    .collect()
            })
            .unwrap_or_default()
    }

    pub fn latest_id(&self) -> u64 {
        self.next_id.load(Ordering::SeqCst).saturating_sub(1)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use mobius_core::{EntityKind, TaskId};

    fn ev() -> DomainEvent {
        DomainEvent::EntityChanged {
            kind: EntityKind::Task,
            id: TaskId::new().to_string(),
        }
    }

    #[test]
    fn replay_returns_events_after_last_id() {
        let bus = EventBus::new();
        let e1 = bus.publish(ev());
        let e2 = bus.publish(ev());
        let e3 = bus.publish(ev());
        assert_eq!((e1.id, e2.id, e3.id), (1, 2, 3));
        let replayed = bus.replay_after(1);
        assert_eq!(replayed.len(), 2);
        assert_eq!(replayed[0].id, 2);
        let all = bus.replay_after(0);
        assert_eq!(all.len(), 3);
    }

    #[test]
    fn ring_is_bounded() {
        let bus = EventBus::new();
        for _ in 0..RING_CAPACITY + 10 {
            bus.publish(ev());
        }
        let all = bus.replay_after(0);
        assert_eq!(all.len(), RING_CAPACITY);
        assert_eq!(all[0].id, 11);
    }
}
