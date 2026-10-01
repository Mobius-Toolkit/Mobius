use mobius_domain::{Session, TranscriptRow};
use mobius_engine::{Engine, github, workstreams};
use mobius_testkit::fake_github::{CheckRun, CheckRunOutput, FakeGitHub};
use mobius_testkit::{git, install_fake_harness, start_with_config, wait_for};
use serde_json::Value;
use tempfile::TempDir;

const REPOSITORY: &str = "owner/shop";
const FAKE_AGENT: &str = env!("CARGO_BIN_EXE_fake-agent");
const CLAUDE: &str = r#"
[options]
model = ["sonnet", "opus"]
thought_level = ["low", "high"]
mode = ["default", "bypassPermissions"]

[[prompts]]
when = "You are the Reviewer"
shell = "true"

[[prompts]]
when = "dispatch of #41"
call = { tool = "start_implementer", arguments = { n = 41, instructions = "Store plans in cents." } }
"#;
const IMPLEMENTER: &str = r#"
[options]
model = ["swe-1.5"]
thought_level = ["high"]

[[prompts]]
when = "Action: fix"
shell = "echo 'cents per month' > plan.txt && git commit -q -am 'Fix the check'"

[[prompts]]
shell = "echo cents > plan.txt && git add plan.txt && git commit -q -m 'Add plan model'"
"#;

async fn connect(data_dir: &TempDir, github: &FakeGitHub, extra_config: &str) -> Engine {
    github.add_manifest_code("manifest-code");
    github.add_repository(REPOSITORY);
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    github.add_issue(REPOSITORY, 41, "Add plan model");
    github.add_sub_issue(REPOSITORY, 12, 41);
    github.set_body(REPOSITORY, 41, "Plans have a price.");
    install_fake_harness(data_dir.path(), FAKE_AGENT, "claude-agent-acp", CLAUDE);
    install_fake_harness(data_dir.path(), FAKE_AGENT, "devin", IMPLEMENTER);
    let engine =
        start_with_config(data_dir.path(), "correct horse", &github.url, extra_config).await;
    github::convert_manifest(&engine, "manifest-code")
        .await
        .unwrap();
    wait_for(async || (!workstreams::list(&engine).await.unwrap().is_empty()).then_some(())).await;
    engine
}

async fn sessions(engine: &Engine) -> Vec<Session> {
    engine
        .store
        .sessions()
        .list("owner", REPOSITORY, 12)
        .await
        .unwrap()
        .into_iter()
        .filter(|session| session.role == "implementer")
        .collect()
}

async fn round_prompt(engine: &Engine) -> String {
    wait_for(async || {
        let implementers = sessions(engine).await;
        let session = implementers.get(1)?;
        prompts(engine, session.id).await.into_iter().next()
    })
    .await
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

async fn task_state(engine: &Engine) -> Option<String> {
    engine
        .store
        .tasks()
        .live(REPOSITORY, 41)
        .await
        .unwrap()
        .map(|task| task.state)
}

async fn ready_for_review(engine: &Engine, github: &FakeGitHub) -> String {
    github.add_label(REPOSITORY, 41, "mobius:ready", "owner");
    wait_for(async || {
        (task_state(engine).await.as_deref() == Some("ready_for_review")).then_some(())
    })
    .await;
    git(&github.remote(REPOSITORY), &["rev-parse", "mobius/41"])
}

fn check_run(name: &str, head: &str, status: &str, conclusion: Option<&str>) -> CheckRun {
    CheckRun {
        name: name.to_string(),
        head_sha: head.to_string(),
        status: status.to_string(),
        conclusion: conclusion.map(str::to_string),
        output: Some(CheckRunOutput {
            title: format!("{name} title"),
            summary: format!("{name} summary"),
        }),
    }
}

// A fix round that the sentinel starts has only the sentinel in its items.
async fn only_sentinel_round(engine: &Engine, github: &FakeGitHub, head: &str) -> String {
    github.add_check_run(
        REPOSITORY,
        check_run("sentinel", head, "completed", Some("failure")),
    );
    round_prompt(engine).await
}

#[tokio::test]
async fn a_failed_check_run_on_the_head_starts_a_fix_round_with_the_check_and_its_annotations() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github, "").await;
    let head = ready_for_review(&engine, &github).await;

    let id = github.add_check_run(
        REPOSITORY,
        check_run("build", &head, "completed", Some("failure")),
    );
    github.add_annotation(id, "plan.txt", 1, "Store the unit.");

    let round = round_prompt(&engine).await;
    for part in [
        "# Open items\n",
        &format!(
            "Check run \"build\", https://github.com/owner/shop/runs/{id}:\nbuild title\n\nbuild summary\n"
        ),
        "- plan.txt line 1: Store the unit.\n",
        "Action: fix\n",
    ] {
        assert!(round.contains(part), "{part:?} in {round}");
    }
    wait_for(async || {
        (task_state(&engine).await.as_deref() == Some("ready_for_review")).then_some(())
    })
    .await;
    let task = engine
        .store
        .tasks()
        .live(REPOSITORY, 41)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(task.fix_rounds, 1);
    assert_eq!(sessions(&engine).await.len(), 2);
}

#[tokio::test]
async fn a_timed_out_check_run_on_the_head_starts_a_fix_round() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github, "").await;
    let head = ready_for_review(&engine, &github).await;

    github.add_check_run(
        REPOSITORY,
        check_run("build", &head, "completed", Some("timed_out")),
    );

    wait_for(async || (sessions(&engine).await.len() == 2).then_some(())).await;
}

#[tokio::test]
async fn a_failed_check_run_mobius_has_no_effect() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github, "").await;
    let head = ready_for_review(&engine, &github).await;

    github.add_check_run(
        REPOSITORY,
        check_run("Mobius", &head, "completed", Some("failure")),
    );
    let round = only_sentinel_round(&engine, &github, &head).await;

    assert!(round.contains("Check run \"sentinel\""), "{round}");
    assert!(!round.contains("Check run \"Mobius\""), "{round}");
}

#[tokio::test]
async fn a_running_check_run_and_a_passed_check_run_have_no_effect() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github, "").await;
    let head = ready_for_review(&engine, &github).await;

    github.add_check_run(REPOSITORY, check_run("running", &head, "in_progress", None));
    github.add_check_run(
        REPOSITORY,
        check_run("passed", &head, "completed", Some("success")),
    );
    let round = only_sentinel_round(&engine, &github, &head).await;

    assert!(round.contains("Check run \"sentinel\""), "{round}");
    assert!(!round.contains("Check run \"running\""), "{round}");
    assert!(!round.contains("Check run \"passed\""), "{round}");
}

#[tokio::test]
async fn a_failed_check_run_at_max_fix_rounds_hands_the_task_to_a_human() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github, "max_fix_rounds = 0").await;
    let head = ready_for_review(&engine, &github).await;

    github.add_check_run(
        REPOSITORY,
        check_run("build", &head, "completed", Some("failure")),
    );

    wait_for(async || (task_state(&engine).await.as_deref() == Some("needs_human")).then_some(()))
        .await;
    assert!(
        github
            .labels(REPOSITORY, 41)
            .contains(&"mobius:needs-human".to_string())
    );
    assert_eq!(sessions(&engine).await.len(), 1);
}
