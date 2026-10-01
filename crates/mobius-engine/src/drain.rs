use std::error::Error;
use std::sync::Mutex;

use mobius_domain::{DrainEnd, Live};
use tokio::sync::Notify;

use crate::{Engine, chat, lead_events};

// The queue reason of a Worker that the drain holds.
pub(crate) const REASON: &str = "Mobius prepares an upgrade";

// The drain for an upgrade. While `on`, no new Worker, Judge, Triager, or event Lead turn starts, and `running` counts each agent that the drain waits for.
#[derive(Default)]
pub(crate) struct Drain {
    state: Mutex<State>,
    // Each change of the state wakes `start`.
    changed: Notify,
}

#[derive(Default)]
struct State {
    on: bool,
    running: usize,
}

impl Drain {
    pub(crate) fn on(&self) -> bool {
        self.state.lock().unwrap().on
    }

    // `Some` with the count while the drain is on, also after it completes, because a completed drain stays on until a cancel.
    fn waiting(&self) -> Option<usize> {
        let state = self.state.lock().unwrap();
        state.on.then_some(state.running)
    }
}

// Counts one running agent while it lives.
pub(crate) struct Guard {
    engine: Engine,
}

impl Drop for Guard {
    fn drop(&mut self) {
        let (on, running) = {
            let mut state = self.engine.drain.state.lock().unwrap();
            state.running -= 1;
            (state.on, state.running)
        };
        if on {
            self.engine.broadcast(Live::Drain {
                waiting: Some(running),
            });
        }
        self.engine.drain.changed.notify_waiters();
    }
}

// Counts one agent from here until the guard drops. `None` while the drain is on, so each held agent stays in its queue.
pub(crate) fn try_track(engine: &Engine) -> Option<Guard> {
    let mut state = engine.drain.state.lock().unwrap();
    if state.on {
        return None;
    }
    state.running += 1;
    drop(state);
    Some(Guard {
        engine: engine.clone(),
    })
}

// Counts one agent from here until the guard drops, also while the drain is on. The chat Lead still runs during the drain, and the drain waits for it.
pub(crate) fn track(engine: &Engine) -> Guard {
    let (on, running) = {
        let mut state = engine.drain.state.lock().unwrap();
        state.running += 1;
        (state.on, state.running)
    };
    if on {
        engine.broadcast(Live::Drain {
            waiting: Some(running),
        });
    }
    Guard {
        engine: engine.clone(),
    }
}

// The number of agents the drain waits for, or `None` while no drain runs.
pub fn waiting(engine: &Engine) -> Option<usize> {
    engine.drain.waiting()
}

// Holds each new agent, asks each Lead to save its memory and to close, and waits until no agent runs. `cancel` ends the wait early. A completed drain stays `on`, so `cancel` still releases the held agents when the restart does not happen.
pub async fn start(engine: &Engine) -> DrainEnd {
    let waiting = {
        let mut state = engine.drain.state.lock().unwrap();
        state.on = true;
        state.running
    };
    // The Workers that wait for a slot show the drain reason.
    engine.workers.changed.notify_waiters();
    chat::close_all(engine);
    lead_events::close_all(engine);
    engine.broadcast(Live::Drain {
        waiting: Some(waiting),
    });
    loop {
        let changed = engine.drain.changed.notified();
        tokio::pin!(changed);
        changed.as_mut().enable();
        let (on, running) = {
            let state = engine.drain.state.lock().unwrap();
            (state.on, state.running)
        };
        if !on {
            return DrainEnd::Cancelled;
        }
        if running == 0 {
            return DrainEnd::Drained;
        }
        changed.await;
    }
}

// Ends the drain: the held Workers start and the Workstreams with waiting events wake.
pub async fn cancel(engine: &Engine) -> Result<(), Box<dyn Error + Send + Sync>> {
    {
        let mut state = engine.drain.state.lock().unwrap();
        if !state.on {
            return Ok(());
        }
        state.on = false;
    }
    engine.drain.changed.notify_waiters();
    engine.broadcast(Live::Drain { waiting: None });
    engine.workers.changed.notify_waiters();
    for (repository, workstream) in engine.store.lead_events().waiting().await? {
        lead_events::wake(engine, &repository, workstream);
    }
    Ok(())
}
