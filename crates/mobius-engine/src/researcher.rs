use std::error::Error;

use mobius_domain::{Author, organization};
use mobius_runner::Session;
use serde_json::Value;
use time::OffsetDateTime;
use tokio::sync::broadcast::Receiver;
use tokio::sync::mpsc::UnboundedReceiver;

use crate::lead::{self, Recorder};
use crate::{Engine, TIME_FORMAT, chat, lead_events, limits, mcp, workers};

pub(crate) const ROLE: &str = "researcher";
const ROLE_PROMPT: &str = include_str!("prompts/researcher.md");

pub(crate) enum Origin {
    Chat,
    Events,
}

pub(crate) struct Job {
    pub(crate) repository: String,
    pub(crate) workstream: i64,
    pub(crate) question: String,
    pub(crate) origin: Origin,
}

pub(crate) async fn run(engine: Engine, mut stops: Receiver<(String, i64)>, job: Job) {
    if let Err(error) = session(&engine, &mut stops, &job).await {
        eprintln!(
            "mobius: Researcher of {}#{}: {error}",
            job.repository, job.workstream
        );
    }
}

// A stop of the Lead, for example at a close of the Workstream, stops the Researcher with no report. A failed Researcher reports the error.
async fn session(
    engine: &Engine,
    stops: &mut Receiver<(String, i64)>,
    job: &Job,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let binding = &engine.config.roles.researcher;
    let session = lead::add_session(
        engine,
        ROLE,
        binding,
        organization(&job.repository),
        &job.repository,
        job.workstream,
    )
    .await?;
    let mut recorder = Recorder::new(
        engine,
        session,
        organization(&job.repository),
        &job.repository,
        job.workstream,
        None,
    );
    // A stop while the session waits ends the Researcher and frees the place in the queue.
    let slot = tokio::select! {
        slot = workers::session_slot(engine, session, workers::Role::Researcher) => slot,
        () = lead::stopped(stops, &job.repository, job.workstream) => {
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
            organization: organization(&job.repository).to_string(),
            repository: job.repository.clone(),
            workstream: job.workstream,
            cannot_do: None,
            fix: None,
            review: None,
            judge: None,
        },
    )?;
    let result = tokio::select! {
        result = research(engine, job, session, &key, &mut recorder) => Some(result),
        () = lead::stopped(stops, &job.repository, job.workstream) => None,
    };
    mcp::close(engine, &key);
    let data_dir = &engine.config.data_dir;
    let dir = mobius_runner::research_dir(data_dir, &job.repository, session);
    {
        let _git = engine.git.lock().await;
        if dir.exists() {
            mobius_runner::remove_worktree(data_dir, &job.repository, &dir).await?;
        }
    }
    match result {
        None => lead::end_session(engine, session, "stopped").await,
        Some(Ok(report)) => {
            lead::end_session(engine, session, "done").await?;
            deliver(engine, job, &report).await
        }
        Some(Err(error)) => {
            recorder.fail(&error.to_string()).await?;
            deliver(engine, job, &format!("The Researcher failed: {error}")).await?;
            Err(error)
        }
    }
}

async fn deliver(
    engine: &Engine,
    job: &Job,
    report: &str,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    match job.origin {
        Origin::Chat => {
            let text = format!(
                "Report of the Researcher on \"{}\":\n\n{report}",
                job.question
            );
            chat::post(
                engine,
                organization(&job.repository),
                &job.repository,
                job.workstream,
                Author::Researcher,
                &text,
            )
            .await
        }
        Origin::Events => {
            let quoted: Vec<String> = report.lines().map(|line| format!("> {line}")).collect();
            let text = format!(
                "{} report of the Researcher on \"{}\":\n\n{}",
                OffsetDateTime::now_utc().format(TIME_FORMAT)?,
                job.question,
                quoted.join("\n")
            );
            lead_events::add(engine, &job.repository, job.workstream, "research", &text).await
        }
    }
}

async fn research(
    engine: &Engine,
    job: &Job,
    session_id: i64,
    session_key: &str,
    recorder: &mut Recorder,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let data_dir = &engine.config.data_dir;
    let name = &job.repository;
    let repository = engine.repository(name)?;
    let dir = mobius_runner::research_dir(data_dir, name, session_id);
    {
        let _git = engine.git.lock().await;
        mobius_runner::fetch(data_dir, name, &repository.clone_url, repository.token()).await?;
        mobius_runner::add_detached_worktree(
            data_dir,
            name,
            &dir,
            &format!("origin/{}", repository.default_branch),
        )
        .await?;
    }
    let brief = lead::brief(&repository, job.workstream).await?;
    let prompt = format!(
        "{ROLE_PROMPT}\n# Brief\n\n{brief}\n\n# Question\n\n{}",
        job.question
    );
    let (session, mut updates) = lead::start(
        engine,
        &engine.config.roles.researcher,
        session_id,
        &dir,
        session_key,
        None,
    )
    .await?;
    let report = turn(&session, &prompt, recorder, &mut updates).await;
    session.close().await;
    report
}

// Gives the last message of the turn: the text after the last tool call.
pub(crate) async fn turn(
    session: &Session,
    prompt: &str,
    recorder: &mut Recorder,
    updates: &mut UnboundedReceiver<Value>,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    loop {
        recorder.prompt(prompt).await?;
        let mut report = String::new();
        let result = {
            let turn = session.prompt(prompt);
            tokio::pin!(turn);
            loop {
                tokio::select! {
                    biased;
                    result = &mut turn => break result,
                    Some(update) = updates.recv() => {
                        add(&mut report, &update);
                        recorder.update(update).await?;
                    }
                }
            }
        };
        // The connection reads each update of the turn before the answer to the prompt.
        while let Ok(update) = updates.try_recv() {
            add(&mut report, &update);
            recorder.update(update).await?;
        }
        if let Err(error) = &result
            && limits::wait_out(recorder, session.harness(), error).await?
        {
            continue;
        }
        result?;
        return Ok(report);
    }
}

fn add(report: &mut String, update: &Value) {
    match update["update"]["sessionUpdate"].as_str() {
        Some("agent_message_chunk") => {
            if let Some(text) = update["update"]["content"]["text"].as_str() {
                report.push_str(text);
            }
        }
        Some("tool_call" | "tool_call_update") => report.clear(),
        _ => {}
    }
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;

    fn chunk(text: &str) -> Value {
        json!({ "update": { "sessionUpdate": "agent_message_chunk", "content": { "type": "text", "text": text } } })
    }

    #[test]
    fn the_report_is_the_text_after_the_last_tool_call() {
        let mut report = String::new();
        for update in [
            chunk("I read the code."),
            json!({ "update": { "sessionUpdate": "tool_call", "toolCallId": "1" } }),
            json!({ "update": { "sessionUpdate": "tool_call_update", "toolCallId": "1" } }),
            chunk("Plans store "),
            chunk("cents."),
            json!({ "update": { "sessionUpdate": "agent_thought_chunk" } }),
        ] {
            add(&mut report, &update);
        }

        assert_eq!(report, "Plans store cents.");
    }
}
