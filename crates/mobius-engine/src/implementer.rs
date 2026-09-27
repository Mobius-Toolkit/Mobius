use std::error::Error;
use std::path::Path;
use std::pin::Pin;

use mobius_github::{PullRequest, Repository};
use mobius_runner::{Check, Session};
use serde_json::Value;
use time::OffsetDateTime;
use tokio::sync::mpsc::{self, UnboundedReceiver};

use crate::lead::{self, Recorder};
use crate::trust::{self, app_login};
use crate::{
    Engine, NEEDS_HUMAN_LABEL, TIME_FORMAT, dispatch, issues, lead_events, mcp, reviewer, workers,
};

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
    // A fix round, or a start after `cannot_do` in a fix round, works on the pull request of an earlier session.
    pull_request: Option<PullRequest>,
    prompt: String,
}

enum Outcome {
    Done(Pushed),
    CannotDo(String),
    CheckFailed(String),
}

struct Pushed {
    branch: String,
    pull_request: PullRequest,
    head: String,
    check_run: i64,
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
    let pull_request = match task.pull_request {
        Some(number) => Some(repository.pull_request(number).await?),
        None => None,
    };
    if !engine.store.tasks().queue(task.id, "dispatched").await? {
        return Err(format!("The task of #{number} is {}, not dispatched.", task.state).into());
    }
    let job = Job {
        repository: name.clone(),
        workstream,
        task: task.id,
        number,
        title,
        branch: task.branch,
        pull_request,
        prompt: format!(
            "{ROLE_PROMPT}\n# Brief\n\n{brief}\n\n# Issue\n\n{issue}\n# Lead instructions\n\n{instructions}"
        ),
    };
    tokio::spawn(run(engine.clone(), job));
    Ok(format!("Started an Implementer for #{number}."))
}

// Starts a fix round for the Reviewer findings that start with a comment in `findings`, or stops the task at `max_fix_rounds`.
pub(crate) async fn fix_round(
    engine: &Engine,
    repository: &Repository,
    review: &reviewer::Job,
    findings: &[i64],
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let tasks = engine.store.tasks();
    let max = engine.config.max_fix_rounds;
    if !tasks.add_fix_round(review.task, max).await? {
        if !stop(engine, &review.repository, review.task, review.number).await? {
            return Ok(());
        }
        repository
            .set_check_run_conclusion(review.check_run, "failure")
            .await?;
        let text = stop_text(
            OffsetDateTime::now_utc(),
            review.number,
            &review.title,
            &format!(
                "the review threads stay open after {max} fix rounds. Mobius set the Mobius check to failure and added mobius:needs-human."
            ),
        )?;
        return lead_events::add(engine, &review.repository, review.workstream, "stop", &text)
            .await;
    }
    let brief = lead::brief(repository, review.workstream).await?;
    let issue = repository
        .issue(review.number)
        .await?
        .ok_or_else(|| format!("#{} does not exist.", review.number))?;
    let trusted = trust::trusted_authors(engine).await?;
    let threads =
        issues::fix_threads(repository, review.pull_request.number, findings, &trusted).await?;
    // A task that the Lead declined during the review gets no fix round.
    if !tasks.queue(review.task, "working").await? {
        return Ok(());
    }
    let job = Job {
        repository: review.repository.clone(),
        workstream: review.workstream,
        task: review.task,
        number: review.number,
        title: review.title.clone(),
        branch: Some(review.branch.clone()),
        pull_request: Some(review.pull_request.clone()),
        prompt: format!(
            "{ROLE_PROMPT}\n# Brief\n\n{brief}\n\n# Issue\n\n#{} {}\n\n{}\n\n# Open review threads\n{threads}",
            review.number,
            issue.title,
            issue.body.unwrap_or_default()
        ),
    };
    tokio::spawn(run(engine.clone(), job));
    Ok(())
}

// The future has a named type, because it and the future of `reviewer::run` start each other.
fn run(engine: Engine, job: Job) -> Pin<Box<dyn Future<Output = ()> + Send>> {
    Box::pin(async move {
        let Err(error) = session(&engine, &job).await else {
            return;
        };
        eprintln!(
            "mobius: Implementer of {}#{}: {error}",
            job.repository, job.number
        );
        if let Err(failure) = stop(&engine, &job.repository, job.task, job.number).await {
            eprintln!(
                "mobius: stop of {}#{}: {failure}",
                job.repository, job.number
            );
        }
    })
}

// Gives `false` when the task is not queued or working, for example after a decline of the Lead.
pub(crate) async fn stop(
    engine: &Engine,
    repository: &str,
    task: i64,
    number: i64,
) -> Result<bool, Box<dyn Error + Send + Sync>> {
    let tasks = engine.store.tasks();
    if !tasks.set_state(task, "working", "stopped").await?
        && !tasks.set_state(task, "queued", "stopped").await?
    {
        return Ok(false);
    }
    engine
        .repository(repository)?
        .add_label(number, NEEDS_HUMAN_LABEL)
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
    let (replies, mut held) = mpsc::unbounded_channel();
    let key = mcp::open(
        engine,
        mcp::Caller {
            session,
            role: ROLE,
            repository: job.repository.clone(),
            workstream: job.workstream,
            cannot_do: Some(cannot_do),
            fix: job.pull_request.as_ref().map(|pull_request| mcp::Fix {
                pull_request: pull_request.number,
                replies,
            }),
            review: None,
        },
    )?;
    let result = implement(
        engine,
        job,
        session,
        &key,
        &mut recorder,
        &mut reasons,
        &mut held,
    )
    .await;
    mcp::close(engine, &key);
    match result {
        Ok(Outcome::Done(pushed)) => {
            lead::end_session(engine, session, "done").await?;
            tokio::spawn(reviewer::run(
                engine.clone(),
                reviewer::Job {
                    repository: job.repository.clone(),
                    workstream: job.workstream,
                    task: job.task,
                    number: job.number,
                    title: job.title.clone(),
                    branch: pushed.branch,
                    pull_request: pushed.pull_request,
                    head: pushed.head,
                    check_run: pushed.check_run,
                },
            ));
            Ok(())
        }
        Ok(Outcome::CheckFailed(_)) => {
            lead::end_session(engine, session, "check_failed").await?;
            if !stop(engine, &job.repository, job.task, job.number).await? {
                return Ok(());
            }
            let text = stop_text(
                OffsetDateTime::now_utc(),
                job.number,
                &job.title,
                &format!(
                    ".mobius/check failed {} times. Mobius pushed the work to a draft pull request and added mobius:needs-human.",
                    engine.config.max_check_attempts
                ),
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
    held: &mut UnboundedReceiver<mcp::Reply>,
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
    let log = match outcome? {
        Some(Outcome::CheckFailed(log)) => Some(log),
        Some(outcome) => return Ok(outcome),
        None => None,
    };
    let repository = engine.repository(name)?;
    let sha = {
        let _git = engine.git.lock().await;
        mobius_runner::push(data_dir, &worktree, repository.token(), &branch).await?
    };
    let pull_request = match &job.pull_request {
        Some(pull_request) => pull_request.clone(),
        None => {
            let pull_request = repository
                .create_draft_pull_request(
                    &job.title,
                    &branch,
                    &repository.default_branch,
                    &format!("Closes #{}", job.number),
                )
                .await?;
            engine
                .store
                .tasks()
                .set_pull_request(job.task, pull_request.number)
                .await?;
            pull_request
        }
    };
    while let Ok(reply) = held.try_recv() {
        repository
            .reply_to_review_comment(pull_request.number, reply.comment, &reply.text)
            .await?;
        if reply.resolve {
            repository.resolve_review_thread(&reply.thread).await?;
        }
    }
    let Some(log) = log else {
        let check_run = repository
            .create_check_run(CHECK_RUN, &sha, "in_progress")
            .await?;
        return Ok(Outcome::Done(Pushed {
            branch,
            pull_request,
            head: sha,
            check_run,
        }));
    };
    let summary = format!(
        "`.mobius/check` failed {} times. The last output ends with these lines:\n\n```\n{log}\n```",
        engine.config.max_check_attempts
    );
    repository
        .create_failed_check_run(CHECK_RUN, &sha, "Local check failed", &summary)
        .await?;
    Ok(Outcome::CheckFailed(log))
}

// Gives `None` when the local check passes.
async fn turns_and_checks(
    engine: &Engine,
    job: &Job,
    worktree: &Path,
    session: &Session,
    updates: &mut UnboundedReceiver<Value>,
    recorder: &mut Recorder,
    reasons: &mut UnboundedReceiver<String>,
) -> Result<Option<Outcome>, Box<dyn Error + Send + Sync>> {
    let mut prompt = job.prompt.clone();
    let mut attempts = 0;
    loop {
        if let Some(reason) = turn(session, &prompt, updates, recorder, reasons).await? {
            return Ok(Some(Outcome::CannotDo(reason)));
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
            return Ok(None);
        };
        attempts += 1;
        let log = tail(&log);
        if attempts >= engine.config.max_check_attempts {
            return Ok(Some(Outcome::CheckFailed(log)));
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
    number: i64,
    title: &str,
    reason: &str,
) -> Result<String, time::error::Format> {
    Ok(format!(
        "{} stop of #{number} \"{title}\": {reason}",
        time.format(TIME_FORMAT)?
    ))
}
