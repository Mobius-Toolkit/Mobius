use std::collections::VecDeque;
use std::error::Error;
use std::path::Path;

use mobius_domain::{Author, ChatMessage, ChatView, Live};
use mobius_runner::Session;
use serde_json::Value;
use tokio::sync::mpsc::{self, UnboundedReceiver, UnboundedSender};

use crate::lead::{self, Recorder, SAVE_PROMPT};
use crate::{Engine, TIME_FORMAT, gh, mcp};

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

async fn run(engine: Engine, first: ChatMessage, mut commands: UnboundedReceiver<Command>) {
    let (repository, workstream) = (first.repository.clone(), first.workstream);
    let session = match lead::add_session(&engine, ROLE, &repository, workstream).await {
        Ok(session) => session,
        Err(error) => return finish(&engine, &repository, workstream, Some(error.to_string())),
    };
    let caller = mcp::Caller {
        session,
        role: ROLE,
        repository: repository.clone(),
        workstream,
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
        session_id,
        &dir,
        session_key,
        Some(&gh::url(engine, session_key)),
    )
    .await?;
    let mut queue = VecDeque::from([prompt]);
    loop {
        while let Some(prompt) = queue.pop_front() {
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
            Some(Command::Prompt(message)) => queue.push_back(message.text),
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
    queue: &mut VecDeque<String>,
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
                    Command::Prompt(message) => queue.push_back(message.text),
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
        let author = message.author.name();
        let time = message.time.format(TIME_FORMAT)?;
        prompt.push_str(&format!("{author} ({time}):\n{}\n\n", message.text));
    }
    prompt.push_str(&format!("# Owner message\n\n{}", first.text));
    Ok(prompt)
}
