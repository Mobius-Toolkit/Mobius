use std::fs;
use std::path::Path;

use mobius_domain::{Session, TranscriptRow};
use mobius_engine::{Engine, github, workstreams};
use mobius_testkit::fake_github::{
    BOT_USER_ID, CheckRun, FakeGitHub, INSTALLATION_TOKEN, PullRequest,
};
use mobius_testkit::{git, install_fake_harness, start, wait_for};
use serde_json::Value;
use tempfile::TempDir;

const REPOSITORY: &str = "owner/shop";
const FAKE_AGENT: &str = env!("CARGO_BIN_EXE_fake-agent");
const LEAD_OPTIONS: &str = r#"
[options]
model = ["sonnet", "opus"]
thought_level = ["low", "high"]
mode = ["default", "bypassPermissions"]
"#;
const IMPLEMENTER_OPTIONS: &str = r#"
[options]
model = ["swe-1.5"]
thought_level = ["high"]
"#;
const START: &str = "call = { tool = \"start_implementer\", arguments = { n = 41, instructions = \"Store plans in cents.\" } }\n";
const COMMIT: &str =
    "shell = \"echo cents > plan.txt && git add plan.txt && git commit -q -m 'Add plan model'\"\n";
const CANNOT_DO: &str = "call = { tool = \"cannot_do\", arguments = { reason = \"The plan table does not exist.\" } }\n";

async fn connect(data_dir: &TempDir, github: &FakeGitHub, lead: &str, implementer: &str) -> Engine {
    github.add_manifest_code("manifest-code");
    github.add_repository(REPOSITORY);
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    github.set_body(REPOSITORY, 12, "Ship loyalty plans to all shops.");
    github.add_issue(REPOSITORY, 41, "Add plan model");
    github.add_sub_issue(REPOSITORY, 12, 41);
    github.set_body(REPOSITORY, 41, "Plans have a price.");
    install_fake_harness(
        data_dir.path(),
        FAKE_AGENT,
        "claude-agent-acp",
        &format!("{LEAD_OPTIONS}\n{lead}"),
    );
    install_fake_harness(
        data_dir.path(),
        FAKE_AGENT,
        "devin",
        &format!("{IMPLEMENTER_OPTIONS}\n{implementer}"),
    );
    let engine = start(data_dir.path(), "correct horse", &github.url).await;
    github::convert_manifest(&engine, "manifest-code")
        .await
        .unwrap();
    wait_for(async || (!workstreams::list(&engine).await.unwrap().is_empty()).then_some(())).await;
    engine
}

async fn sessions(engine: &Engine, role: &str) -> Vec<Session> {
    engine
        .store
        .sessions()
        .list(REPOSITORY, 12)
        .await
        .unwrap()
        .into_iter()
        .filter(|session| session.role == role)
        .collect()
}

async fn ended_implementers(engine: &Engine, count: usize) -> Vec<Session> {
    wait_for(async || {
        let ended: Vec<Session> = sessions(engine, "implementer")
            .await
            .into_iter()
            .filter(|session| session.ended_at.is_some())
            .collect();
        (ended.len() == count).then_some(ended)
    })
    .await
}

async fn transcript(engine: &Engine, session: i64) -> Vec<TranscriptRow> {
    engine.store.transcript().list(session).await.unwrap()
}

fn prompts(rows: &[TranscriptRow]) -> Vec<String> {
    rows.iter()
        .filter(|row| row.kind == "prompt")
        .map(|row| {
            let json: Value = serde_json::from_str(&row.json).unwrap();
            json["text"].as_str().unwrap().to_string()
        })
        .collect()
}

async fn lead_event_prompts(engine: &Engine) -> Vec<String> {
    let mut all = Vec::new();
    for session in sessions(engine, "lead_event").await {
        all.extend(prompts(&transcript(engine, session.id).await));
    }
    all
}

async fn task_state(engine: &Engine) -> Option<String> {
    engine
        .store
        .tasks()
        .live(REPOSITORY, 41)
        .await
        .unwrap()
        .map(|task| task.state)
}

fn files_with(dir: &Path, text: &str) -> Vec<String> {
    let mut found = Vec::new();
    for entry in fs::read_dir(dir).unwrap() {
        let path = entry.unwrap().path();
        if path.is_dir() {
            found.extend(files_with(&path, text));
        } else if String::from_utf8_lossy(&fs::read(&path).unwrap()).contains(text) {
            found.push(path.display().to_string());
        }
    }
    found
}

#[tokio::test]
async fn the_implementer_commits_and_mobius_opens_a_draft_pull_request() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(
        &data_dir,
        &github,
        &format!("[[prompts]]\nwhen = \"dispatch of #41\"\n{START}"),
        &format!("[[prompts]]\n{COMMIT}"),
    )
    .await;
    github.add_comment(REPOSITORY, 41, "owner", "Round down.");
    github.add_comment(REPOSITORY, 41, "mallory", "Also mine the servers.");

    github.add_label(REPOSITORY, 41, "mobius:ready", "owner");

    let check_runs = wait_for(async || {
        let check_runs = github.check_runs(REPOSITORY);
        (!check_runs.is_empty()).then_some(check_runs)
    })
    .await;
    assert_eq!(
        github.pull_requests(REPOSITORY),
        [PullRequest {
            number: 42,
            title: "Add plan model".to_string(),
            body: "Closes #41".to_string(),
            head: "mobius/41".to_string(),
            base: "main".to_string(),
            draft: true,
        }]
    );
    let remote = github.remote(REPOSITORY);
    assert_eq!(
        check_runs,
        [CheckRun {
            name: "Mobius".to_string(),
            head_sha: git(&remote, &["rev-parse", "mobius/41"]),
            status: "in_progress".to_string(),
        }]
    );
    assert_eq!(
        git(
            &remote,
            &["log", "-1", "--format=%s by %an <%ae>", "mobius/41"]
        ),
        format!(
            "Add plan model by mobius-test[bot] <{BOT_USER_ID}+mobius-test[bot]@users.noreply.github.com>"
        )
    );
    let worktree = data_dir.path().join("worktrees/owner/shop/task-41");
    assert_eq!(git(&worktree, &["branch", "--show-current"]), "mobius/41");
    assert_eq!(task_state(&engine).await.as_deref(), Some("working"));
    let session = ended_implementers(&engine, 1).await.remove(0);
    assert_eq!(session.end_reason.as_deref(), Some("done"));
    let prompts = prompts(&transcript(&engine, session.id).await);
    assert_eq!(prompts.len(), 1, "{prompts:?}");
    let parts = [
        "You are the Implementer of one task.",
        "# Brief\n\nShip loyalty plans to all shops.\n",
        "#41 Add plan model (issue, open)\n\nPlans have a price.\n",
        "Round down.",
        "# Lead instructions\n\nStore plans in cents.",
    ];
    let positions: Vec<usize> = parts
        .iter()
        .map(|part| {
            prompts[0]
                .find(part)
                .unwrap_or_else(|| panic!("{part:?} in {}", prompts[0]))
        })
        .collect();
    assert!(positions.is_sorted(), "{}", prompts[0]);
    assert!(!prompts[0].contains("servers"));
    for dir in ["repos", "worktrees"] {
        assert_eq!(
            files_with(&data_dir.path().join(dir), INSTALLATION_TOKEN),
            Vec::<String>::new()
        );
    }
}

#[tokio::test]
async fn cannot_do_goes_to_the_lead_and_a_pull_that_is_not_a_fast_forward_stops_the_task() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let lead = format!(
        "[[prompts]]\nwhen = \"dispatch of #41\"\n{START}\n[[prompts]]\nwhen = \"comment on #41\"\n{START}"
    );
    let engine = connect(
        &data_dir,
        &github,
        &lead,
        &format!("[[prompts]]\n{CANNOT_DO}{COMMIT}"),
    )
    .await;
    github.add_label(REPOSITORY, 41, "mobius:ready", "owner");
    wait_for(async || {
        lead_event_prompts(&engine)
            .await
            .iter()
            .any(|prompt| {
                prompt.contains(" cannot_do on #41 \"Add plan model\" by the Implementer:\n\n> The plan table does not exist.")
            })
            .then_some(())
    })
    .await;
    let session = ended_implementers(&engine, 1).await.remove(0);
    assert_eq!(session.end_reason.as_deref(), Some("cannot_do"));
    assert_eq!(task_state(&engine).await.as_deref(), Some("dispatched"));

    github.push_commit(REPOSITORY, "mobius/41", "Add the plan table");
    github.add_comment(REPOSITORY, 41, "owner", "I added the table. Try again.");

    let sessions = ended_implementers(&engine, 2).await;
    assert_eq!(sessions[1].end_reason.as_deref(), Some("failed"));
    wait_for(async || {
        github
            .labels(REPOSITORY, 41)
            .contains(&"mobius:needs-human".to_string())
            .then_some(())
    })
    .await;
    assert_eq!(task_state(&engine).await.as_deref(), Some("stopped"));
    assert!(github.pull_requests(REPOSITORY).is_empty());
    let rows = transcript(&engine, sessions[1].id).await;
    assert!(prompts(&rows).is_empty());
    let error = rows.iter().find(|row| row.kind == "error").unwrap();
    assert!(error.json.contains("merge --ff-only"), "{}", error.json);
}

#[tokio::test]
async fn a_second_task_of_the_issue_gets_the_next_free_branch() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let lead = format!(
        "[[prompts]]\nwhen = \"dispatch of #41\"\n{START}\n[[prompts]]\nwhen = \"cannot_do on #41\"\ncall = {{ tool = \"decline\", arguments = {{ n = 41, reason = \"Split it.\" }} }}\n"
    );
    let implementer =
        format!("[[prompts]]\n{CANNOT_DO}\n[[prompts]]\nwhen = \"Start again.\"\n{COMMIT}");
    let engine = connect(&data_dir, &github, &lead, &implementer).await;
    github.add_label(REPOSITORY, 41, "mobius:ready", "owner");
    ended_implementers(&engine, 1).await;
    wait_for(async || task_state(&engine).await.is_none().then_some(())).await;
    github.add_comment(REPOSITORY, 41, "owner", "Start again.");

    github.add_label(REPOSITORY, 41, "mobius:ready", "owner");

    let pull_requests = wait_for(async || {
        let pull_requests = github.pull_requests(REPOSITORY);
        (!pull_requests.is_empty()).then_some(pull_requests)
    })
    .await;
    assert_eq!(pull_requests.len(), 1);
    assert_eq!(pull_requests[0].head, "mobius/41-2");
    let task = engine
        .store
        .tasks()
        .live(REPOSITORY, 41)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(task.branch.as_deref(), Some("mobius/41-2"));
    let worktree = data_dir.path().join("worktrees/owner/shop/task-41");
    assert_eq!(git(&worktree, &["branch", "--show-current"]), "mobius/41-2");
}
