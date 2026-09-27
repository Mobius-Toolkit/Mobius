use std::error::Error;
use std::path::Path;

use mobius_github::Repository;
use mobius_runner::{Check, Session};
use serde_json::Value;
use time::OffsetDateTime;
use tokio::sync::mpsc::{self, UnboundedReceiver};

use crate::lead::{self, Recorder};
use crate::trust::{self, app_login};
use crate::{Engine, NEEDS_HUMAN_LABEL, TIME_FORMAT, dispatch, issues, lead_events, mcp, workers};

pub(crate) const ROLE: &str = "implementer";
const ROLE_PROMPT: &str = include_str!("prompts/implementer.md");
const CHECK_RUN: &str = "Mobius";
// GitHub allows a maximum of 65535 characters in the summary of a check run.
const LOG_TAIL: usize = 60_000;

struct Job {
    repository: String,
    workstream: i64,
    task: i64,
    number: i64,
    title: String,
    branch: Option<String>,
    prompt: String,
}

enum Outcome {
    Done,
    CannotDo(String),
    CheckFailed(String),
}

pub(crate) async fn start(
    engine: &Engine,
    repository: &Repository,
    workstream: i64,
    number: i64,
    instructions: &str,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let name = &repository.full_name;
    let task = dispatch::live_task(engine, name, workstream, number).await?;
    let brief = lead::brief(repository, workstream).await?;
    let title = repository
        .issue(number)
        .await?
        .ok_or_else(|| format!("#{number} does not exist."))?
        .title;
    let trusted = trust::trusted_authors(engine).await?;
    let issue = issues::read_issue(repository, number, &trusted).await?;
    if !engine.store.tasks().queue(task.id).await? {
        return Err(format!("The task of #{number} is {}, not dispatched.", task.state).into());
    }
    let job = Job {
        repository: name.clone(),
        workstream,
        task: task.id,
        number,
        title,
        branch: task.branch,
        prompt: format!(
            "{ROLE_PROMPT}\n# Brief\n\n{brief}\n\n# Issue\n\n{issue}\n# Lead instructions\n\n{instructions}"
        ),
    };
    tokio::spawn(run(engine.clone(), job));
    Ok(format!("Started an Implementer for #{number}."))
}

async fn run(engine: Engine, job: Job) {
    let Err(error) = session(&engine, &job).await else {
        return;
    };
    eprintln!(
        "mobius: Implementer of {}#{}: {error}",
        job.repository, job.number
    );
    if let Err(failure) = stop(&engine, &job).await {
        eprintln!(
            "mobius: stop of {}#{}: {failure}",
            job.repository, job.number
        );
    }
}

// Gives `false` when the task is not queued or working, for example after a decline of the Lead.
async fn stop(engine: &Engine, job: &Job) -> Result<bool, Box<dyn Error + Send + Sync>> {
    let tasks = engine.store.tasks();
    if !tasks.set_state(job.task, "working", "stopped").await?
        && !tasks.set_state(job.task, "queued", "stopped").await?
    {
        return Ok(false);
    }
    engine
        .repository(&job.repository)?
        .add_label(job.number, NEEDS_HUMAN_LABEL)
        .await?;
    Ok(true)
}

async fn session(engine: &Engine, job: &Job) -> Result<(), Box<dyn Error + Send + Sync>> {
    let session = lead::add_session(
        engine,
        ROLE,
        &engine.config.roles.implementer,
        &job.repository,
        job.workstream,
    )
    .await?;
    let mut recorder = Recorder::new(engine, session, &job.repository, job.workstream, false);
    let harness = engine.config.roles.implementer.harness;
    let slot = match workers::slot(engine, job.task, session, harness).await {
        Ok(slot) => slot,
        Err(error) => {
            recorder.fail(&error.to_string()).await?;
            return Err(error);
        }
    };
    // The Implementer keeps its slot until this function returns, also while its check waits and runs.
    let Some(_slot) = slot else {
        return lead::end_session(engine, session, "declined").await;
    };
    let (cannot_do, mut reasons) = mpsc::unbounded_channel();
    let key = mcp::open(
        engine,
        mcp::Caller {
            session,
            role: ROLE,
            repository: job.repository.clone(),
            workstream: job.workstream,
            cannot_do: Some(cannot_do),
        },
    )?;
    let result = implement(engine, job, session, &key, &mut recorder, &mut reasons).await;
    mcp::close(engine, &key);
    match result {
        Ok(Outcome::Done) => lead::end_session(engine, session, "done").await,
        Ok(Outcome::CheckFailed(_)) => {
            lead::end_session(engine, session, "check_failed").await?;
            if !stop(engine, job).await? {
                return Ok(());
            }
            let text = stop_text(
                OffsetDateTime::now_utc(),
                job,
                engine.config.max_check_attempts,
            )?;
            lead_events::add(engine, &job.repository, job.workstream, "stop", &text).await
        }
        Ok(Outcome::CannotDo(reason)) => {
            lead::end_session(engine, session, "cannot_do").await?;
            // A task that the Lead declined during the turn gets no event.
            if !engine
                .store
                .tasks()
                .set_state(job.task, "working", "dispatched")
                .await?
            {
                return Ok(());
            }
            let text = cannot_do_text(OffsetDateTime::now_utc(), job, &reason)?;
            lead_events::add(engine, &job.repository, job.workstream, "cannot_do", &text).await
        }
        Err(error) => {
            recorder.fail(&error.to_string()).await?;
            Err(error)
        }
    }
}

async fn implement(
    engine: &Engine,
    job: &Job,
    session_id: i64,
    session_key: &str,
    recorder: &mut Recorder,
    reasons: &mut UnboundedReceiver<String>,
) -> Result<Outcome, Box<dyn Error + Send + Sync>> {
    let data_dir = &engine.config.data_dir;
    let name = &job.repository;
    let worktree = mobius_runner::task_dir(data_dir, name, job.number);
    // A new branch is free on `origin`, so only a branch of an earlier session needs a pull.
    let branch = match &job.branch {
        Some(branch) => {
            let _git = engine.git.lock().await;
            let repository = engine.repository(name)?;
            mobius_runner::fetch(data_dir, name, &repository.clone_url, repository.token()).await?;
            mobius_runner::pull(data_dir, &worktree, branch).await?;
            branch.clone()
        }
        None => {
            let app = engine
                .store
                .github_app()
                .get()
                .await?
                .ok_or("The Mobius App does not exist.")?;
            let login = app_login(&app.slug);
            let id = engine.github.user_id(&login).await?;
            let _git = engine.git.lock().await;
            let repository = engine.repository(name)?;
            mobius_runner::fetch(data_dir, name, &repository.clone_url, repository.token()).await?;
            let branch = mobius_runner::add_worktree(
                data_dir,
                name,
                job.number,
                &repository.default_branch,
                &login,
                &format!("{id}+{login}@users.noreply.github.com"),
            )
            .await?;
            engine.store.tasks().set_branch(job.task, &branch).await?;
            branch
        }
    };
    let (session, mut updates) = lead::start(
        engine,
        &engine.config.roles.implementer,
        session_id,
        &worktree,
        session_key,
        None,
    )
    .await?;
    let outcome = turns_and_checks(
        engine,
        job,
        &worktree,
        &session,
        &mut updates,
        recorder,
        reasons,
    )
    .await;
    session.close().await;
    let outcome = outcome?;
    if let Outcome::CannotDo(_) = outcome {
        return Ok(outcome);
    }
    let repository = engine.repository(name)?;
    let sha = {
        let _git = engine.git.lock().await;
        mobius_runner::push(data_dir, &worktree, repository.token(), &branch).await?
    };
    repository
        .create_draft_pull_request(
            &job.title,
            &branch,
            &repository.default_branch,
            &format!("Closes #{}", job.number),
        )
        .await?;
    match &outcome {
        Outcome::CheckFailed(log) => {
            let summary = format!(
                "`.mobius/check` failed {} times. The last output ends with these lines:\n\n```\n{log}\n```",
                engine.config.max_check_attempts
            );
            repository
                .create_failed_check_run(CHECK_RUN, &sha, "Local check failed", &summary)
                .await?;
        }
        _ => {
            repository
                .create_check_run(CHECK_RUN, &sha, "in_progress")
                .await?;
        }
    }
    Ok(outcome)
}

async fn turns_and_checks(
    engine: &Engine,
    job: &Job,
    worktree: &Path,
    session: &Session,
    updates: &mut UnboundedReceiver<Value>,
    recorder: &mut Recorder,
    reasons: &mut UnboundedReceiver<String>,
) -> Result<Outcome, Box<dyn Error + Send + Sync>> {
    let mut prompt = job.prompt.clone();
    let mut attempts = 0;
    loop {
        if let Some(reason) = turn(session, &prompt, updates, recorder, reasons).await? {
            return Ok(Outcome::CannotDo(reason));
        }
        let check = {
            let _check = engine.checks.acquire().await?;
            mobius_runner::check(
                &engine.config.data_dir,
                worktree,
                &engine.harness_path,
                engine.config.check_timeout,
            )
            .await?
        };
        let Check::Failed(log) = check else {
            return Ok(Outcome::Done);
        };
        attempts += 1;
        let log = tail(&log);
        if attempts >= engine.config.max_check_attempts {
            return Ok(Outcome::CheckFailed(log));
        }
        prompt = format!(
            "The local check `.mobius/check` failed. Fix the code and commit your work. The output ends with these lines:\n\n```\n{log}\n```"
        );
    }
}

// Gives the reason when the Implementer calls `cannot_do`.
async fn turn(
    session: &Session,
    prompt: &str,
    updates: &mut UnboundedReceiver<Value>,
    recorder: &mut Recorder,
    reasons: &mut UnboundedReceiver<String>,
) -> Result<Option<String>, Box<dyn Error + Send + Sync>> {
    recorder.prompt(prompt).await?;
    let mut reason = None;
    let result = {
        let turn = session.prompt(prompt);
        tokio::pin!(turn);
        loop {
            tokio::select! {
                biased;
                result = &mut turn => break result,
                Some(update) = updates.recv() => recorder.update(update).await?,
                Some(text) = reasons.recv() => {
                    session.cancel();
                    reason = Some(text);
                }
            }
        }
    };
    // The connection reads each update of the turn before the answer to the prompt.
    while let Ok(update) = updates.try_recv() {
        recorder.update(update).await?;
    }
    // With `biased`, the end of the turn wins over a reason that arrived just before it.
    if let Some(reason) = reason.or_else(|| reasons.try_recv().ok()) {
        return Ok(Some(reason));
    }
    result?;
    Ok(None)
}

fn tail(log: &str) -> String {
    let count = log.chars().count();
    log.chars().skip(count.saturating_sub(LOG_TAIL)).collect()
}

fn cannot_do_text(
    time: OffsetDateTime,
    job: &Job,
    reason: &str,
) -> Result<String, time::error::Format> {
    let quoted: Vec<String> = reason.lines().map(|line| format!("> {line}")).collect();
    Ok(format!(
        "{} cannot_do on #{} \"{}\" by the Implementer:\n\n{}",
        time.format(TIME_FORMAT)?,
        job.number,
        job.title,
        quoted.join("\n")
    ))
}

fn stop_text(
    time: OffsetDateTime,
    job: &Job,
    attempts: u32,
) -> Result<String, time::error::Format> {
    Ok(format!(
        "{} stop of #{} \"{}\": .mobius/check failed {attempts} times. Mobius pushed the work to a draft pull request and added mobius:needs-human.",
        time.format(TIME_FORMAT)?,
        job.number,
        job.title
    ))
}
