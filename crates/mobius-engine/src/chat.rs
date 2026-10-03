use std::collections::VecDeque;
use std::error::Error;
use std::fs;
use std::path::Path;
use std::sync::{Arc, Mutex};

use mobius_domain::{Author, ChatMessage, ChatView, InboxKind, Live, organization};
use mobius_github::Repository;
use mobius_runner::Session;
use mobius_store::{LeadEvent, NewInboxItem};
use serde_json::Value;
use tokio::sync::broadcast;
use tokio::sync::mpsc::{self, UnboundedReceiver, UnboundedSender};

use crate::lead::{self, Recorder, SAVE_PROMPT};
use crate::{
    ChatKey, Engine, TIME_FORMAT, drain, gh, inbox, lead_events, limits, mcp, triager, workers,
    workstreams,
};

pub(crate) const ROLE: &str = "lead_chat";
const ROLE_PROMPT: &str = include_str!("prompts/lead.md");
const HISTORY_SIZE: i64 = 20;

pub(crate) struct ChatHandle {
    commands: UnboundedSender<Command>,
    writing: bool,
}

// One turn of the session. An Owner message and an event share one queue, in the order that they occurred.
#[derive(Clone, Debug)]
enum Item {
    Message(ChatMessage),
    Event(LeadEvent),
}

// The turn that runs. A crash in this turn sends its item again in a new session.
#[derive(Default)]
pub(crate) struct Current {
    item: Option<Item>,
    // The Lead called `hold_event` in this turn.
    held: bool,
}

#[derive(Debug)]
enum Command {
    Prompt(ChatMessage),
    Event(LeadEvent),
    Stop,
    // The drain for an upgrade: the session saves its memory and closes.
    Drain,
}

pub async fn send(
    engine: &Engine,
    organization: &str,
    repository: &str,
    workstream: i64,
    text: &str,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    if !workstreams::organizations(engine)
        .iter()
        .any(|name| name == organization)
    {
        return Err(
            format!("Mobius has no repository in the organization \"{organization}\".").into(),
        );
    }
    post(
        engine,
        organization,
        repository,
        workstream,
        Author::Owner,
        text,
    )
    .await
}

pub(crate) async fn post(
    engine: &Engine,
    organization: &str,
    repository: &str,
    workstream: i64,
    author: Author,
    text: &str,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let Some(guard) = drain::track(engine) else {
        return Err("Mobius restarts for an upgrade. Send the message after the restart.".into());
    };
    let _order = engine.chat_order.lock().await;
    let message = engine
        .store
        .chat_messages()
        .add(organization, repository, workstream, author, text)
        .await?;
    if author != Author::Owner && author != Author::Researcher {
        engine.broadcast(Live::Unread(
            engine
                .store
                .chat_messages()
                .unread_of(organization, repository, workstream)
                .await?,
        ));
    }
    if author != Author::Researcher {
        engine.broadcast(Live::Message(message.clone()));
    }
    let mut chats = engine.chats.lock().unwrap();
    let key = (organization.to_string(), repository.to_string(), workstream);
    match chats.get_mut(&key) {
        Some(handle) => {
            handle.commands.send(Command::Prompt(message))?;
            handle.writing = true;
        }
        None => {
            let (commands, receiver) = mpsc::unbounded_channel();
            chats.insert(
                key.clone(),
                ChatHandle {
                    commands,
                    writing: true,
                },
            );
            // The subscription comes before the spawn, so the session gets each stop of its Lead.
            tokio::spawn(run(
                engine.clone(),
                engine.lead_stops.subscribe(),
                key,
                Item::Message(message),
                receiver,
                guard,
            ));
        }
    }
    engine.broadcast(Live::Lead {
        organization: organization.to_string(),
        repository: repository.to_string(),
        workstream,
        writing: true,
        error: None,
    });
    Ok(())
}

pub(crate) async fn wake_events(
    engine: &Engine,
    repository: &str,
    workstream: i64,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let _order = engine.chat_order.lock().await;
    send_events(engine, repository, workstream).await
}

// Sends each ready event of the Workstream to its Lead session, and starts the session when none runs. A session that already has an event skips the copy. The caller holds `chat_order`.
pub(crate) async fn send_events(
    engine: &Engine,
    repository: &str,
    workstream: i64,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let mut events = engine
        .store
        .lead_events()
        .ready(repository, workstream)
        .await?
        .into_iter();
    let Some(first) = events.next() else {
        return Ok(());
    };
    let organization = organization(repository);
    let key = (organization.to_string(), repository.to_string(), workstream);
    let mut chats = engine.chats.lock().unwrap();
    match chats.get_mut(&key) {
        Some(handle) => {
            for event in std::iter::once(first).chain(events) {
                handle.commands.send(Command::Event(event))?;
            }
            handle.writing = true;
        }
        None => {
            // The drain holds each new session. Its events stay undelivered.
            let Some(guard) = drain::try_track(engine) else {
                return Ok(());
            };
            let (commands, receiver) = mpsc::unbounded_channel();
            for event in events {
                commands.send(Command::Event(event))?;
            }
            chats.insert(
                key.clone(),
                ChatHandle {
                    commands,
                    writing: true,
                },
            );
            // The subscription comes before the spawn, so the session gets each stop of its Lead.
            tokio::spawn(run(
                engine.clone(),
                engine.lead_stops.subscribe(),
                key,
                Item::Event(first),
                receiver,
                guard,
            ));
        }
    }
    engine.broadcast(Live::Lead {
        organization: organization.to_string(),
        repository: repository.to_string(),
        workstream,
        writing: true,
        error: None,
    });
    Ok(())
}

// The drain sends each chat the close signal, and each chat closes when its work ends.
pub(crate) fn close_all(engine: &Engine) {
    for handle in engine.chats.lock().unwrap().values() {
        let _ = handle.commands.send(Command::Drain);
    }
}

pub fn stop(
    engine: &Engine,
    organization: &str,
    repository: &str,
    workstream: i64,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    if let Some(handle) = engine.chats.lock().unwrap().get(&(
        organization.to_string(),
        repository.to_string(),
        workstream,
    )) {
        handle.commands.send(Command::Stop)?;
    }
    Ok(())
}

pub async fn view(
    engine: &Engine,
    organization: &str,
    repository: &str,
    workstream: i64,
) -> Result<ChatView, Box<dyn Error + Send + Sync>> {
    let messages = engine
        .store
        .chat_messages()
        .list(organization, repository, workstream)
        .await?;
    let writing = engine
        .chats
        .lock()
        .unwrap()
        .get(&(organization.to_string(), repository.to_string(), workstream))
        .is_some_and(|handle| handle.writing);
    Ok(ChatView {
        messages,
        writing,
        lead: if workstream == triager::CHAT {
            engine.config.roles.triager.harness
        } else {
            engine.config.roles.lead.harness
        },
    })
}

pub async fn seen(
    engine: &Engine,
    organization: &str,
    repository: &str,
    workstream: i64,
    message: i64,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let chat_messages = engine.store.chat_messages();
    chat_messages
        .set_seen(organization, repository, workstream, message)
        .await?;
    engine.broadcast(Live::Unread(
        chat_messages
            .unread_of(organization, repository, workstream)
            .await?,
    ));
    Ok(())
}

pub(crate) async fn tell_owner(
    engine: &Engine,
    repository: &Repository,
    workstream: i64,
    text: &str,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let name = &repository.full_name;
    let organization = organization(name);
    let issue = repository
        .issue(workstream)
        .await?
        .ok_or("The Workstream issue does not exist.")?;
    let chat_messages = engine.store.chat_messages();
    let message = chat_messages
        .add(organization, name, workstream, Author::TellOwner, text)
        .await?;
    engine.broadcast(Live::Message(message));
    engine.broadcast(Live::Unread(
        chat_messages
            .unread_of(organization, name, workstream)
            .await?,
    ));
    inbox::add(
        engine,
        InboxKind::Lead,
        name,
        workstream,
        workstream,
        text,
        &issue.html_url,
    )
    .await?;
    Ok("Sent to the Owner.".to_string())
}

// After a crash, a new session gets the item of the failed turn as its first item. The queue keeps the later items.
async fn run(
    engine: Engine,
    mut stops: broadcast::Receiver<(String, i64)>,
    (organization, repository, workstream): ChatKey,
    first: Item,
    mut commands: UnboundedReceiver<Command>,
    _drain: drain::Guard,
) {
    let (role, binding, author) = if workstream == triager::CHAT {
        (triager::ROLE, &engine.config.roles.triager, Author::Triager)
    } else {
        (ROLE, &engine.config.roles.lead, Author::Lead)
    };
    let chat_key = (organization.clone(), repository.clone(), workstream);
    let mut first = first;
    let mut queue = VecDeque::new();
    let mut crashes = 0;
    loop {
        let session = match lead::add_session(
            &engine,
            role,
            binding,
            &organization,
            &repository,
            workstream,
            lead::Links::default(),
        )
        .await
        {
            Ok(session) => session,
            Err(error) => {
                return finish(
                    &engine,
                    &organization,
                    &repository,
                    workstream,
                    Some(error.to_string()),
                );
            }
        };
        let mut recorder = Recorder::new(
            &engine,
            session,
            &organization,
            &repository,
            workstream,
            Some(author),
        );
        // A stop while the session waits drops a first item that is an Owner message. The chat ends when no later item remains. An event stays.
        let wait = workers::session_slot(
            &engine,
            session,
            if workstream == triager::CHAT {
                workers::Role::Triager
            } else {
                workers::Role::Lead
            },
        );
        tokio::pin!(wait);
        let slot = loop {
            let stopped = tokio::select! {
                slot = &mut wait => break slot,
                () = lead::stopped(&mut stops, &repository, workstream) => true,
                command = commands.recv() => match command {
                    Some(Command::Prompt(message)) => {
                        queue.push_back(Item::Message(message));
                        false
                    }
                    Some(Command::Event(event)) => {
                        queue.push_back(Item::Event(event));
                        false
                    }
                    // The first item still turns, and the session closes after it.
                    Some(Command::Drain) => false,
                    Some(Command::Stop) if matches!(first, Item::Event(_)) => false,
                    Some(Command::Stop) => match queue.pop_front() {
                        Some(next) => {
                            first = next;
                            false
                        }
                        None => true,
                    },
                    None => true,
                },
            };
            if stopped {
                finish(&engine, &organization, &repository, workstream, None);
                if let Err(failure) = lead::end_session(&engine, session, "stopped").await {
                    eprintln!("mobius: chat session {session}: {failure}");
                }
                return;
            }
        };
        let _slot = match slot {
            Ok(slot) => slot,
            Err(error) => {
                let message = error.to_string();
                if let Err(failure) = recorder.fail(&message).await {
                    eprintln!("mobius: chat session {session}: {failure}");
                }
                return finish(
                    &engine,
                    &organization,
                    &repository,
                    workstream,
                    Some(message),
                );
            }
        };
        let current = Arc::new(Mutex::new(Current::default()));
        let caller = mcp::Caller {
            session,
            role,
            organization: organization.clone(),
            repository: repository.clone(),
            workstream,
            cannot_do: None,
            fix: None,
            review: None,
            judge: None,
            turn: Some(current.clone()),
        };
        let key = match mcp::open(&engine, caller) {
            Ok(key) => key,
            Err(error) => {
                return finish(
                    &engine,
                    &organization,
                    &repository,
                    workstream,
                    Some(error.to_string()),
                );
            }
        };
        let result = tokio::select! {
            result = chat(&engine, &first, &key, &mut recorder, &mut commands, &mut queue, &current) => result.map(|()| "idle"),
            () = lead::stopped(&mut stops, &repository, workstream) => Ok("stopped"),
        };
        mcp::close(&engine, &key);
        let error = match result {
            Ok(reason) => {
                if reason == "stopped" {
                    finish(&engine, &organization, &repository, workstream, None);
                }
                if let Err(failure) = lead::end_session(&engine, session, reason).await {
                    eprintln!("mobius: chat session {session}: {failure}");
                }
                return;
            }
            Err(error) => error,
        };
        if let Err(failure) = recorder.fail(&error.to_string()).await {
            eprintln!("mobius: chat session {session}: {failure}");
        }
        if !lead::context_error(&*error) {
            crashes += 1;
        }
        let item = current.lock().unwrap().item.take();
        if let Some(item) = item {
            if crashes <= lead::MAX_CRASHES {
                first = item;
                continue;
            }
            if let Err(failure) = failed(&engine, &chat_key, &item).await {
                eprintln!("mobius: chat session {session}: {failure}");
            }
        }
        return finish(
            &engine,
            &organization,
            &repository,
            workstream,
            Some(error.to_string()),
        );
    }
}

async fn failed(
    engine: &Engine,
    (_, repository, workstream): &ChatKey,
    item: &Item,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let message = match item {
        Item::Message(message) => message,
        Item::Event(_) => return lead_events::failed(engine, repository, *workstream).await,
    };
    let item = engine
        .store
        .inbox_items()
        .add(NewInboxItem {
            kind: InboxKind::LeadFailed,
            organization: &message.organization,
            repository: &message.repository,
            workstream: message.workstream,
            issue: message.workstream,
            text: &message.text,
            link: "",
        })
        .await?;
    engine.broadcast(Live::Inbox(item));
    Ok(())
}

fn finish(
    engine: &Engine,
    organization: &str,
    repository: &str,
    workstream: i64,
    error: Option<String>,
) {
    engine.chats.lock().unwrap().remove(&(
        organization.to_string(),
        repository.to_string(),
        workstream,
    ));
    engine.broadcast(Live::Lead {
        organization: organization.to_string(),
        repository: repository.to_string(),
        workstream,
        writing: false,
        error,
    });
}

async fn chat(
    engine: &Engine,
    first: &Item,
    session_key: &str,
    recorder: &mut Recorder,
    commands: &mut UnboundedReceiver<Command>,
    queue: &mut VecDeque<Item>,
    current: &Mutex<Current>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let (organization, repository, workstream) = recorder.chat_key();
    let key = &(organization.to_string(), repository.to_string(), workstream);
    let (organization, repository) = (&key.0, &key.1);
    let session_id = recorder.session();
    let triager = workstream == triager::CHAT;
    let data_dir = &engine.config.data_dir;
    let (dir, prompt, binding, gh_url) = match first {
        Item::Message(message) if triager => {
            let dir = mobius_runner::scratch_dir(data_dir, session_id);
            fs::create_dir_all(&dir)?;
            let history = history(engine, key, message.id).await?;
            let prompt = triager::chat_prompt(engine, message, &history).await?;
            (dir, prompt, &engine.config.roles.triager, None)
        }
        _ => {
            let dir = mobius_runner::lead_dir(data_dir, repository, workstream)?;
            let prompt = first_prompt(engine, &dir, key, first).await?;
            let url = gh::url(engine, session_key);
            (dir, prompt, &engine.config.roles.lead, Some(url))
        }
    };
    let (session, mut updates) = lead::start(
        engine,
        binding,
        session_id,
        &dir,
        session_key,
        gh_url.as_deref(),
    )
    .await?;
    current.lock().unwrap().item = Some(first.clone());
    item_turn(
        &session,
        first,
        &prompt,
        recorder,
        &mut updates,
        commands,
        queue,
    )
    .await?;
    end_turn(engine, repository, workstream, first, current, queue).await?;
    loop {
        while let Some(item) = queue.pop_front() {
            let prompt = match &item {
                Item::Message(message) => message_prompt(message),
                // The drain holds each event. The event stays undelivered until the drain ends.
                Item::Event(event) => {
                    if engine.drain.on() || !pending(engine, repository, workstream, event).await? {
                        continue;
                    }
                    event_prompt(event)
                }
            };
            current.lock().unwrap().item = Some(item.clone());
            item_turn(
                &session,
                &item,
                &prompt,
                recorder,
                &mut updates,
                commands,
                queue,
            )
            .await?;
            end_turn(engine, repository, workstream, &item, current, queue).await?;
        }
        {
            let mut chats = engine.chats.lock().unwrap();
            if let Some(handle) = chats.get_mut(key)
                && commands.is_empty()
            {
                handle.writing = false;
                engine.broadcast(Live::Lead {
                    organization: organization.to_string(),
                    repository: repository.to_string(),
                    workstream,
                    writing: false,
                    error: None,
                });
            }
        }
        // A chat that the drain reaches while it is idle saves and closes at once. A queued item still turns.
        let command = if engine.drain.on() {
            commands.try_recv().ok()
        } else {
            let idle = tokio::time::timeout(engine.config.lead_idle_timeout, async {
                loop {
                    tokio::select! {
                        Some(update) = updates.recv() => recorder.update(update).await?,
                        command = commands.recv() => return Ok::<_, Box<dyn Error + Send + Sync>>(command),
                    }
                }
            })
            .await;
            match idle {
                Ok(command) => command?,
                Err(_) => None,
            }
        };
        match command {
            Some(Command::Prompt(message)) => queue.push_back(Item::Message(message)),
            Some(Command::Event(event)) => queue.push_back(Item::Event(event)),
            Some(Command::Stop) => {}
            // The drain or the idle timeout closes the session. The Triager has no memory to save.
            Some(Command::Drain) | None => {
                if !triager {
                    turn(
                        &session,
                        SAVE_PROMPT,
                        true,
                        recorder,
                        &mut updates,
                        commands,
                        queue,
                    )
                    .await?;
                }
                let mut chats = engine.chats.lock().unwrap();
                if queue.is_empty() && commands.is_empty() {
                    chats.remove(key);
                    break;
                }
            }
        }
    }
    session.close().await;
    if triager {
        fs::remove_dir_all(&dir)?;
    }
    Ok(())
}

// A copy of an event that an earlier turn delivered or held is not pending. A held event holds the copies of each later event of its task issue.
async fn pending(
    engine: &Engine,
    repository: &str,
    workstream: i64,
    event: &LeadEvent,
) -> Result<bool, Box<dyn Error + Send + Sync>> {
    Ok(engine
        .store
        .lead_events()
        .ready(repository, workstream)
        .await?
        .iter()
        .any(|ready| ready.id == event.id))
}

// The reply text of an event turn goes only to the transcript. The Lead uses `tell_owner` to write to the Owner.
// A stop cancels only a turn for an Owner message. The Owner cannot see an event turn.
async fn item_turn(
    session: &Session,
    item: &Item,
    prompt: &str,
    recorder: &mut Recorder,
    updates: &mut UnboundedReceiver<Value>,
    commands: &mut UnboundedReceiver<Command>,
    queue: &mut VecDeque<Item>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    if matches!(item, Item::Message(_)) {
        return turn(session, prompt, true, recorder, updates, commands, queue).await;
    }
    let chat = recorder.set_chat(None);
    turn(session, prompt, false, recorder, updates, commands, queue).await?;
    recorder.set_chat(chat);
    Ok(())
}

pub(crate) fn hold_event(current: &Mutex<Current>) -> Result<String, Box<dyn Error + Send + Sync>> {
    let mut current = current.lock().unwrap();
    if !matches!(current.item, Some(Item::Event(_))) {
        return Err("hold_event works only in a turn for an event.".into());
    }
    current.held = true;
    Ok(
        "Mobius holds the event. It sends the event again after your next reply to the Owner."
            .to_string(),
    )
}

// An event is delivered at the end of its turn, unless the Lead held it. The end of a turn for an Owner message frees each held event.
// The freed events and the ready events of their task issues go in front of the queue and replace their queued copies. Each other queued item keeps its place.
async fn end_turn(
    engine: &Engine,
    repository: &str,
    workstream: i64,
    item: &Item,
    current: &Mutex<Current>,
    queue: &mut VecDeque<Item>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let events = engine.store.lead_events();
    match item {
        Item::Event(event) if current.lock().unwrap().held => events.hold(event.id).await?,
        Item::Event(event) => events.deliver(event.id).await?,
        Item::Message(message) if message.author == Author::Owner => {
            let freed = events.free(repository, workstream).await?;
            if !freed.is_empty() {
                let affected: Vec<LeadEvent> = events
                    .ready(repository, workstream)
                    .await?
                    .into_iter()
                    .filter(|ready| {
                        freed.iter().any(|event| {
                            event.id == ready.id
                                || (event.issue.is_some() && event.issue == ready.issue)
                        })
                    })
                    .collect();
                queue.retain(|item| match item {
                    Item::Event(queued) => !affected.iter().any(|event| event.id == queued.id),
                    Item::Message(_) => true,
                });
                for event in affected.into_iter().rev() {
                    queue.push_front(Item::Event(event));
                }
            }
        }
        Item::Message(_) => {}
    }
    *current.lock().unwrap() = Current::default();
    Ok(())
}

async fn turn(
    session: &Session,
    prompt: &str,
    stoppable: bool,
    recorder: &mut Recorder,
    updates: &mut UnboundedReceiver<Value>,
    commands: &mut UnboundedReceiver<Command>,
    queue: &mut VecDeque<Item>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    loop {
        recorder.prompt(prompt).await?;
        let result = {
            let turn = session.prompt(prompt);
            tokio::pin!(turn);
            loop {
                // The first poll of `turn` sends the prompt, so a `session/cancel` never goes before it.
                tokio::select! {
                    biased;
                    result = &mut turn => break result,
                    Some(update) = updates.recv() => recorder.update(update).await?,
                    Some(command) = commands.recv() => match command {
                        Command::Stop => {
                            if stoppable {
                                session.cancel();
                            }
                        }
                        Command::Prompt(message) => queue.push_back(Item::Message(message)),
                        Command::Event(event) => queue.push_back(Item::Event(event)),
                        // The session closes after the turn.
                        Command::Drain => {}
                    },
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

async fn first_prompt(
    engine: &Engine,
    dir: &Path,
    key: &ChatKey,
    first: &Item,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let context = lead::context(engine, dir, &key.1, key.2).await?;
    let sections = lead::repository_sections(engine, &engine.repository(&key.1)?, "lead").await?;
    let (before, heading, text) = match first {
        Item::Message(message) => (
            message.id,
            format!("{} message", message.author.name()),
            &message.text,
        ),
        Item::Event(event) => (
            event.chat_message.unwrap_or(i64::MAX),
            "Event".to_string(),
            &event.payload,
        ),
    };
    Ok(format!(
        "{ROLE_PROMPT}\n{sections}{context}{}# {heading}\n\n{text}",
        history(engine, key, before).await?,
    ))
}

async fn history(
    engine: &Engine,
    (organization, repository, workstream): &ChatKey,
    before: i64,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let messages = engine
        .store
        .chat_messages()
        .before(organization, repository, *workstream, before, HISTORY_SIZE)
        .await?;
    let mut history = "# Chat history\n\n".to_string();
    for message in messages {
        history.push_str(&block(&message)?);
    }
    Ok(history)
}

fn message_prompt(message: &ChatMessage) -> String {
    match message.author {
        Author::Owner => message.text.clone(),
        _ => format!("# {} message\n\n{}", message.author.name(), message.text),
    }
}

fn event_prompt(event: &LeadEvent) -> String {
    format!("# Event\n\n{}", event.payload)
}

fn block(message: &ChatMessage) -> Result<String, time::error::Format> {
    Ok(format!(
        "{} ({}):\n{}\n\n",
        message.author.name(),
        message.time.format(TIME_FORMAT)?,
        message.text
    ))
}
