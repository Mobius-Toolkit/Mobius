use mobius_domain::{Blocker, Live, TaskLine, TranscriptRow};
use mobius_engine::{Engine, activity, github, tasks, workstreams};
use mobius_testkit::fake_github::FakeGitHub;
use mobius_testkit::{install_fake_harness, start, wait_for};
use serde_json::Value;
use tempfile::TempDir;

const REPOSITORY: &str = "owner/shop";
const APP: &str = "mobius-test[bot]";
const FAKE_AGENT: &str = env!("CARGO_BIN_EXE_fake-agent");
const LEAD: &str = r#"
[options]
model = ["sonnet", "opus"]
thought_level = ["low", "high"]
mode = ["default", "bypassPermissions"]

[[prompts]]
when = "creation of Workstream #12"
call = { tool = "create_issue", arguments = { title = "Add plan model", body = "Plans have a price.", parent = 12, blocked_by = [88], ready = false } }

[[prompts]]
when = "creation of Workstream #13"
call = { tool = "create_issue", arguments = { title = "Add plan price", body = "", parent = 13, blocked_by = [], ready = true } }

[[prompts]]
when = "creation of Workstream #14"
call = { tool = "create_issue", arguments = { title = "Add plan name", body = "", parent = 14, blocked_by = [], ready = true } }
"#;

// The Workstream "Billing" exists before the first poll. Each test adds its Workstream after it, so the Lead gets a creation event.
async fn connect(data_dir: &TempDir, github: &FakeGitHub) -> Engine {
    github.add_manifest_code("manifest-code");
    github.add_repository(REPOSITORY);
    github.add_issue(REPOSITORY, 20, "Billing");
    github.add_label(REPOSITORY, 20, "mobius:workstream", "owner");
    github.add_issue(REPOSITORY, 88, "Invoice totals");
    github.add_sub_issue(REPOSITORY, 20, 88);
    install_fake_harness(data_dir.path(), FAKE_AGENT, "claude-agent-acp", LEAD);
    let engine = start(data_dir.path(), "correct horse", &github.url).await;
    github::convert_manifest(&engine, "manifest-code")
        .await
        .unwrap();
    wait_for(async || (!workstreams::list(&engine).await.unwrap().is_empty()).then_some(())).await;
    engine
}

async fn lead_replies(engine: &Engine, workstream: i64) -> String {
    let mut replies = String::new();
    for session in engine
        .store
        .sessions()
        .list(REPOSITORY, workstream)
        .await
        .unwrap()
    {
        let rows: Vec<TranscriptRow> = engine.store.transcript().list(session.id).await.unwrap();
        for row in rows.iter().filter(|row| row.kind == "update") {
            let json: Value = serde_json::from_str(&row.json).unwrap();
            if let Some(text) = json["update"]["content"]["text"].as_str() {
                replies.push_str(text);
            }
        }
    }
    replies
}

#[tokio::test]
async fn the_lead_creates_a_sub_issue_with_a_blocker_in_another_workstream() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github).await;
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");

    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");

    wait_for(async || (lead_replies(&engine, 12).await == "Created #89.").then_some(())).await;
    assert_eq!(github.sub_issue_numbers(REPOSITORY, 12), [89]);
    assert_eq!(
        github.issue(REPOSITORY, 89),
        (
            "Add plan model".to_string(),
            "Plans have a price.".to_string()
        )
    );
    assert_eq!(github.blocker_numbers(REPOSITORY, 89), [88]);
    assert!(github.labels(REPOSITORY, 89).is_empty());
    assert_eq!(
        tasks::list(&engine, REPOSITORY, 12).await.unwrap(),
        [TaskLine {
            number: 89,
            title: "Add plan model".to_string(),
            state: "open".to_string(),
            url: "https://github.com/owner/shop/issues/89".to_string(),
            blocked_by: vec![Blocker {
                number: 88,
                workstream_title: Some("Billing".to_string()),
            }],
        }]
    );
}

#[tokio::test]
async fn a_ready_issue_needs_autopilot_from_a_trusted_user() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github).await;
    github.add_issue(REPOSITORY, 13, "Plan prices");
    github.add_issue(REPOSITORY, 14, "Plan names");
    github.add_label(REPOSITORY, 14, "mobius:autopilot", "mallory");

    github.add_label(REPOSITORY, 13, "mobius:workstream", "owner");
    github.add_label(REPOSITORY, 14, "mobius:workstream", "owner");

    for workstream in [13, 14] {
        wait_for(async || {
            (lead_replies(&engine, workstream).await
                == "error: ready needs Autopilot on the Workstream issue.")
                .then_some(())
        })
        .await;
        assert!(github.sub_issue_numbers(REPOSITORY, workstream).is_empty());
    }
}

#[tokio::test]
async fn with_autopilot_a_ready_label_of_the_mobius_app_dispatches() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github).await;
    let mut feed = activity::feed(&engine, None).await.unwrap();
    github.add_issue(REPOSITORY, 14, "Plan names");
    github.add_label(REPOSITORY, 14, "mobius:autopilot", "owner");

    github.add_label(REPOSITORY, 14, "mobius:workstream", "owner");

    let dispatched = wait_for(async || {
        engine
            .store
            .events()
            .latest(100)
            .await
            .unwrap()
            .into_iter()
            .find(|row| row.text == "Dispatched \"Add plan name\"")
    })
    .await;
    assert_eq!(dispatched.actor, APP);
    let task = engine
        .store
        .tasks()
        .live(REPOSITORY, 89)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(task.workstream, 14);
    assert!(
        github
            .labels(REPOSITORY, 89)
            .contains(&"mobius:working".to_string())
    );
    tokio::time::timeout(std::time::Duration::from_secs(5), async {
        while !matches!(feed.next().await, Some(Live::Workstreams)) {}
    })
    .await
    .unwrap();
    let list = workstreams::list(&engine).await.unwrap();
    assert!(
        list.iter()
            .any(|workstream| workstream.number == 14 && workstream.autopilot)
    );
}
