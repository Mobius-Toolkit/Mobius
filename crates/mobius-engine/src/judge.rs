use std::error::Error;
use std::time::Instant;

use mobius_domain::organization;
use mobius_github::{PullRequest, Repository};
use mobius_store::Task;
use serde::Deserialize;
use time::OffsetDateTime;
use tokio::sync::broadcast::Receiver;
use tokio::sync::mpsc::{self, UnboundedReceiver};

use crate::labels::NEEDS_HUMAN_LABEL;
use crate::lead::{self, Recorder};
use crate::trust::{self, app_login};
use crate::{
    Engine, TIME_FORMAT, drain, ends, implementer, issues, lead_events, limits, mcp, reviewer,
    threads, workers,
};

pub(crate) const ROLE: &str = "judge";
const ROLE_PROMPT: &str = include_str!("prompts/judge.md");

// An open review thread or a conversation comment of the pull request with a new comment of a trusted user or bot.
pub(crate) struct Item {
    // The id of the first comment of the thread, or of the conversation comment.
    pub(crate) id: i64,
    // The newest comment of the item comes from a trusted bot.
    pub(crate) bot: bool,
    pub(crate) text: String,
    pub(crate) at: OffsetDateTime,
}

#[derive(Clone, Copy, Debug, Deserialize, PartialEq)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum Verdict {
    Fix,
    Question,
    FollowUp,
    Reject,
}

#[derive(Clone, Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct Action {
    pub(crate) verdict: Verdict,
    pub(crate) text: String,
}

#[derive(Clone, Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub(crate) struct ItemVerdicts {
    pub(crate) item: i64,
    pub(crate) actions: Vec<Action>,
}

#[derive(Debug, Default, PartialEq)]
struct Routes {
    // The `fix` and `question` actions of each item.
    round: Vec<(i64, Vec<Action>)>,
    follow_ups: Vec<(i64, String)>,
    rejects: Vec<(i64, String)>,
}

struct Job {
    repository: String,
    workstream: i64,
    task: i64,
    number: i64,
    title: String,
    body: String,
    // The state of the task before the Judge.
    from: String,
    branch: String,
    pull_request: PullRequest,
    items: Vec<Item>,
}

// With no new item, a task in the state `reviewed` with no open thread is ready for review.
pub(crate) async fn check(
    engine: &Engine,
    repository: &Repository,
    task: &Task,
    pull_request: PullRequest,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    // The drain holds each new Judge. The next poll after a cancel starts it.
    if engine.drain.on() {
        return Ok(());
    }
    let app_login = app_login(&repository.app_slug);
    let items = new_items(engine, repository, task, pull_request.number, &app_login).await?;
    // Only a trusted user continues a task that waits for a human.
    if task.state == "needs_human" && items.iter().all(|item| item.bot) {
        return Ok(());
    }
    let Some(newest) = items.iter().map(|item| item.at).max() else {
        engine.quiet.lock().unwrap().remove(&task.id);
        if task.state == "reviewed" {
            ready(engine, repository, task, pull_request, &app_login).await?;
        }
        return Ok(());
    };
    {
        let mut quiet = engine.quiet.lock().unwrap();
        let (seen, since) = quiet.entry(task.id).or_insert((newest, Instant::now()));
        if *seen != newest {
            *seen = newest;
            *since = Instant::now();
        }
        if since.elapsed() < engine.config.review_quiet_period {
            return Ok(());
        }
        quiet.remove(&task.id);
    }
    let issue = repository
        .issue(task.issue)
        .await?
        .ok_or_else(|| format!("#{} does not exist.", task.issue))?;
    // The subscription comes before the state change, so the session gets each stop of the task.
    let stops = engine.stops.subscribe();
    // A drain that starts during this check holds the Judge.
    let Some(guard) = drain::try_track(engine) else {
        return Ok(());
    };
    if !engine
        .store
        .tasks()
        .set_state(task.id, &task.state, "working")
        .await?
    {
        return Ok(());
    }
    engine
        .store
        .tasks()
        .set_worker(task.id, ROLE, Some(&task.state))
        .await?;
    tokio::spawn(run(
        engine.clone(),
        stops,
        Job {
            repository: repository.full_name.clone(),
            workstream: task.workstream,
            task: task.id,
            number: task.issue,
            title: issue.title,
            body: issue.body.unwrap_or_default(),
            from: task.state.clone(),
            branch: task.branch.clone().unwrap_or_default(),
            pull_request,
            items,
        },
        guard,
    ));
    Ok(())
}

async fn new_items(
    engine: &Engine,
    repository: &Repository,
    task: &Task,
    pull_request: i64,
    app_login: &str,
) -> Result<Vec<Item>, Box<dyn Error + Send + Sync>> {
    let trusted = trust::trusted_authors(engine, repository);
    let bot = |login: &str| {
        engine
            .config
            .trusted_bots
            .iter()
            .any(|bot| bot.eq_ignore_ascii_case(login))
    };
    let new = |login: &str, at: OffsetDateTime| {
        trusted(login)
            && !login.eq_ignore_ascii_case(app_login)
            && task.judged_at.is_none_or(|judged_at| at > judged_at)
    };
    let comments = repository.review_comments(pull_request).await?;
    let mut items = Vec::new();
    for thread in repository.review_threads(pull_request).await? {
        if !reviewer::is_open(&thread, &trusted, app_login) {
            continue;
        }
        let Some(root) = comments.iter().find(|comment| comment.id == thread.comment) else {
            continue;
        };
        let Some(newest) = comments
            .iter()
            .filter(|comment| {
                (comment.id == root.id || comment.in_reply_to_id == Some(root.id))
                    && trusted(&comment.user.login)
            })
            .max_by_key(|comment| comment.created_at)
        else {
            continue;
        };
        if new(&newest.user.login, newest.created_at) {
            items.push(Item {
                id: root.id,
                bot: bot(&newest.user.login),
                text: issues::thread(&comments, root, &trusted)?,
                at: newest.created_at,
            });
        }
    }
    for comment in repository.issue_comments(pull_request).await? {
        if new(&comment.user.login, comment.created_at) {
            items.push(Item {
                id: comment.id,
                bot: bot(&comment.user.login),
                text: format!(
                    "\nComment {}:\n{}",
                    comment.id,
                    issues::entry(&comment.user.login, comment.created_at, "", &comment.body)?
                ),
                at: comment.created_at,
            });
        }
    }
    Ok(items)
}

async fn ready(
    engine: &Engine,
    repository: &Repository,
    task: &Task,
    pull_request: PullRequest,
    app_login: &str,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let trusted = trust::trusted_authors(engine, repository);
    if repository
        .review_threads(pull_request.number)
        .await?
        .iter()
        .any(|thread| reviewer::is_open(thread, &trusted, app_login))
    {
        return Ok(());
    }
    let title = repository
        .issue(task.issue)
        .await?
        .ok_or_else(|| format!("#{} does not exist.", task.issue))?
        .title;
    let head = pull_request.head.sha.clone();
    let check_run = repository
        .create_check_run(implementer::CHECK_RUN, &head, "in_progress")
        .await?;
    let job = reviewer::Job {
        repository: repository.full_name.clone(),
        workstream: task.workstream,
        task: task.id,
        number: task.issue,
        title,
        branch: task.branch.clone().unwrap_or_default(),
        pull_request,
        head,
        check_run,
    };
    reviewer::ready_for_review(engine, repository, &job, "reviewed").await
}

// A failed session still marks its items as judged, so the same items start no new Judge.
async fn run(engine: Engine, mut stops: Receiver<i64>, job: Job, _drain: drain::Guard) {
    let Err(error) = session(&engine, &mut stops, &job).await else {
        return;
    };
    if let Some(newest) = job.items.iter().map(|item| item.at).max()
        && let Err(failure) = engine.store.tasks().set_judged_at(job.task, newest).await
    {
        eprintln!(
            "mobius: Judge of {}#{}: {failure}",
            job.repository, job.number
        );
    }
    eprintln!(
        "mobius: Judge of {}#{}: {error}",
        job.repository, job.number
    );
    if let Err(failure) =
        implementer::hand_to_human(&engine, &job.repository, job.task, job.number).await
    {
        eprintln!(
            "mobius: stop of {}#{}: {failure}",
            job.repository, job.number
        );
    }
}

async fn session(
    engine: &Engine,
    stops: &mut Receiver<i64>,
    job: &Job,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let binding = &engine.config.roles.judge;
    let session = lead::add_session(
        engine,
        ROLE,
        binding,
        organization(&job.repository),
        &job.repository,
        job.workstream,
        Some(job.number),
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
    // A stop while the session waits ends the Judge and frees the place in the queue.
    let slot = tokio::select! {
        slot = workers::session_slot(engine, session, workers::Role::Judge) => slot,
        () = ends::stopped(stops, job.task) => {
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
    let (verdicts, mut received) = mpsc::unbounded_channel();
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
            judge: Some(mcp::Judge {
                items: job.items.iter().map(|item| (item.id, item.bot)).collect(),
                verdicts,
            }),
        },
    )?;
    let result = tokio::select! {
        result = judge(engine, job, session, &key, &mut recorder) => result.map(|()| "done"),
        () = ends::stopped(stops, job.task) => Ok("stopped"),
    };
    mcp::close(engine, &key);
    let data_dir = &engine.config.data_dir;
    let dir = mobius_runner::judge_dir(data_dir, &job.repository, session);
    {
        let _git = engine.git.lock().await;
        if dir.exists() {
            mobius_runner::remove_worktree(data_dir, &job.repository, &dir).await?;
        }
    }
    match result {
        Ok("stopped") => lead::end_session(engine, session, "stopped").await,
        Ok(_) => {
            lead::end_session(engine, session, "done").await?;
            route(engine, job, last(&mut received)).await
        }
        Err(error) => {
            recorder.fail(&error.to_string()).await?;
            Err(error)
        }
    }
}

// A later valid call replaces an earlier one.
fn last(received: &mut UnboundedReceiver<Vec<ItemVerdicts>>) -> Option<Vec<ItemVerdicts>> {
    let mut last = None;
    while let Ok(verdicts) = received.try_recv() {
        last = Some(verdicts);
    }
    last
}

async fn judge(
    engine: &Engine,
    job: &Job,
    session_id: i64,
    session_key: &str,
    recorder: &mut Recorder,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let data_dir = &engine.config.data_dir;
    let name = &job.repository;
    let repository = engine.repository(name)?;
    let dir = mobius_runner::judge_dir(data_dir, name, session_id);
    {
        let _git = engine.git.lock().await;
        mobius_runner::fetch(data_dir, name, &repository.clone_url, repository.token()).await?;
        mobius_runner::add_detached_worktree(data_dir, name, &dir, &job.pull_request.head.sha)
            .await?;
    }
    let items: String = job.items.iter().map(|item| item.text.as_str()).collect();
    let prompt = format!(
        "{ROLE_PROMPT}\n# Issue\n\n#{} {}\n\n{}\n\n# Items\n{items}",
        job.number, job.title, job.body
    );
    limits::wait(engine, engine.config.roles.judge.harness, Some(session_id)).await?;
    let (session, mut updates) = lead::start(
        engine,
        &engine.config.roles.judge,
        session_id,
        &dir,
        session_key,
        None,
    )
    .await?;
    let result = lead_events::turn(&session, &prompt, recorder, &mut updates).await;
    session.close().await;
    result
}

async fn route(
    engine: &Engine,
    job: &Job,
    verdicts: Option<Vec<ItemVerdicts>>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let repository = engine.repository(&job.repository)?;
    let tasks = engine.store.tasks();
    let pull_request = job.pull_request.number;
    let routes = routes(&job.items, verdicts.as_deref());
    if let Some(newest) = job.items.iter().map(|item| item.at).max() {
        tasks.set_judged_at(job.task, newest).await?;
    }
    for (id, reason) in &routes.rejects {
        if let Some(target) = threads::target(&repository, pull_request, *id).await? {
            threads::reply(&repository, pull_request, &target, reason, false).await?;
        }
    }
    for (id, text) in &routes.follow_ups {
        let item = job.items.iter().find(|item| item.id == *id);
        let event = format!(
            "{} follow-up on pull request #{pull_request} of #{} \"{}\", item {id}:\n\n> {text}\n{}",
            OffsetDateTime::now_utc().format(TIME_FORMAT)?,
            job.number,
            job.title,
            item.map(|item| item.text.as_str()).unwrap_or_default()
        );
        lead_events::add(engine, &job.repository, job.workstream, "follow_up", &event).await?;
    }
    if routes.round.is_empty() {
        tasks.set_state(job.task, "working", &job.from).await?;
        return Ok(());
    }
    if job.from == "needs_human" {
        repository
            .remove_label(job.number, NEEDS_HUMAN_LABEL)
            .await?;
    }
    let counts = routes
        .round
        .iter()
        .any(|(_, actions)| actions.iter().any(|action| action.verdict == Verdict::Fix));
    implementer::fix_round(
        engine,
        &repository,
        implementer::Round {
            repository: job.repository.clone(),
            workstream: job.workstream,
            task: job.task,
            number: job.number,
            title: job.title.clone(),
            branch: job.branch.clone(),
            pull_request: job.pull_request.clone(),
            check_run: None,
            counts,
            items: round_text(&job.items, &routes.round),
        },
    )
    .await
}

fn round_text(items: &[Item], round: &[(i64, Vec<Action>)]) -> String {
    let mut text = String::new();
    for (id, actions) in round {
        if let Some(item) = items.iter().find(|item| item.id == *id) {
            text.push_str(&item.text);
        }
        for action in actions {
            let verdict = match action.verdict {
                Verdict::Fix => "fix",
                Verdict::Question => "question",
                Verdict::FollowUp => "follow-up",
                Verdict::Reject => "reject",
            };
            if action.text.is_empty() {
                text.push_str(&format!("\nAction: {verdict}\n"));
            } else {
                text.push_str(&format!("\nAction: {verdict}: {}\n", action.text));
            }
        }
    }
    text
}

// With no valid call of `submit_verdicts`, each item goes to the fix round as `fix`.
fn routes(items: &[Item], verdicts: Option<&[ItemVerdicts]>) -> Routes {
    let Some(verdicts) = verdicts else {
        return Routes {
            round: items
                .iter()
                .map(|item| {
                    (
                        item.id,
                        vec![Action {
                            verdict: Verdict::Fix,
                            text: String::new(),
                        }],
                    )
                })
                .collect(),
            ..Routes::default()
        };
    };
    let mut routes = Routes::default();
    for item in verdicts {
        let mut round = Vec::new();
        for action in &item.actions {
            match action.verdict {
                Verdict::Fix | Verdict::Question => round.push(action.clone()),
                Verdict::FollowUp => routes.follow_ups.push((item.item, action.text.clone())),
                Verdict::Reject => routes.rejects.push((item.item, action.text.clone())),
            }
        }
        if !round.is_empty() {
            routes.round.push((item.item, round));
        }
    }
    routes
}

// Each item of the batch needs one entry. Items of trusted users take `fix`, `question`, and `follow-up`, and items of trusted bots take `fix` and `reject`.
pub(crate) fn validate(items: &[(i64, bool)], verdicts: &[ItemVerdicts]) -> Result<(), String> {
    for (id, _) in items {
        if verdicts
            .iter()
            .filter(|verdict| verdict.item == *id)
            .count()
            != 1
        {
            return Err(format!("Give item {id} one time."));
        }
    }
    for verdict in verdicts {
        let Some((_, bot)) = items.iter().find(|(id, _)| *id == verdict.item) else {
            return Err(format!("{} is not an item of this batch.", verdict.item));
        };
        if verdict.actions.is_empty() {
            return Err(format!("Give item {} one action or more.", verdict.item));
        }
        for action in &verdict.actions {
            if action.text.trim().is_empty() {
                return Err(format!(
                    "Each action of item {} needs a text.",
                    verdict.item
                ));
            }
            let refused = matches!(
                (bot, action.verdict),
                (false, Verdict::Reject) | (true, Verdict::Question | Verdict::FollowUp)
            );
            if refused {
                return Err(if *bot {
                    format!(
                        "Item {} is from a trusted bot, so its actions are only fix and reject.",
                        verdict.item
                    )
                } else {
                    format!(
                        "Item {} is from a trusted user, so its actions are only fix, question, and follow-up.",
                        verdict.item
                    )
                });
            }
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn item(id: i64, bot: bool) -> Item {
        Item {
            id,
            bot,
            text: format!("\nThread {id}:\n"),
            at: OffsetDateTime::UNIX_EPOCH,
        }
    }

    fn action(verdict: Verdict, text: &str) -> Action {
        Action {
            verdict,
            text: text.to_string(),
        }
    }

    fn verdicts(item: i64, actions: Vec<Action>) -> ItemVerdicts {
        ItemVerdicts { item, actions }
    }

    #[test]
    fn fix_and_question_go_to_the_round_and_the_other_actions_to_their_routes() {
        let items = [item(1, false), item(2, true)];
        let given = [
            verdicts(
                1,
                vec![
                    action(Verdict::Fix, "Rename the field."),
                    action(Verdict::FollowUp, "Move the parser."),
                    action(Verdict::Question, "Why cents?"),
                ],
            ),
            verdicts(2, vec![action(Verdict::Reject, "The API needs the name.")]),
        ];

        assert_eq!(
            routes(&items, Some(&given)),
            Routes {
                round: vec![(
                    1,
                    vec![
                        action(Verdict::Fix, "Rename the field."),
                        action(Verdict::Question, "Why cents?")
                    ]
                )],
                follow_ups: vec![(1, "Move the parser.".to_string())],
                rejects: vec![(2, "The API needs the name.".to_string())],
            }
        );
    }

    #[test]
    fn with_no_valid_call_each_item_goes_to_the_round_as_fix() {
        let items = [item(1, false), item(2, true)];

        assert_eq!(
            routes(&items, None),
            Routes {
                round: vec![
                    (1, vec![action(Verdict::Fix, "")]),
                    (2, vec![action(Verdict::Fix, "")])
                ],
                ..Routes::default()
            }
        );
    }

    #[test]
    fn a_valid_call_gives_each_item_one_time_with_allowed_actions() {
        let items = [(1, false), (2, true)];

        assert_eq!(
            validate(
                &items,
                &[
                    verdicts(1, vec![action(Verdict::FollowUp, "Move the parser.")]),
                    verdicts(2, vec![action(Verdict::Fix, "Add the test.")])
                ]
            ),
            Ok(())
        );
    }

    #[test]
    fn a_call_with_a_missing_a_repeated_or_an_unknown_item_is_not_valid() {
        let items = [(1, false), (2, false)];
        let fix = || vec![action(Verdict::Fix, "Rename the field.")];

        assert!(validate(&items, &[verdicts(1, fix())]).is_err());
        assert!(
            validate(
                &items,
                &[verdicts(1, fix()), verdicts(1, fix()), verdicts(2, fix())]
            )
            .is_err()
        );
        assert!(
            validate(
                &items,
                &[verdicts(1, fix()), verdicts(2, fix()), verdicts(3, fix())]
            )
            .is_err()
        );
    }

    #[test]
    fn a_bot_item_takes_no_question_or_follow_up_and_a_user_item_takes_no_reject() {
        assert!(
            validate(
                &[(1, true)],
                &[verdicts(1, vec![action(Verdict::Question, "Why?")])]
            )
            .is_err()
        );
        assert!(
            validate(
                &[(1, true)],
                &[verdicts(1, vec![action(Verdict::FollowUp, "Later.")])]
            )
            .is_err()
        );
        assert!(
            validate(
                &[(1, false)],
                &[verdicts(1, vec![action(Verdict::Reject, "No.")])]
            )
            .is_err()
        );
    }

    #[test]
    fn an_action_needs_a_text_and_an_item_needs_an_action() {
        assert!(
            validate(
                &[(1, false)],
                &[verdicts(1, vec![action(Verdict::Fix, " ")])]
            )
            .is_err()
        );
        assert!(validate(&[(1, false)], &[verdicts(1, Vec::new())]).is_err());
    }
}
