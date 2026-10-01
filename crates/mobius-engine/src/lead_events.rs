use std::error::Error;

use mobius_domain::{InboxKind, Live, organization};
use mobius_runner::Session;
use mobius_store::NewInboxItem;
use serde_json::Value;
use tokio::sync::broadcast;
use tokio::sync::mpsc::{self, UnboundedReceiver, UnboundedSender};

use crate::lead::{self, Recorder, SAVE_PROMPT};
use crate::{Engine, drain, limits, mcp, workers};

pub(crate) const ROLE: &str = "lead_event";
const ROLE_PROMPT: &str = include_str!("prompts/lead_event.md");

// Each send tells the event session of one Workstream that its queue can have a new event.
pub(crate) type Wakes = UnboundedSender<()>;

pub(crate) async fn add(
    engine: &Engine,
    repository: &str,
    workstream: i64,
    kind: &str,
    payload: &str,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    engine
        .store
        .lead_events()
        .add(repository, workstream, kind, payload)
        .await?;
    wake(engine, repository, workstream);
    Ok(())
}

// An event session starts when none runs, and it reads each undelivered event of the Workstream.
pub(crate) fn wake(engine: &Engine, repository: &str, workstream: i64) {
    let mut sessions = engine.event_sessions.lock().unwrap();
    let key = (repository.to_string(), workstream);
    match sessions.get(&key) {
        // The entry exists only while `run` holds the receiver.
        Some(wakes) => {
            let _ = wakes.send(());
        }
        None => {
            // The drain holds each new event session. Its events stay in the queue.
            let Some(guard) = drain::try_track(engine) else {
                return;
            };
            let (wakes, receiver) = mpsc::unbounded_channel();
            sessions.insert(key, wakes);
            // The subscription comes before the spawn, so the session gets each stop of its Lead.
            tokio::spawn(run(
                engine.clone(),
                engine.lead_stops.subscribe(),
                repository.to_string(),
                workstream,
                receiver,
                guard,
            ));
        }
    }
}

// The drain wakes each event session, and each saves its memory and closes.
pub(crate) fn close_all(engine: &Engine) {
    for wakes in engine.event_sessions.lock().unwrap().values() {
        let _ = wakes.send(());
    }
}

// After a crash, a new session gets the same event, because an event stays in the queue until its turn ends.
async fn run(
    engine: Engine,
    mut stops: broadcast::Receiver<(String, i64)>,
    repository: String,
    workstream: i64,
    mut wakes: UnboundedReceiver<()>,
    _drain: drain::Guard,
) {
    let mut crashes = 0;
    loop {
        let Err(error) = session(&engine, &mut stops, &repository, workstream, &mut wakes).await
        else {
            return;
        };
        eprintln!("mobius: event session of {repository}#{workstream}: {error}");
        if !lead::context_error(&*error) {
            crashes += 1;
        }
        if crashes <= lead::MAX_CRASHES {
            continue;
        }
        if let Err(failure) = failed(&engine, &repository, workstream).await {
            eprintln!("mobius: event session of {repository}#{workstream}: {failure}");
        }
        return remove(&engine, &repository, workstream);
    }
}

async fn failed(
    engine: &Engine,
    repository: &str,
    workstream: i64,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let events = engine.store.lead_events();
    for event in events.undelivered(repository, workstream).await? {
        let item = engine
            .store
            .inbox_items()
            .add(NewInboxItem {
                kind: InboxKind::LeadFailed,
                organization: organization(repository),
                repository,
                workstream,
                issue: workstream,
                text: &event.payload,
                link: "",
            })
            .await?;
        engine.broadcast(Live::Inbox(item));
        events.deliver(event.id).await?;
    }
    Ok(())
}

async fn session(
    engine: &Engine,
    stops: &mut broadcast::Receiver<(String, i64)>,
    repository: &str,
    workstream: i64,
    wakes: &mut UnboundedReceiver<()>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let session = lead::add_session(
        engine,
        ROLE,
        &engine.config.roles.lead,
        organization(repository),
        repository,
        workstream,
        None,
    )
    .await?;
    let mut recorder = Recorder::new(
        engine,
        session,
        organization(repository),
        repository,
        workstream,
        None,
    );
    // A stop while the session waits ends the session and frees the place in the queue.
    let slot = tokio::select! {
        slot = workers::session_slot(engine, session, workers::Role::Lead) => slot,
        () = lead::stopped(stops, repository, workstream) => {
            remove(engine, repository, workstream);
            return lead::end_session(engine, session, "stopped").await;
        }
    };
    let _slot = match slot {
        Ok(slot) => slot,
        Err(error) => {
            recorder.fail(&error.to_string()).await?;
            return Err(error);
        }
    };
    let key = mcp::open(
        engine,
        mcp::Caller {
            session,
            role: ROLE,
            organization: organization(repository).to_string(),
            repository: repository.to_string(),
            workstream,
            cannot_do: None,
            fix: None,
            review: None,
            judge: None,
        },
    )?;
    let result = tokio::select! {
        result = events(engine, repository, workstream, session, &key, &mut recorder, wakes) => result.map(|()| "idle"),
        () = lead::stopped(stops, repository, workstream) => Ok("stopped"),
    };
    mcp::close(engine, &key);
    match result {
        Ok(reason) => {
            if reason == "stopped" {
                remove(engine, repository, workstream);
            }
            lead::end_session(engine, session, reason).await
        }
        Err(error) => {
            recorder.fail(&error.to_string()).await?;
            Err(error)
        }
    }
}

fn remove(engine: &Engine, repository: &str, workstream: i64) {
    engine
        .event_sessions
        .lock()
        .unwrap()
        .remove(&(repository.to_string(), workstream));
}

// An event stays in the queue until its turn ends.
async fn events(
    engine: &Engine,
    repository: &str,
    workstream: i64,
    session_id: i64,
    session_key: &str,
    recorder: &mut Recorder,
    wakes: &mut UnboundedReceiver<()>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let dir = mobius_runner::lead_dir(&engine.config.data_dir, repository, workstream)?;
    let context = lead::context(engine, &dir, repository, workstream).await?;
    let mut first = Some(format!("{ROLE_PROMPT}\n{context}# Event\n\n"));
    limits::wait(engine, engine.config.roles.lead.harness, Some(session_id)).await?;
    let (session, mut updates) = lead::start(
        engine,
        &engine.config.roles.lead,
        session_id,
        &dir,
        session_key,
        None,
    )
    .await?;
    let queue = engine.store.lead_events();
    loop {
        while wakes.try_recv().is_ok() {}
        // The drain holds each new turn. The events stay in the queue.
        if !engine.drain.on() {
            if let Some(event) = queue.next(repository, workstream).await? {
                let prompt = format!("{}{}", first.take().unwrap_or_default(), event.payload);
                turn(&session, &prompt, recorder, &mut updates).await?;
                queue.deliver(event.id).await?;
                continue;
            }
            let idle = tokio::time::timeout(engine.config.event_idle_timeout, async {
                loop {
                    tokio::select! {
                        Some(update) = updates.recv() => recorder.update(update).await?,
                        _ = wakes.recv() => return Ok::<_, Box<dyn Error + Send + Sync>>(()),
                    }
                }
            })
            .await;
            if let Ok(woken) = idle {
                woken?;
                continue;
            }
        }
        // A session with no turn has nothing to save, and `SAVE_PROMPT` as its first prompt lacks the Workstream context.
        if first.is_none() {
            turn(&session, SAVE_PROMPT, recorder, &mut updates).await?;
        }
        let mut sessions = engine.event_sessions.lock().unwrap();
        if engine.drain.on() || wakes.is_empty() {
            sessions.remove(&(repository.to_string(), workstream));
            break;
        }
    }
    session.close().await;
    Ok(())
}

pub(crate) async fn turn(
    session: &Session,
    prompt: &str,
    recorder: &mut Recorder,
    updates: &mut UnboundedReceiver<Value>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    loop {
        recorder.prompt(prompt).await?;
        let result = {
            let turn = session.prompt(prompt);
            tokio::pin!(turn);
            loop {
                tokio::select! {
                    biased;
                    result = &mut turn => break result,
                    Some(update) = updates.recv() => recorder.update(update).await?,
                }
            }
        };
        // The connection reads each update of the turn before the answer to the prompt.
        while let Ok(update) = updates.try_recv() {
            recorder.update(update).await?;
        }
        if let Err(error) = &result
            && limits::wait_out(recorder, session.harness(), error).await?
        {
            continue;
        }
        return Ok(result?);
    }
}
