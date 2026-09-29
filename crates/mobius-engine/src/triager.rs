use std::error::Error;
use std::fs;
use std::path::Path;
use std::sync::Arc;

use mobius_domain::{ChatMessage, Live, organization};
use mobius_github::Repository;
use tokio::sync::Notify;

use crate::lead::{self, Recorder};
use crate::trust::app_login;
use crate::{
    Engine, NO_WORKSTREAM_LABEL, READY_LABEL, WORKSTREAM_LABEL, drain, limits, mcp, researcher,
    workstreams,
};

pub(crate) const ROLE: &str = "triager";
// The Triager belongs to no Workstream. Its chat belongs to an organization, so the key of the chat is (organization, "", CHAT).
pub(crate) const CHAT: i64 = 0;
const ROLE_PROMPT: &str = include_str!("prompts/triager.md");

// A notice stops the Triager session of one issue.
pub(crate) type Stop = Arc<Notify>;

pub(crate) async fn chat_prompt(
    engine: &Engine,
    first: &ChatMessage,
    history: &str,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let repositories = organization_repositories(engine, &first.organization);
    Ok(format!(
        "{ROLE_PROMPT}\n{}\n{history}# Owner message\n\n{}",
        workstreams(&repositories).await?,
        first.text
    ))
}

async fn workstreams(repositories: &[Repository]) -> Result<String, Box<dyn Error + Send + Sync>> {
    let mut text = "# Open Workstreams\n".to_string();
    for repository in repositories {
        for issue in repository.open_issues_with_label(WORKSTREAM_LABEL).await? {
            text.push_str(&format!(
                "\n#{} {} ({})\n\n{}\n",
                issue.number,
                issue.title,
                repository.full_name,
                issue.body.unwrap_or_default()
            ));
        }
    }
    Ok(text)
}

fn organization_repositories(engine: &Engine, organization: &str) -> Vec<Repository> {
    engine
        .repositories
        .read()
        .unwrap()
        .iter()
        .filter(|repository| repository.full_name.split('/').next() == Some(organization))
        .cloned()
        .collect()
}

// The Triager of the chat has an organization and no repository, so it uses the one repository of the organization.
pub(crate) fn repository(
    engine: &Engine,
    organization: &str,
    repository: &str,
) -> Result<Repository, String> {
    if !repository.is_empty() {
        return engine.repository(repository);
    }
    match organization_repositories(engine, organization).as_slice() {
        [repository] => Ok(repository.clone()),
        _ => Err(format!(
            "The Triager chat needs exactly one repository in {organization}."
        )),
    }
}

// Mobius ignores its own removal of `mobius:no-workstream`, for example in `move_issue`.
pub(crate) async fn stop(
    engine: &Engine,
    app_slug: &str,
    repository: &Repository,
    number: i64,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let key = (repository.full_name.clone(), number);
    if !engine.triages.lock().unwrap().contains_key(&key) {
        return Ok(());
    }
    let events = repository.issue_events(number).await?;
    let by_person = events
        .iter()
        .rev()
        .find(|event| {
            event.event == "unlabeled"
                && event
                    .label
                    .as_ref()
                    .is_some_and(|label| label.name == NO_WORKSTREAM_LABEL)
        })
        .and_then(|event| event.actor.as_ref())
        .is_some_and(|actor| !actor.login.eq_ignore_ascii_case(&app_login(app_slug)));
    if by_person && let Some(stop) = engine.triages.lock().unwrap().get(&key) {
        stop.notify_one();
    }
    Ok(())
}

pub(crate) async fn triage(
    engine: &Engine,
    repository: &Repository,
    number: i64,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    // The drain holds each new Triager. The next poll after a cancel starts it.
    if engine.drain.on() {
        return Ok(());
    }
    let Some(guard) = drain::try_track(engine) else {
        return Ok(());
    };
    repository.add_label(number, NO_WORKSTREAM_LABEL).await?;
    repository.remove_label(number, READY_LABEL).await?;
    let stop = Arc::new(Notify::new());
    engine
        .triages
        .lock()
        .unwrap()
        .insert((repository.full_name.clone(), number), stop.clone());
    tokio::spawn(run(engine.clone(), repository.clone(), number, stop, guard));
    Ok(())
}

async fn run(
    engine: Engine,
    repository: Repository,
    number: i64,
    stop: Stop,
    _drain: drain::Guard,
) {
    if let Err(error) = session(&engine, &repository, number, &stop).await {
        eprintln!(
            "mobius: Triager of {}#{number}: {error}",
            repository.full_name
        );
    }
    engine
        .triages
        .lock()
        .unwrap()
        .remove(&(repository.full_name.clone(), number));
}

// A removal of `mobius:no-workstream` stops the session. When the issue keeps the label, the last message of the Triager goes to the issue as its proposal.
async fn session(
    engine: &Engine,
    repository: &Repository,
    number: i64,
    stop: &Notify,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let name = &repository.full_name;
    let binding = &engine.config.roles.triager;
    let session = lead::add_session(engine, ROLE, binding, organization(name), name, CHAT).await?;
    let mut recorder = Recorder::new(engine, session, organization(name), name, CHAT, None);
    let key = mcp::open(
        engine,
        mcp::Caller {
            session,
            role: ROLE,
            organization: organization(name).to_string(),
            repository: name.clone(),
            workstream: CHAT,
            cannot_do: None,
            fix: None,
            review: None,
            judge: None,
        },
    )?;
    let dir = mobius_runner::scratch_dir(&engine.config.data_dir, session);
    let result = tokio::select! {
        result = propose(engine, repository, number, session, &key, &dir, &mut recorder) => Some(result),
        () = stop.notified() => None,
    };
    mcp::close(engine, &key);
    if dir.exists() {
        fs::remove_dir_all(&dir)?;
    }
    match result {
        None => lead::end_session(engine, session, "stopped").await,
        Some(Ok(proposal)) => {
            lead::end_session(engine, session, "done").await?;
            if !proposal.trim().is_empty()
                && repository
                    .issue(number)
                    .await?
                    .is_some_and(|issue| issue.has_label(NO_WORKSTREAM_LABEL))
            {
                repository.add_comment(number, &proposal).await?;
            }
            Ok(())
        }
        Some(Err(error)) => {
            recorder.fail(&error.to_string()).await?;
            Err(error)
        }
    }
}

async fn propose(
    engine: &Engine,
    repository: &Repository,
    number: i64,
    session_id: i64,
    session_key: &str,
    dir: &Path,
    recorder: &mut Recorder,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let issue = repository
        .issue(number)
        .await?
        .ok_or_else(|| format!("#{number} does not exist."))?;
    let prompt = format!(
        "{ROLE_PROMPT}\n{}\n# Issue\n\n#{number} {}\n\n{}",
        workstreams(std::slice::from_ref(repository)).await?,
        issue.title,
        issue.body.unwrap_or_default()
    );
    fs::create_dir_all(dir)?;
    limits::wait(
        engine,
        engine.config.roles.triager.harness,
        Some(session_id),
    )
    .await?;
    let (session, mut updates) = lead::start(
        engine,
        &engine.config.roles.triager,
        session_id,
        dir,
        session_key,
        None,
    )
    .await?;
    let proposal = researcher::turn(&session, &prompt, recorder, &mut updates).await;
    session.close().await;
    proposal
}

pub(crate) async fn move_issue(
    repository: &Repository,
    number: i64,
    workstream: i64,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let issue = repository
        .issue(number)
        .await?
        .filter(|issue| issue.pull_request.is_none())
        .ok_or_else(|| format!("#{number} is not an issue of {}.", repository.full_name))?;
    if workstreams::workstream_of(repository, number)
        .await?
        .is_some()
    {
        return Err(format!("#{number} is already in a Workstream.").into());
    }
    repository
        .issue(workstream)
        .await?
        .filter(|target| target.state == "open" && target.has_label(WORKSTREAM_LABEL))
        .ok_or_else(|| format!("#{workstream} is not an open Workstream."))?;
    repository.add_sub_issue(workstream, issue.id).await?;
    if issue.has_label(NO_WORKSTREAM_LABEL) {
        repository.remove_label(number, NO_WORKSTREAM_LABEL).await?;
        repository.add_label(number, READY_LABEL).await?;
    }
    Ok(format!("Moved #{number} to the Workstream #{workstream}."))
}

pub(crate) async fn create_workstream(
    engine: &Engine,
    repository: &Repository,
    title: &str,
    brief: &str,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let issue = repository.create_issue(title, brief).await?;
    repository.add_label(issue.number, WORKSTREAM_LABEL).await?;
    engine.broadcast(Live::WorkstreamCreated {
        repository: repository.full_name.clone(),
        number: issue.number,
    });
    Ok(format!("Created the Workstream #{}.", issue.number))
}
