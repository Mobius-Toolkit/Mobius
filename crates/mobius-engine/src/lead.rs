use std::error::Error;
use std::path::Path;

use mobius_domain::{Author, Live};
use mobius_github::Repository;
use mobius_runner::Session;
use serde_json::{Value, json};
use time::OffsetDateTime;
use tokio::sync::broadcast;
use tokio::sync::mpsc::UnboundedReceiver;

use crate::config::RoleBinding;
use crate::{Engine, agents, mcp, tasks};

pub(crate) const SAVE_PROMPT: &str = "Save in the Workstream memory what the next session needs.";

pub(crate) async fn add_session(
    engine: &Engine,
    role: &str,
    binding: &RoleBinding,
    repository: &str,
    workstream: i64,
) -> Result<i64, Box<dyn Error + Send + Sync>> {
    let session = engine
        .store
        .sessions()
        .add(
            role,
            binding.harness,
            &binding.model,
            repository,
            workstream,
        )
        .await?;
    let id = session.id;
    engine.broadcast(Live::Agent(agents::node(session)));
    Ok(id)
}

pub(crate) async fn end_session(
    engine: &Engine,
    session: i64,
    reason: &str,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let ended = engine.store.sessions().end(session, reason).await?;
    engine.broadcast(Live::Agent(agents::node(ended)));
    Ok(())
}

pub(crate) async fn start(
    engine: &Engine,
    binding: &RoleBinding,
    session_id: i64,
    dir: &Path,
    session_key: &str,
    gh_token_url: Option<&str>,
) -> Result<(Session, UnboundedReceiver<Value>), Box<dyn Error + Send + Sync>> {
    let (mut session, updates) = mobius_runner::start(
        binding.harness,
        dir,
        &engine.config.data_dir,
        &engine.harness_path,
        &mcp::url(engine, session_key),
        gh_token_url,
    )
    .await?;
    engine
        .store
        .sessions()
        .set_acp_session_id(session_id, session.acp_id())
        .await?;
    session
        .configure(&binding.model, binding.effort.as_deref())
        .await?;
    Ok((session, updates))
}

pub(crate) async fn context(
    engine: &Engine,
    dir: &Path,
    repository: &str,
    workstream: i64,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let brief = brief(&engine.repository(repository)?, workstream).await?;
    let memory = mobius_runner::memory(dir)?;
    let tasks = tasks::text(&tasks::list(engine, repository, workstream).await?);
    Ok(format!(
        "# Brief\n\n{brief}\n\n# MEMORY.md\n\n{memory}\n\n# Task list\n\n{tasks}\n\n"
    ))
}

pub(crate) async fn brief(
    repository: &Repository,
    workstream: i64,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    Ok(repository
        .issue(workstream)
        .await?
        .ok_or("The Workstream issue does not exist.")?
        .body
        .unwrap_or_default())
}

pub(crate) struct Recorder {
    engine: Engine,
    session: i64,
    repository: String,
    workstream: i64,
    // The author of the chat messages of the session. With `None`, the text of the session goes only to the transcript.
    chat: Option<Author>,
    // The last transcript row while it is a chunk, and its JSON.
    chunk: Option<(i64, Value)>,
    // The Lead chat message that the text chunks grow until the next prompt or tool call.
    message: Option<i64>,
    // The last `_claude/rateLimit.resetsAt` of a `usage_update` of the session.
    reset_hint: Option<OffsetDateTime>,
}

impl Recorder {
    pub(crate) fn new(
        engine: &Engine,
        session: i64,
        repository: &str,
        workstream: i64,
        chat: Option<Author>,
    ) -> Recorder {
        Recorder {
            engine: engine.clone(),
            session,
            repository: repository.to_string(),
            workstream,
            chat,
            chunk: None,
            message: None,
            reset_hint: None,
        }
    }

    pub(crate) fn engine(&self) -> &Engine {
        &self.engine
    }

    pub(crate) fn chat_key(&self) -> (&str, i64) {
        (&self.repository, self.workstream)
    }

    pub(crate) fn session(&self) -> i64 {
        self.session
    }

    pub(crate) fn reset_hint(&self) -> Option<OffsetDateTime> {
        self.reset_hint
    }

    pub(crate) async fn prompt(&mut self, text: &str) -> Result<(), Box<dyn Error + Send + Sync>> {
        self.chunk = None;
        self.message = None;
        self.engine
            .store
            .transcript()
            .add(self.session, "prompt", &json!({ "text": text }).to_string())
            .await?;
        Ok(())
    }

    pub(crate) async fn update(
        &mut self,
        mut update: Value,
    ) -> Result<(), Box<dyn Error + Send + Sync>> {
        let transcript = self.engine.store.transcript();
        let kind = update["update"]["sessionUpdate"]
            .as_str()
            .unwrap_or_default()
            .to_string();
        // Mobius reads `resetsAt` as Unix seconds.
        if let Some(seconds) = update["update"]["_meta"]["_claude/rateLimit"]["resetsAt"].as_i64() {
            self.reset_hint = OffsetDateTime::from_unix_timestamp(seconds).ok();
        }
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
        let (Some(author), Some(text), "agent_message_chunk") = (self.chat, text, kind.as_str())
        else {
            return Ok(());
        };
        let chat_messages = self.engine.store.chat_messages();
        let message = match self.message {
            Some(id) => chat_messages.append(id, &text).await?,
            None => {
                chat_messages
                    .add(&self.repository, self.workstream, author, &text)
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

    pub(crate) async fn fail(&mut self, error: &str) -> Result<(), Box<dyn Error + Send + Sync>> {
        self.engine
            .store
            .transcript()
            .add(
                self.session,
                "error",
                &json!({ "message": error }).to_string(),
            )
            .await?;
        end_session(&self.engine, self.session, "failed").await
    }
}

// `Engine` holds the sender, so the channel stays open while a Lead session waits.
pub(crate) async fn stopped(
    stops: &mut broadcast::Receiver<(String, i64)>,
    repository: &str,
    workstream: i64,
) {
    loop {
        if stops
            .recv()
            .await
            .is_ok_and(|(name, number)| name == repository && number == workstream)
        {
            return;
        }
    }
}
