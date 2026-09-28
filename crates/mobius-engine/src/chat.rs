use std::collections::VecDeque;
use std::error::Error;
use std::path::Path;

use mobius_domain::{Author, ChatMessage, ChatView, InboxKind, Live};
use mobius_github::Repository;
use mobius_runner::Session;
use serde_json::Value;
use tokio::sync::mpsc::{self, UnboundedReceiver, UnboundedSender};

use crate::lead::{self, Recorder, SAVE_PROMPT};
use crate::{Engine, TIME_FORMAT, gh, inbox, mcp};

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
    repository: &str,
    workstream: i64,
    text: &str,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let message = engine
        .store
        .chat_messages()
        .add(repository, workstream, Author::Owner, text)
        .await?;
    engine.broadcast(Live::Message(message.clone()));
    let mut chats = engine.chats.lock().unwrap();
    let key = (repository.to_string(), workstream);
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
            tokio::spawn(run(engine.clone(), message, receiver));
        }
    }
    engine.broadcast(Live::Lead {
        repository: repository.to_string(),
        workstream,
        writing: true,
        error: None,
    });
    Ok(())
}

pub fn stop(
    engine: &Engine,
    repository: &str,
    workstream: i64,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    if let Some(handle) = engine
        .chats
        .lock()
        .unwrap()
        .get(&(repository.to_string(), workstream))
    {
        handle.commands.send(Command::Stop)?;
    }
    Ok(())
}

pub async fn view(
    engine: &Engine,
    repository: &str,
    workstream: i64,
) -> Result<ChatView, Box<dyn Error + Send + Sync>> {
    let messages = engine
        .store
        .chat_messages()
        .list(repository, workstream)
        .await?;
    let writing = engine
        .chats
        .lock()
        .unwrap()
        .get(&(repository.to_string(), workstream))
        .is_some_and(|handle| handle.writing);
    Ok(ChatView {
        messages,
        writing,
        lead: engine.config.roles.lead.harness,
    })
}

pub async fn seen(
    engine: &Engine,
    repository: &str,
    workstream: i64,
    message: i64,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let chat_messages = engine.store.chat_messages();
    chat_messages
        .set_seen(repository, workstream, message)
        .await?;
    engine.broadcast(Live::Unread(
        chat_messages.unread_of(repository, workstream).await?,
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
    let issue = repository
        .issue(workstream)
        .await?
        .ok_or("The Workstream issue does not exist.")?;
    let chat_messages = engine.store.chat_messages();
    let message = chat_messages
        .add(name, workstream, Author::TellOwner, text)
        .await?;
    engine.broadcast(Live::Message(message));
    engine.broadcast(Live::Unread(
        chat_messages.unread_of(name, workstream).await?,
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

async fn run(engine: Engine, first: ChatMessage, mut commands: UnboundedReceiver<Command>) {
    let (repository, workstream) = (first.repository.clone(), first.workstream);
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
        Err(error) => return finish(&engine, &repository, workstream, Some(error.to_string())),
    };
    let caller = mcp::Caller {
        session,
        role: ROLE,
        repository: repository.clone(),
        workstream,
        cannot_do: None,
        fix: None,
        review: None,
        judge: None,
    };
    let key = match mcp::open(&engine, caller) {
        Ok(key) => key,
        Err(error) => return finish(&engine, &repository, workstream, Some(error.to_string())),
    };
    let mut recorder = Recorder::new(&engine, session, &repository, workstream, true);
    let result = chat(&engine, &first, session, &key, &mut recorder, &mut commands).await;
    mcp::close(&engine, &key);
    match result {
        Ok(()) => {
            if let Err(failure) = lead::end_session(&engine, session, "idle").await {
                eprintln!("mobius: chat session {session}: {failure}");
            }
        }
        Err(error) => {
            let error = error.to_string();
            if let Err(failure) = recorder.fail(&error).await {
                eprintln!("mobius: chat session {session}: {failure}");
            }
            finish(&engine, &repository, workstream, Some(error));
        }
    }
}

fn finish(engine: &Engine, repository: &str, workstream: i64, error: Option<String>) {
    engine
        .chats
        .lock()
        .unwrap()
        .remove(&(repository.to_string(), workstream));
    engine.broadcast(Live::Lead {
        repository: repository.to_string(),
        workstream,
        writing: false,
        error,
    });
}

async fn chat(
    engine: &Engine,
    first: &ChatMessage,
    session_id: i64,
    session_key: &str,
    recorder: &mut Recorder,
    commands: &mut UnboundedReceiver<Command>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let (repository, workstream) = (first.repository.as_str(), first.workstream);
    let key = (repository.to_string(), workstream);
    let dir = mobius_runner::lead_dir(&engine.config.data_dir, repository, workstream)?;
    let prompt = first_prompt(engine, &dir, first).await?;
    let (session, mut updates) = lead::start(
        engine,
        &engine.config.roles.lead,
        session_id,
        &dir,
        session_key,
        Some(&gh::url(engine, session_key)),
    )
    .await?;
    let mut queue = VecDeque::new();
    let mut last = first.id;
    turn(
        &session,
        &prompt,
        recorder,
        &mut updates,
        commands,
        &mut queue,
    )
    .await?;
    loop {
        while let Some(message) = queue.pop_front() {
            let prompt = owner_prompt(engine, &message, &mut last).await?;
            turn(
                &session,
                &prompt,
                recorder,
                &mut updates,
                commands,
                &mut queue,
            )
            .await?;
        }
        {
            let mut chats = engine.chats.lock().unwrap();
            if let Some(handle) = chats.get_mut(&key)
                && commands.is_empty()
            {
                handle.writing = false;
                engine.broadcast(Live::Lead {
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
            None => {
                turn(
                    &session,
                    SAVE_PROMPT,
                    recorder,
                    &mut updates,
                    commands,
                    &mut queue,
                )
                .await?;
                let mut chats = engine.chats.lock().unwrap();
                if queue.is_empty() && commands.is_empty() {
                    chats.remove(&key);
                    break;
                }
            }
        }
    }
    session.close().await;
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
    Ok(result?)
}

async fn first_prompt(
    engine: &Engine,
    dir: &Path,
    first: &ChatMessage,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let context = lead::context(engine, dir, &first.repository, first.workstream).await?;
    let history = engine
        .store
        .chat_messages()
        .before(&first.repository, first.workstream, first.id, HISTORY_SIZE)
        .await?;
    let mut prompt = format!("{ROLE_PROMPT}\n{context}# Chat history\n\n");
    for message in history {
        prompt.push_str(&block(&message)?);
    }
    prompt.push_str(&format!("# Owner message\n\n{}", first.text));
    Ok(prompt)
}

// The session already has each `tell_owner` message with an id up to `last`.
async fn owner_prompt(
    engine: &Engine,
    message: &ChatMessage,
    last: &mut i64,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let told = engine
        .store
        .chat_messages()
        .after(
            &message.repository,
            message.workstream,
            Author::TellOwner,
            *last,
        )
        .await?;
    let Some(newest) = told.last() else {
        return Ok(message.text.clone());
    };
    *last = newest.id;
    let mut prompt = "# Event session messages\n\n".to_string();
    for told in &told {
        prompt.push_str(&block(told)?);
    }
    prompt.push_str(&format!("# Owner message\n\n{}", message.text));
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
