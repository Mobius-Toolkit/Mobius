use std::collections::VecDeque;
use std::error::Error;
use std::fs;
use std::path::Path;

use mobius_domain::{Author, ChatMessage, ChatView, InboxKind, Live, organization};
use mobius_github::Repository;
use mobius_runner::Session;
use mobius_store::NewInboxItem;
use serde_json::Value;
use tokio::sync::broadcast;
use tokio::sync::mpsc::{self, UnboundedReceiver, UnboundedSender};

use crate::lead::{self, Recorder, SAVE_PROMPT};
use crate::{Engine, TIME_FORMAT, gh, inbox, limits, mcp, triager, workers, workstreams};

pub(crate) const ROLE: &str = "lead_chat";
const ROLE_PROMPT: &str = include_str!("prompts/lead.md");
const HISTORY_SIZE: i64 = 20;

pub(crate) struct ChatHandle {
    commands: UnboundedSender<Command>,
    writing: bool,
}

#[derive(Debug)]
enum Command {
    Prompt(ChatMessage),
    Stop,
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
                key,
                ChatHandle {
                    commands,
                    writing: true,
                },
            );
            // The subscription comes before the spawn, so the session gets each stop of its Lead.
            tokio::spawn(run(
                engine.clone(),
                engine.lead_stops.subscribe(),
                message,
                receiver,
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

// After a crash, a new session gets the message of the failed turn as its first message. The queue keeps the later messages.
async fn run(
    engine: Engine,
    mut stops: broadcast::Receiver<(String, i64)>,
    first: ChatMessage,
    mut commands: UnboundedReceiver<Command>,
) {
    let (organization, repository, workstream) = (
        first.organization.clone(),
        first.repository.clone(),
        first.workstream,
    );
    let (role, binding, author) = if workstream == triager::CHAT {
        (triager::ROLE, &engine.config.roles.triager, Author::Triager)
    } else {
        (ROLE, &engine.config.roles.lead, Author::Lead)
    };
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
        // A stop while the session waits ends the chat and frees the place in the queue.
        let slot = tokio::select! {
            slot = workers::session_slot(
                &engine,
                session,
                if workstream == triager::CHAT {
                    workers::Role::Triager
                } else {
                    workers::Role::Lead
                },
            ) => slot,
            () = lead::stopped(&mut stops, &repository, workstream) => {
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
        let mut current = None;
        let result = tokio::select! {
            result = chat(&engine, &first, &key, &mut recorder, &mut commands, &mut queue, &mut current) => result.map(|()| "idle"),
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
        if let Some(message) = current {
            if crashes <= lead::MAX_CRASHES {
                first = message;
                continue;
            }
            if let Err(failure) = failed(&engine, &message).await {
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
    message: &ChatMessage,
) -> Result<(), Box<dyn Error + Send + Sync>> {
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
    first: &ChatMessage,
    session_key: &str,
    recorder: &mut Recorder,
    commands: &mut UnboundedReceiver<Command>,
    queue: &mut VecDeque<ChatMessage>,
    // The message of the turn that runs. A crash in this turn sends the message again in a new session.
    current: &mut Option<ChatMessage>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let (organization, repository, workstream) = (
        first.organization.as_str(),
        first.repository.as_str(),
        first.workstream,
    );
    let key = (organization.to_string(), repository.to_string(), workstream);
    let session_id = recorder.session();
    let triager = workstream == triager::CHAT;
    let data_dir = &engine.config.data_dir;
    let (dir, prompt, binding, gh_url) = if triager {
        let dir = mobius_runner::scratch_dir(data_dir, session_id);
        fs::create_dir_all(&dir)?;
        let prompt = triager::chat_prompt(engine, first, &history(engine, first).await?).await?;
        (dir, prompt, &engine.config.roles.triager, None)
    } else {
        let dir = mobius_runner::lead_dir(data_dir, repository, workstream)?;
        let prompt = first_prompt(engine, &dir, first).await?;
        let url = gh::url(engine, session_key);
        (dir, prompt, &engine.config.roles.lead, Some(url))
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
    let mut last = first.id;
    *current = Some(first.clone());
    turn(&session, &prompt, recorder, &mut updates, commands, queue).await?;
    *current = None;
    loop {
        while let Some(message) = queue.pop_front() {
            let prompt = message_prompt(engine, &message, &mut last).await?;
            *current = Some(message);
            turn(&session, &prompt, recorder, &mut updates, commands, queue).await?;
            *current = None;
        }
        {
            let mut chats = engine.chats.lock().unwrap();
            if let Some(handle) = chats.get_mut(&key)
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
        let idle = tokio::time::timeout(engine.config.lead_idle_timeout, async {
            loop {
                tokio::select! {
                    Some(update) = updates.recv() => recorder.update(update).await?,
                    command = commands.recv() => return Ok::<_, Box<dyn Error + Send + Sync>>(command),
                }
            }
        })
        .await;
        let command = match idle {
            Ok(command) => command?,
            Err(_) => None,
        };
        match command {
            Some(Command::Prompt(message)) => queue.push_back(message),
            Some(Command::Stop) => {}
            // The Triager has no memory to save.
            None => {
                if !triager {
                    turn(
                        &session,
                        SAVE_PROMPT,
                        recorder,
                        &mut updates,
                        commands,
                        queue,
                    )
                    .await?;
                }
                let mut chats = engine.chats.lock().unwrap();
                if queue.is_empty() && commands.is_empty() {
                    chats.remove(&key);
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

async fn turn(
    session: &Session,
    prompt: &str,
    recorder: &mut Recorder,
    updates: &mut UnboundedReceiver<Value>,
    commands: &mut UnboundedReceiver<Command>,
    queue: &mut VecDeque<ChatMessage>,
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
                        Command::Stop => session.cancel(),
                        Command::Prompt(message) => queue.push_back(message),
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
    first: &ChatMessage,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let context = lead::context(engine, dir, &first.repository, first.workstream).await?;
    Ok(format!(
        "{ROLE_PROMPT}\n{context}{}# {} message\n\n{}",
        history(engine, first).await?,
        first.author.name(),
        first.text
    ))
}

async fn history(
    engine: &Engine,
    first: &ChatMessage,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let messages = engine
        .store
        .chat_messages()
        .before(
            &first.organization,
            &first.repository,
            first.workstream,
            first.id,
            HISTORY_SIZE,
        )
        .await?;
    let mut history = "# Chat history\n\n".to_string();
    for message in messages {
        history.push_str(&block(&message)?);
    }
    Ok(history)
}

// The session already has each `tell_owner` message with an id up to `last`.
async fn message_prompt(
    engine: &Engine,
    message: &ChatMessage,
    last: &mut i64,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let told = engine
        .store
        .chat_messages()
        .after(
            &message.organization,
            &message.repository,
            message.workstream,
            Author::TellOwner,
            *last,
        )
        .await?;
    let heading = format!("# {} message\n\n", message.author.name());
    let Some(newest) = told.last() else {
        return Ok(match message.author {
            Author::Owner => message.text.clone(),
            _ => format!("{heading}{}", message.text),
        });
    };
    *last = newest.id;
    let mut prompt = "# Event session messages\n\n".to_string();
    for told in &told {
        prompt.push_str(&block(told)?);
    }
    prompt.push_str(&format!("{heading}{}", message.text));
    Ok(prompt)
}

fn block(message: &ChatMessage) -> Result<String, time::error::Format> {
    Ok(format!(
        "{} ({}):\n{}\n\n",
        message.author.name(),
        message.time.format(TIME_FORMAT)?,
        message.text
    ))
}
