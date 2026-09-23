//! `EventSource` wrapper feeding `DomainEvent`s into a Dioxus signal.

use dioxus::prelude::*;
use mobius_core::EventEnvelope;
use std::cell::RefCell;
use std::rc::Rc;
use wasm_bindgen::JsCast;
use wasm_bindgen::closure::Closure;

/// Subscribe to `/api/v1/events` once; every envelope is pushed into
/// `events` (capped buffer) and passed to `on_event`.
pub fn use_event_stream(events: Signal<Vec<EventEnvelope>>, on_event: Callback<EventEnvelope>) {
    let holder = use_hook(|| Rc::new(RefCell::new(None::<web_sys::EventSource>)));
    use_hook(|| {
        let es = web_sys::EventSource::new("/api/v1/events").expect("EventSource");
        let mut events = events;
        let cb = Closure::wrap(Box::new(move |ev: web_sys::MessageEvent| {
            if let Some(text) = ev.data().as_string()
                && let Ok(env) = serde_json::from_str::<EventEnvelope>(&text)
            {
                let mut list = events.write();
                list.push(env.clone());
                if list.len() > 500 {
                    let excess = list.len() - 500;
                    list.drain(..excess);
                }
                std::mem::drop(list);
                on_event.call(env);
            }
        }) as Box<dyn FnMut(_)>);
        es.set_onmessage(Some(cb.as_ref().unchecked_ref()));
        cb.forget(); // lives as long as the EventSource
        *holder.borrow_mut() = Some(es);
    });
    use_drop(move || {
        if let Some(es) = holder.borrow_mut().take() {
            es.close();
        }
    });
}
