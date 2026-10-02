use std::error::Error;
use std::sync::Mutex;

use mobius_domain::{DrainEnd, Live};
use tokio::sync::Notify;

use crate::{Engine, chat};

// The queue reason of a Worker that the drain holds.
pub(crate) const REASON: &str = "Mobius prepares an upgrade";

// The drain for an upgrade. While `on`, no new Worker, Judge, Triager, or event turn of the Lead starts, and `running` counts each agent that the drain waits for.
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
    // Set by `seal`. A sealed drain accepts no new agent and ignores `cancel`.
    sealed: bool,
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

// Counts one agent from here until the guard drops, also while the drain is on. The chat Lead still runs during the drain, and the drain waits for it. `None` after `seal`.
pub(crate) fn track(engine: &Engine) -> Option<Guard> {
    let (on, running) = {
        let mut state = engine.drain.state.lock().unwrap();
        if state.sealed {
            return None;
        }
        state.running += 1;
        (state.on, state.running)
    };
    if on {
        engine.broadcast(Live::Drain {
            waiting: Some(running),
        });
    }
    Some(Guard {
        engine: engine.clone(),
    })
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

pub(crate) enum Seal {
    Sealed,
    // An agent runs, and the caller must wait with `start` again.
    Busy,
    // A `cancel` ended the drain.
    Cancelled,
}

// Closes the drain for the restart. After `Sealed`, no agent starts and `cancel` does nothing, until `abort`.
pub(crate) fn seal(engine: &Engine) -> Seal {
    let mut state = engine.drain.state.lock().unwrap();
    if !state.on {
        return Seal::Cancelled;
    }
    if state.running > 0 {
        return Seal::Busy;
    }
    state.sealed = true;
    Seal::Sealed
}

// Ends the drain: the held Workers start and the Workstreams with waiting events wake. A sealed drain ends only with `abort`.
pub async fn cancel(engine: &Engine) -> Result<(), Box<dyn Error + Send + Sync>> {
    if engine.drain.state.lock().unwrap().sealed {
        return Ok(());
    }
    release(engine).await
}

// Ends a sealed drain when the restart fails.
pub(crate) async fn abort(engine: &Engine) -> Result<(), Box<dyn Error + Send + Sync>> {
    engine.drain.state.lock().unwrap().sealed = false;
    release(engine).await
}

async fn release(engine: &Engine) -> Result<(), Box<dyn Error + Send + Sync>> {
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
        chat::wake_events(engine, &repository, workstream).await?;
    }
    Ok(())
}
