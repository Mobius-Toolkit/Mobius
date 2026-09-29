use std::fs;
use std::path::Path;
use std::time::Duration;

use mobius_domain::{Author, ChatMessage, Live, Session, TranscriptRow, Unread};
use mobius_engine::{Engine, activity, chat, github, workstreams};
use mobius_testkit::fake_github::FakeGitHub;
use mobius_testkit::{install_fake_agent, start, wait_for};
use serde_json::Value;
use tempfile::TempDir;

const REPOSITORY: &str = "owner/shop";
const FAKE_AGENT: &str = env!("CARGO_BIN_EXE_fake-agent");
const OPTIONS: &str = r#"
[options]
model = ["sonnet", "opus"]
thought_level = ["low", "high"]
mode = ["default", "bypassPermissions"]
"#;

async fn connect(data_dir: &TempDir, github: &FakeGitHub, script: &str) -> Engine {
    github.add_manifest_code("manifest-code");
    github.add_repository(REPOSITORY);
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    install_fake_agent(data_dir.path(), FAKE_AGENT, script);
    let engine = start(data_dir.path(), "correct horse", &github.url).await;
    github::convert_manifest(&engine, "manifest-code")
        .await
        .unwrap();
    wait_for(async || (!workstreams::list(&engine).await.unwrap().is_empty()).then_some(())).await;
    engine
}

async fn messages(engine: &Engine) -> Vec<ChatMessage> {
    engine
        .store
        .chat_messages()
        .list("owner", REPOSITORY, 12)
        .await
        .unwrap()
}

async fn wait_for_lead_text(engine: &Engine, text: &str) {
    wait_for(async || {
        messages(engine)
            .await
            .iter()
            .any(|message| message.author == Author::Lead && message.text == text)
            .then_some(())
    })
    .await;
}

async fn sessions(engine: &Engine) -> Vec<Session> {
    engine
        .store
        .sessions()
        .list("owner", REPOSITORY, 12)
        .await
        .unwrap()
}

async fn ended_session(engine: &Engine, index: usize) -> Session {
    wait_for(async || {
        sessions(engine)
            .await
            .into_iter()
            .nth(index)
            .filter(|session| session.ended_at.is_some())
    })
    .await
}

async fn transcript(engine: &Engine, session: i64) -> Vec<TranscriptRow> {
    engine.store.transcript().list(session).await.unwrap()
}

fn json(row: &TranscriptRow) -> Value {
    serde_json::from_str(&row.json).unwrap()
}

fn prompts(rows: &[TranscriptRow]) -> Vec<String> {
    rows.iter()
        .filter(|row| row.kind == "prompt")
        .map(|row| json(row)["text"].as_str().unwrap().to_string())
        .collect()
}

fn session_updates(rows: &[TranscriptRow], kind: &str) -> Vec<Value> {
    rows.iter()
        .filter(|row| row.kind == "update")
        .map(|row| json(row)["update"].clone())
        .filter(|update| update["sessionUpdate"] == kind)
        .collect()
}

fn env_value(data_dir: &Path, name: &str) -> String {
    fs::read_to_string(data_dir.join("harnesses/env"))
        .unwrap()
        .lines()
        .find_map(|line| line.strip_prefix(&format!("{name}=")))
        .unwrap()
        .to_string()
}

#[tokio::test]
async fn an_owner_message_gets_the_lead_reply_in_the_store() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = format!("{OPTIONS}\n[[prompts]]\nreply = [\"Hello\", \" there\"]\n");
    let engine = connect(&data_dir, &github, &script).await;

    chat::send(&engine, "owner", REPOSITORY, 12, "Plan the loyalty API")
        .await
        .unwrap();

    wait_for_lead_text(&engine, "Hello there").await;
    let messages = messages(&engine).await;
    assert_eq!(
        messages
            .iter()
            .map(|message| (message.author, message.text.as_str()))
            .collect::<Vec<_>>(),
        [
            (Author::Owner, "Plan the loyalty API"),
            (Author::Lead, "Hello there")
        ]
    );
    let session = ended_session(&engine, 0).await;
    let chunks = session_updates(
        &transcript(&engine, session.id).await,
        "agent_message_chunk",
    );
    assert_eq!(chunks.len(), 1);
    assert_eq!(chunks[0]["content"]["text"], "Hello there");
}

#[tokio::test]
async fn the_harness_process_gets_the_agent_env_guard_in_the_lead_directory() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = format!("{OPTIONS}\n[[prompts]]\nreply = [\"Hello\"]\n");
    let engine = connect(&data_dir, &github, &script).await;

    chat::send(&engine, "owner", REPOSITORY, 12, "Plan the loyalty API")
        .await
        .unwrap();

    wait_for_lead_text(&engine, "Hello").await;
    let data_dir = data_dir.path();
    let agent_env = data_dir.join("agent-env");
    assert_eq!(
        fs::read_to_string(data_dir.join("harnesses/pwd"))
            .unwrap()
            .trim(),
        data_dir
            .join("leads/owner/shop/12")
            .canonicalize()
            .unwrap()
            .display()
            .to_string()
    );
    let gh_config = env_value(data_dir, "GH_CONFIG_DIR");
    assert_eq!(gh_config, agent_env.join("gh-config").display().to_string());
    assert_eq!(fs::read_dir(&gh_config).unwrap().count(), 0);
    assert_eq!(
        env_value(data_dir, "GIT_CONFIG_GLOBAL"),
        agent_env.join("gitconfig").display().to_string()
    );
    assert_eq!(env_value(data_dir, "GIT_TERMINAL_PROMPT"), "0");
    let path = env_value(data_dir, "PATH");
    let first = path.split(':').next().unwrap();
    assert_eq!(first, agent_env.join("chat-bin").display().to_string());
}

#[tokio::test]
async fn the_session_gets_the_model_the_effort_and_the_full_auto_mode() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = format!("{OPTIONS}\n[[prompts]]\nreply = [\"Hello\"]\n");
    let engine = connect(&data_dir, &github, &script).await;

    chat::send(&engine, "owner", REPOSITORY, 12, "Plan the loyalty API")
        .await
        .unwrap();

    let session = ended_session(&engine, 0).await;
    let updates = session_updates(
        &transcript(&engine, session.id).await,
        "config_option_update",
    );
    let values: Vec<(String, String)> = updates.last().unwrap()["configOptions"]
        .as_array()
        .unwrap()
        .iter()
        .map(|option| {
            (
                option["id"].as_str().unwrap().to_string(),
                option["currentValue"].as_str().unwrap().to_string(),
            )
        })
        .collect();
    assert_eq!(
        values,
        [
            ("mode".to_string(), "bypassPermissions".to_string()),
            ("model".to_string(), "opus".to_string()),
            ("thought_level".to_string(), "high".to_string()),
        ]
    );
}

#[tokio::test]
async fn a_refused_model_ends_the_session_before_the_first_prompt() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = OPTIONS.replace("[\"sonnet\", \"opus\"]", "[\"sonnet\", \"haiku\"]");
    let engine = connect(&data_dir, &github, &script).await;

    chat::send(&engine, "owner", REPOSITORY, 12, "Plan the loyalty API")
        .await
        .unwrap();

    let session = ended_session(&engine, 0).await;
    assert_eq!(session.end_reason.as_deref(), Some("failed"));
    let rows = transcript(&engine, session.id).await;
    assert!(prompts(&rows).is_empty());
    let error = rows.iter().find(|row| row.kind == "error").unwrap();
    assert_eq!(
        json(error)["message"],
        "The Harness refuses model \"opus\". The Harness has: sonnet, haiku."
    );
    assert!(
        !chat::view(&engine, "owner", REPOSITORY, 12)
            .await
            .unwrap()
            .writing
    );
}

#[tokio::test]
async fn a_harness_that_needs_a_login_fails_the_session() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = format!("login_required = true\n{OPTIONS}");
    let engine = connect(&data_dir, &github, &script).await;

    chat::send(&engine, "owner", REPOSITORY, 12, "Plan the loyalty API")
        .await
        .unwrap();

    let session = ended_session(&engine, 0).await;
    assert_eq!(session.end_reason.as_deref(), Some("failed"));
    let rows = transcript(&engine, session.id).await;
    let error = rows.iter().find(|row| row.kind == "error").unwrap();
    assert_eq!(json(error)["message"], "Authentication required");
}

#[tokio::test]
async fn the_first_prompt_has_the_context_parts_in_order() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github, OPTIONS).await;
    github.set_body(REPOSITORY, 12, "Ship loyalty plans to all shops.");
    github.add_issue(REPOSITORY, 41, "Add plan model");
    github.add_label(REPOSITORY, 41, "mobius:working", "owner");
    github.add_issue(REPOSITORY, 42, "Old spike");
    github.close_issue(REPOSITORY, 42);
    github.add_issue(REPOSITORY, 43, "Plan API");
    github.add_issue(REPOSITORY, 50, "Billing");
    github.add_label(REPOSITORY, 50, "mobius:workstream", "owner");
    github.add_issue(REPOSITORY, 51, "Invoice totals");
    github.add_sub_issue(REPOSITORY, 12, 41);
    github.add_sub_issue(REPOSITORY, 12, 42);
    github.add_sub_issue(REPOSITORY, 41, 43);
    github.add_sub_issue(REPOSITORY, 12, 50);
    github.add_sub_issue(REPOSITORY, 50, 51);
    let lead_dir = data_dir.path().join("leads/owner/shop/12");
    fs::create_dir_all(&lead_dir).unwrap();
    let memory: Vec<String> = (1..=201).map(|line| format!("note {line}")).collect();
    fs::write(lead_dir.join("MEMORY.md"), memory.join("\n")).unwrap();
    for number in 1..=21 {
        let author = if number % 2 == 1 {
            Author::Owner
        } else {
            Author::Lead
        };
        engine
            .store
            .chat_messages()
            .add(
                "owner",
                REPOSITORY,
                12,
                author,
                &format!("message {number}"),
            )
            .await
            .unwrap();
    }

    chat::send(&engine, "owner", REPOSITORY, 12, "Plan the next step")
        .await
        .unwrap();

    let session = ended_session(&engine, 0).await;
    let prompt = prompts(&transcript(&engine, session.id).await)[0].clone();
    let parts = [
        "You are the Lead of one Workstream.",
        "# Brief\n\nShip loyalty plans to all shops.\n",
        "# MEMORY.md\n\nnote 1\n",
        "note 200\nMEMORY.md is too long. Make it shorter.\n",
        "# Task list\n\n#41 Add plan model: working\n#43 Plan API: open\n\n",
        "# Chat history\n\nLead (",
        "):\nmessage 2\n\n",
        "):\nmessage 21\n\n",
        "# Owner message\n\nPlan the next step",
    ];
    let positions: Vec<usize> = parts
        .iter()
        .map(|part| {
            prompt
                .find(part)
                .unwrap_or_else(|| panic!("{part:?} in {prompt}"))
        })
        .collect();
    assert!(positions.is_sorted(), "{prompt}");
    assert!(prompt.ends_with("Plan the next step"));
    assert!(!prompt.contains("note 201"));
    assert!(!prompt.contains("message 1\n"));
    assert!(!prompt.contains("Old spike"));
    assert!(!prompt.contains("Invoice totals"));
}

#[tokio::test]
async fn a_new_owner_message_waits_for_the_turn_and_stop_ends_the_turn() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = format!(
        "{OPTIONS}\n[[prompts]]\nhang = true\n\n[[prompts]]\nreply = [\"After the stop\"]\n"
    );
    let engine = connect(&data_dir, &github, &script).await;
    chat::send(&engine, "owner", REPOSITORY, 12, "Plan the loyalty API")
        .await
        .unwrap();
    let session = wait_for(async || {
        let session = sessions(&engine).await.into_iter().next()?;
        (prompts(&transcript(&engine, session.id).await).len() == 1).then_some(session)
    })
    .await;

    chat::send(&engine, "owner", REPOSITORY, 12, "Also add a plan price")
        .await
        .unwrap();

    assert!(
        chat::view(&engine, "owner", REPOSITORY, 12)
            .await
            .unwrap()
            .writing
    );
    assert_eq!(prompts(&transcript(&engine, session.id).await).len(), 1);

    chat::stop(&engine, "owner", REPOSITORY, 12).unwrap();

    wait_for_lead_text(&engine, "After the stop").await;
    let session = ended_session(&engine, 0).await;
    assert_eq!(session.end_reason.as_deref(), Some("idle"));
    assert_eq!(
        prompts(&transcript(&engine, session.id).await)[1],
        "Also add a plan price"
    );
}

#[tokio::test]
async fn an_idle_session_saves_and_closes_and_the_next_message_starts_a_new_session() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = format!("{OPTIONS}\n[[prompts]]\nreply = [\"First answer\"]\n");
    let engine = connect(&data_dir, &github, &script).await;
    chat::send(&engine, "owner", REPOSITORY, 12, "Plan the loyalty API")
        .await
        .unwrap();

    let first = ended_session(&engine, 0).await;

    assert_eq!(first.end_reason.as_deref(), Some("idle"));
    assert_eq!(
        prompts(&transcript(&engine, first.id).await)
            .last()
            .unwrap(),
        "Save in the Workstream memory what the next session needs."
    );

    chat::send(&engine, "owner", REPOSITORY, 12, "Add a plan price")
        .await
        .unwrap();

    let second = ended_session(&engine, 1).await;
    let prompt = prompts(&transcript(&engine, second.id).await)[0].clone();
    let history = &prompt[prompt.find("# Chat history").unwrap()..];
    assert!(history.contains("):\nPlan the loyalty API\n"), "{history}");
    assert!(history.contains("):\nFirst answer\n"), "{history}");
    assert!(history.ends_with("# Owner message\n\nAdd a plan price"));
}

#[tokio::test]
async fn a_lead_reply_is_unread_until_the_owner_sees_it() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = format!("{OPTIONS}\n[[prompts]]\nreply = [\"Hello\"]\n");
    let engine = connect(&data_dir, &github, &script).await;
    let mut feed = activity::feed(&engine, None).await.unwrap();

    chat::send(&engine, "owner", REPOSITORY, 12, "Plan the loyalty API")
        .await
        .unwrap();

    wait_for_lead_text(&engine, "Hello").await;
    let unread = Unread {
        organization: "owner".to_string(),
        repository: REPOSITORY.to_string(),
        workstream: 12,
        count: 1,
    };
    assert_eq!(
        engine.store.chat_messages().unread().await.unwrap(),
        std::slice::from_ref(&unread)
    );
    let lead = messages(&engine).await.pop().unwrap();

    chat::seen(&engine, "owner", REPOSITORY, 12, lead.id)
        .await
        .unwrap();

    assert!(
        engine
            .store
            .chat_messages()
            .unread()
            .await
            .unwrap()
            .is_empty()
    );
    let seen = Unread { count: 0, ..unread };
    tokio::time::timeout(Duration::from_secs(5), async {
        while feed.next().await.unwrap() != Live::Unread(seen.clone()) {}
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn a_stop_during_the_session_start_ends_the_first_turn() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = format!("{OPTIONS}\n[[prompts]]\nhang = true\n");
    let engine = connect(&data_dir, &github, &script).await;

    chat::send(&engine, "owner", REPOSITORY, 12, "Plan the loyalty API")
        .await
        .unwrap();
    chat::stop(&engine, "owner", REPOSITORY, 12).unwrap();

    let session = ended_session(&engine, 0).await;
    assert_eq!(session.end_reason.as_deref(), Some("idle"));
}
