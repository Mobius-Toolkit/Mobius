use std::error::Error;

use mobius_runner::Session;
use serde_json::Value;
use tokio::sync::broadcast;
use tokio::sync::mpsc::{self, UnboundedReceiver, UnboundedSender};

use crate::lead::{self, Recorder, SAVE_PROMPT};
use crate::{Engine, mcp};

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
    let mut sessions = engine.event_sessions.lock().unwrap();
    let key = (repository.to_string(), workstream);
    match sessions.get(&key) {
        // The entry exists only while `run` holds the receiver.
        Some(wakes) => {
            let _ = wakes.send(());
        }
        None => {
            let (wakes, receiver) = mpsc::unbounded_channel();
            sessions.insert(key, wakes);
            // The subscription comes before the spawn, so the session gets each stop of its Lead.
            tokio::spawn(run(
                engine.clone(),
                engine.lead_stops.subscribe(),
                repository.to_string(),
                workstream,
                receiver,
            ));
        }
    }
    Ok(())
}

async fn run(
    engine: Engine,
    mut stops: broadcast::Receiver<(String, i64)>,
    repository: String,
    workstream: i64,
    mut wakes: UnboundedReceiver<()>,
) {
    let session = match lead::add_session(
        &engine,
        ROLE,
        &engine.config.roles.lead,
        &repository,
        workstream,
    )
    .await
    {
        Ok(session) => session,
        Err(error) => {
            eprintln!("mobius: event session of {repository}#{workstream}: {error}");
            return remove(&engine, &repository, workstream);
        }
    };
    let mut recorder = Recorder::new(&engine, session, &repository, workstream, None);
    let result = match mcp::open(
        &engine,
        mcp::Caller {
            session,
            role: ROLE,
            repository: repository.clone(),
            workstream,
            cannot_do: None,
            fix: None,
            review: None,
            judge: None,
        },
    ) {
        Ok(key) => {
            let result = tokio::select! {
                result = events(
                    &engine,
                    &repository,
                    workstream,
                    session,
                    &key,
                    &mut recorder,
                    &mut wakes,
                ) => result.map(|()| "idle"),
                () = lead::stopped(&mut stops, &repository, workstream) => Ok("stopped"),
            };
            mcp::close(&engine, &key);
            result
        }
        Err(error) => Err(error.into()),
    };
    let ended = match result {
        Ok(reason) => {
            if reason == "stopped" {
                remove(&engine, &repository, workstream);
            }
            lead::end_session(&engine, session, reason).await
        }
        Err(error) => {
            remove(&engine, &repository, workstream);
            recorder.fail(&error.to_string()).await
        }
    };
    if let Err(failure) = ended {
        eprintln!("mobius: event session {session}: {failure}");
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
        turn(&session, SAVE_PROMPT, recorder, &mut updates).await?;
        let mut sessions = engine.event_sessions.lock().unwrap();
        if wakes.is_empty() {
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
    Ok(result?)
}
