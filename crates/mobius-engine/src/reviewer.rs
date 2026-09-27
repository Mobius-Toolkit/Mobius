use std::error::Error;

use mobius_domain::InboxKind;
use mobius_github::{PullRequest, Repository, ReviewThread};
use time::OffsetDateTime;

use crate::lead::{self, Recorder};
use crate::trust::{self, app_login};
use crate::{Engine, TIME_FORMAT, implementer, inbox, issues, lead_events, mcp, workers};

pub(crate) const ROLE: &str = "reviewer";
const ROLE_PROMPT: &str = include_str!("prompts/reviewer.md");

pub(crate) struct Job {
    pub(crate) repository: String,
    pub(crate) workstream: i64,
    pub(crate) task: i64,
    pub(crate) number: i64,
    pub(crate) title: String,
    pub(crate) pull_request: PullRequest,
    pub(crate) head: String,
    pub(crate) check_run: i64,
}

pub(crate) async fn run(engine: Engine, job: Job) {
    let Err(error) = session(&engine, &job).await else {
        return;
    };
    eprintln!(
        "mobius: Reviewer of {}#{}: {error}",
        job.repository, job.number
    );
    if let Err(failure) = implementer::stop(&engine, &job.repository, job.task, job.number).await {
        eprintln!(
            "mobius: stop of {}#{}: {failure}",
            job.repository, job.number
        );
    }
}

async fn session(engine: &Engine, job: &Job) -> Result<(), Box<dyn Error + Send + Sync>> {
    // A task that the Lead declined during the Implementer session gets no Reviewer.
    if !engine.store.tasks().queue(job.task, "working").await? {
        return Ok(());
    }
    let binding = &engine.config.roles.reviewer;
    let session = lead::add_session(engine, ROLE, binding, &job.repository, job.workstream).await?;
    let mut recorder = Recorder::new(engine, session, &job.repository, job.workstream, false);
    let slot = match workers::slot(engine, job.task, session, binding.harness).await {
        Ok(slot) => slot,
        Err(error) => {
            recorder.fail(&error.to_string()).await?;
            return Err(error);
        }
    };
    let Some(_slot) = slot else {
        return lead::end_session(engine, session, "declined").await;
    };
    let key = mcp::open(
        engine,
        mcp::Caller {
            session,
            role: ROLE,
            repository: job.repository.clone(),
            workstream: job.workstream,
            cannot_do: None,
            review: Some(mcp::Review {
                pull_request: job.pull_request.number,
                head: job.head.clone(),
            }),
        },
    )?;
    let result = review(engine, job, session, &key, &mut recorder).await;
    mcp::close(engine, &key);
    if let Err(error) = result {
        recorder.fail(&error.to_string()).await?;
        return Err(error);
    }
    lead::end_session(engine, session, "done").await
}

async fn review(
    engine: &Engine,
    job: &Job,
    session_id: i64,
    session_key: &str,
    recorder: &mut Recorder,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let data_dir = &engine.config.data_dir;
    let name = &job.repository;
    let repository = engine.repository(name)?;
    let dir = mobius_runner::review_dir(data_dir, name, session_id);
    let base = {
        let _git = engine.git.lock().await;
        mobius_runner::fetch(data_dir, name, &repository.clone_url, repository.token()).await?;
        mobius_runner::add_detached_worktree(data_dir, name, &dir, &job.head).await?;
        mobius_runner::merge_base(
            data_dir,
            name,
            &format!("origin/{}", repository.default_branch),
            &job.head,
        )
        .await?
    };
    let brief = lead::brief(&repository, job.workstream).await?;
    let issue = repository
        .issue(job.number)
        .await?
        .ok_or_else(|| format!("#{} does not exist.", job.number))?;
    let trusted = trust::trusted_authors(engine).await?;
    let threads = issues::review_threads(&repository, job.pull_request.number, &trusted).await?;
    let prompt = format!(
        "{ROLE_PROMPT}\n# Brief\n\n{brief}\n\n# Issue\n\n#{} {}\n\n{}\n\n# Commits\n\nBase commit: {base}\nHead commit: {}\n\nThe changes are `git diff {base} {}`.\n\n# Review threads\n{threads}",
        job.number,
        issue.title,
        issue.body.unwrap_or_default(),
        job.head,
        job.head
    );
    let (session, mut updates) = lead::start(
        engine,
        &engine.config.roles.reviewer,
        session_id,
        &dir,
        session_key,
        None,
    )
    .await?;
    let result = lead_events::turn(&session, &prompt, recorder, &mut updates).await;
    session.close().await;
    result?;
    {
        let _git = engine.git.lock().await;
        mobius_runner::remove_worktree(data_dir, name, &dir).await?;
    }
    let app = engine
        .store
        .github_app()
        .get()
        .await?
        .ok_or("The Mobius App does not exist.")?;
    let app_login = app_login(&app.slug);
    if repository
        .review_threads(job.pull_request.number)
        .await?
        .iter()
        .any(|thread| is_open(thread, &trusted, &app_login))
    {
        return Ok(());
    }
    ready_for_review(engine, &repository, job).await
}

async fn ready_for_review(
    engine: &Engine,
    repository: &Repository,
    job: &Job,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    // A task that the Lead declined during the review stays a draft.
    if !engine
        .store
        .tasks()
        .set_state(job.task, "working", "ready_for_review")
        .await?
    {
        return Ok(());
    }
    repository
        .set_check_run_conclusion(job.check_run, "success")
        .await?;
    repository
        .mark_ready_for_review(&job.pull_request.node_id)
        .await?;
    inbox::add(
        engine,
        InboxKind::ReadyForReview,
        &job.repository,
        job.workstream,
        job.number,
        &format!(
            "Pull request #{} of #{} \"{}\" is ready for review.",
            job.pull_request.number, job.number, job.title
        ),
        &job.pull_request.html_url,
    )
    .await?;
    let text = ready_text(OffsetDateTime::now_utc(), job)?;
    lead_events::add(
        engine,
        &job.repository,
        job.workstream,
        "ready_for_review",
        &text,
    )
    .await
}

fn ready_text(time: OffsetDateTime, job: &Job) -> Result<String, time::error::Format> {
    Ok(format!(
        "{} ready for review of #{} \"{}\": pull request #{} {}.",
        time.format(TIME_FORMAT)?,
        job.number,
        job.title,
        job.pull_request.number,
        job.pull_request.html_url
    ))
}

// A thread is open when it is unresolved, a trusted author started it, and its last trusted comment is not a reply of the Mobius App. The first comment of the Mobius App is a finding of the Reviewer.
fn is_open(thread: &ReviewThread, trusted: impl Fn(&str) -> bool, app_login: &str) -> bool {
    let trusted_authors: Vec<&String> = thread
        .authors
        .iter()
        .filter(|author| trusted(author))
        .collect();
    !thread.resolved
        && thread.authors.first() == trusted_authors.first().copied()
        && match trusted_authors.as_slice() {
            [] => false,
            [_] => true,
            [.., last] => !last.eq_ignore_ascii_case(app_login),
        }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn thread(resolved: bool, authors: &[&str]) -> ReviewThread {
        ReviewThread {
            resolved,
            authors: authors.iter().map(|author| author.to_string()).collect(),
        }
    }

    fn open(thread: &ReviewThread) -> bool {
        is_open(thread, |login| login != "mallory", "mobius-app[bot]")
    }

    #[test]
    fn a_finding_of_the_reviewer_with_no_reply_is_open() {
        assert!(open(&thread(false, &["mobius-app[bot]"])));
    }

    #[test]
    fn a_thread_with_a_last_reply_of_the_mobius_app_is_not_open() {
        assert!(!open(&thread(
            false,
            &["mobius-app[bot]", "mobius-app[bot]"]
        )));
        assert!(!open(&thread(false, &["owner", "Mobius-App[bot]"])));
    }

    #[test]
    fn a_comment_of_a_trusted_user_after_a_reply_of_the_mobius_app_is_open() {
        assert!(open(&thread(false, &["owner", "mobius-app[bot]", "owner"])));
    }

    #[test]
    fn a_resolved_thread_is_not_open() {
        assert!(!open(&thread(true, &["owner"])));
    }

    #[test]
    fn a_thread_that_an_untrusted_author_started_is_not_open() {
        assert!(!open(&thread(false, &["mallory", "owner"])));
    }

    #[test]
    fn a_reply_of_an_untrusted_author_does_not_count() {
        assert!(!open(&thread(
            false,
            &["owner", "mobius-app[bot]", "mallory"]
        )));
    }
}
