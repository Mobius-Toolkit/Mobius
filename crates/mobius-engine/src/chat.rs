use std::collections::VecDeque;
use std::error::Error;
use std::path::Path;

use mobius_domain::{Author, ChatMessage, ChatView, Live};
use mobius_runner::Session;
use serde_json::{Value, json};
use tokio::sync::mpsc::{self, UnboundedReceiver, UnboundedSender};

use crate::{Engine, TIME_FORMAT, agents, mcp, tasks, trust};

pub(crate) const ROLE: &str = "lead_chat";
const ROLE_PROMPT: &str = include_str!("prompts/lead.md");
const SAVE_PROMPT: &str = "Save in the Workstream memory what the next session needs.";
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
    let lead = &engine.config.roles.lead;
    let (repository, workstream) = (first.repository.clone(), first.workstream);
    let session = match engine
        .store
        .sessions()
        .add(ROLE, lead.harness, &lead.model, &repository, workstream)
        .await
    {
        Ok(session) => {
            let id = session.id;
            engine.broadcast(Live::Agent(agents::node(session)));
            id
        }
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
    let mut recorder = Recorder {
        engine: engine.clone(),
        session,
        repository: repository.clone(),
        workstream,
        chunk: None,
        message: None,
    };
    let result = chat(
        &engine,
        &first,
        &mcp::url(&engine, &key),
        &mut recorder,
        &mut commands,
    )
    .await;
    mcp::close(&engine, &key);
    match result {
        Ok(()) => match engine.store.sessions().end(session, "idle").await {
            Ok(ended) => engine.broadcast(Live::Agent(agents::node(ended))),
            Err(failure) => eprintln!("mobius: chat session {session}: {failure}"),
        },
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
    mcp_url: &str,
    recorder: &mut Recorder,
    commands: &mut UnboundedReceiver<Command>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let (repository, workstream) = (first.repository.as_str(), first.workstream);
    let key = (repository.to_string(), workstream);
    let lead = &engine.config.roles.lead;
    let dir = mobius_runner::lead_dir(&engine.config.data_dir, repository, workstream)?;
    let prompt = first_prompt(engine, &dir, first).await?;
    let (mut session, mut updates) = mobius_runner::start(
        lead.harness,
        &dir,
        &engine.config.data_dir,
        &engine.harness_path,
        mcp_url,
    )
    .await?;
    engine
        .store
        .sessions()
        .set_acp_session_id(recorder.session, session.acp_id())
        .await?;
    session
        .configure(&lead.model, lead.effort.as_deref())
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
    let github = engine.repository(&first.repository)?;
    let brief = github
        .issue(first.workstream)
        .await?
        .ok_or("The Workstream issue does not exist.")?
        .body
        .unwrap_or_default();
    let memory = mobius_runner::memory(dir)?;
    let trusted = trust::trusted_authors(engine).await?;
    let tasks = tasks::task_list(&github, first.workstream, &trusted).await?;
    let history = engine
        .store
        .chat_messages()
        .before(&first.repository, first.workstream, first.id, HISTORY_SIZE)
        .await?;
    let mut prompt = format!(
        "{ROLE_PROMPT}\n# Brief\n\n{brief}\n\n# MEMORY.md\n\n{memory}\n\n# Task list\n\n{tasks}\n\n# Chat history\n\n"
    );
    for message in history {
        let author = message.author.name();
        let time = message.time.format(TIME_FORMAT)?;
        prompt.push_str(&format!("{author} ({time}):\n{}\n\n", message.text));
    }
    prompt.push_str(&format!("# Owner message\n\n{}", first.text));
    Ok(prompt)
}

struct Recorder {
    engine: Engine,
    session: i64,
    repository: String,
    workstream: i64,
    // The last transcript row while it is a chunk, and its JSON.
    chunk: Option<(i64, Value)>,
    // The Lead chat message that the text chunks grow until the next prompt or tool call.
    message: Option<i64>,
}

impl Recorder {
    async fn prompt(&mut self, text: &str) -> Result<(), Box<dyn Error + Send + Sync>> {
        self.chunk = None;
        self.message = None;
        self.engine
            .store
            .transcript()
            .add(self.session, "prompt", &json!({ "text": text }).to_string())
            .await?;
        Ok(())
    }

    async fn update(&mut self, mut update: Value) -> Result<(), Box<dyn Error + Send + Sync>> {
        let transcript = self.engine.store.transcript();
        let kind = update["update"]["sessionUpdate"]
            .as_str()
            .unwrap_or_default()
            .to_string();
        let text = update["update"]["content"]["text"]
            .as_str()
            .map(str::to_string);
        let chunk = matches!(kind.as_str(), "agent_message_chunk" | "agent_thought_chunk");
        match (&mut self.chunk, &text) {
            (Some((id, last)), Some(text)) if chunk && last["update"]["sessionUpdate"] == kind => {
                let merged = format!(
                    "{}{text}",
                    last["update"]["content"]["text"]
                        .as_str()
                        .unwrap_or_default()
                );
                last["update"]["content"]["text"] = Value::String(merged);
                transcript.set_json(*id, &last.to_string()).await?;
            }
            _ => {
                let id = transcript
                    .add(self.session, "update", &update.to_string())
                    .await?;
                self.chunk = (chunk && text.is_some()).then(|| (id, update.take()));
            }
        }
        if kind == "tool_call" {
            self.message = None;
        }
        let (Some(text), "agent_message_chunk") = (text, kind.as_str()) else {
            return Ok(());
        };
        let chat_messages = self.engine.store.chat_messages();
        let message = match self.message {
            Some(id) => chat_messages.append(id, &text).await?,
            None => {
                chat_messages
                    .add(&self.repository, self.workstream, Author::Lead, &text)
                    .await?
            }
        };
        let new = self.message.is_none();
        self.message = Some(message.id);
        self.engine.broadcast(Live::Message(message));
        if new {
            self.engine.broadcast(Live::Unread(
                chat_messages
                    .unread_of(&self.repository, self.workstream)
                    .await?,
            ));
        }
        Ok(())
    }

    async fn fail(&mut self, error: &str) -> Result<(), Box<dyn Error + Send + Sync>> {
        self.engine
            .store
            .transcript()
            .add(
                self.session,
                "error",
                &json!({ "message": error }).to_string(),
            )
            .await?;
        let ended = self
            .engine
            .store
            .sessions()
            .end(self.session, "failed")
            .await?;
        self.engine.broadcast(Live::Agent(agents::node(ended)));
        Ok(())
    }
}
