use std::fs;

use mobius_domain::{Harness, InboxKind, Session, TranscriptRow};
use mobius_engine::{Engine, github, inbox, workstreams};
use mobius_store::Store;
use mobius_testkit::fake_github::FakeGitHub;
use mobius_testkit::{install_fake_harness, start, wait_for};
use serde_json::{Value, json};
use tempfile::TempDir;

const REPOSITORY: &str = "owner/shop";
const FAKE_AGENT: &str = env!("CARGO_BIN_EXE_fake-agent");
const PROMPT: &str = "You are the Implementer of one task. Store plans in cents.";
const LEAD: &str = r#"
[options]
model = ["sonnet", "opus"]
thought_level = ["low", "high"]
mode = ["default", "bypassPermissions"]

[[prompts]]
when = "You are the Reviewer"
shell = "true"
"#;
const IMPLEMENTER: &str = r#"
[options]
model = ["swe-1.5"]
thought_level = ["high"]

[[prompts]]
shell = "echo cents > plan.txt && git add plan.txt && git commit -q -m 'Add plan model'"
"#;

fn add_issues(github: &FakeGitHub) {
    github.add_manifest_code("manifest-code");
    github.add_repository(REPOSITORY);
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    github.add_issue(REPOSITORY, 41, "Add plan model");
    github.add_sub_issue(REPOSITORY, 12, 41);
    github.add_label(REPOSITORY, 41, "mobius:working", "mobius-test[bot]");
}

async fn connect(data_dir: &TempDir, github: &FakeGitHub) -> Engine {
    install_fake_harness(data_dir.path(), FAKE_AGENT, "claude-agent-acp", LEAD);
    install_fake_harness(data_dir.path(), FAKE_AGENT, "devin", IMPLEMENTER);
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

async fn prompts(engine: &Engine, session: i64) -> Vec<String> {
    let rows: Vec<TranscriptRow> = engine.store.transcript().list(session).await.unwrap();
    rows.iter()
        .filter(|row| row.kind == "prompt")
        .filter_map(|row| {
            let json: Value = serde_json::from_str(&row.json).unwrap();
            json["text"].as_str().map(str::to_string)
        })
        .collect()
}

#[tokio::test]
async fn a_restart_starts_the_implementer_again_and_the_event_session() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    add_issues(&github);
    // The store of a server that stopped during a turn of the Implementer.
    let old = {
        let store = Store::open(data_dir.path()).await.unwrap();
        let task = store.tasks().add(REPOSITORY, 41, 12).await.unwrap();
        store.tasks().queue(task.id, "dispatched").await.unwrap();
        store
            .tasks()
            .set_state(task.id, "queued", "working")
            .await
            .unwrap();
        let session = store
            .sessions()
            .add("implementer", Harness::Devin, "swe-1.5", REPOSITORY, 12)
            .await
            .unwrap();
        store
            .tasks()
            .set_worker(task.id, "implementer", Some(PROMPT))
            .await
            .unwrap();
        store
            .transcript()
            .add(session.id, "prompt", &json!({ "text": PROMPT }).to_string())
            .await
            .unwrap();
        store
            .lead_events()
            .add(REPOSITORY, 12, "comment", "A comment before the restart.")
            .await
            .unwrap();
        session.id
    };
    let scratch = data_dir.path().join(format!("scratch/{old}"));
    fs::create_dir_all(&scratch).unwrap();

    let engine = connect(&data_dir, &github).await;

    wait_for(async || (!github.pull_requests(REPOSITORY).is_empty()).then_some(())).await;
    let implementers = sessions(&engine, "implementer").await;
    assert_eq!(implementers.len(), 2);
    assert_eq!(implementers[0].id, old);
    assert_eq!(implementers[0].end_reason.as_deref(), Some("restart"));
    assert_eq!(prompts(&engine, implementers[1].id).await, [PROMPT]);
    assert!(!scratch.exists());
    wait_for(async || {
        for session in sessions(&engine, "lead_event").await {
            if prompts(&engine, session.id)
                .await
                .iter()
                .any(|prompt| prompt.contains("A comment before the restart."))
            {
                return Some(());
            }
        }
        None
    })
    .await;
}

#[tokio::test]
async fn a_start_with_an_empty_store_hands_a_working_issue_to_a_human() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    add_issues(&github);

    let engine = connect(&data_dir, &github).await;

    let item = wait_for(async || {
        inbox::list(&engine)
            .await
            .unwrap()
            .into_iter()
            .find(|item| item.kind == InboxKind::Stopped)
    })
    .await;
    assert_eq!(
        item.text,
        "Mobius lost the state of this task. Add mobius:ready to start again."
    );
    assert_eq!(item.issue, 41);
    assert_eq!(item.workstream, 12);
    assert_eq!(item.link, "https://github.com/owner/shop/issues/41");
    assert_eq!(
        github.labels(REPOSITORY, 41),
        ["mobius:needs-human".to_string()]
    );
}
