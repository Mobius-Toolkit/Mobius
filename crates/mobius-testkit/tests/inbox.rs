use std::fs;
use std::time::Duration;

use mobius_domain::{Author, ChatMessage, InboxKind, Live, Session, TranscriptRow, Unread};
use mobius_engine::{Engine, activity, chat, github, inbox, workstreams};
use mobius_testkit::fake_github::FakeGitHub;
use mobius_testkit::{install_fake_agent, start, wait_for};
use rmcp::ServiceExt;
use rmcp::model::CallToolRequestParams;
use rmcp::transport::StreamableHttpClientTransport;
use serde_json::{Value, json};
use tempfile::TempDir;

const REPOSITORY: &str = "owner/shop";
const FAKE_AGENT: &str = env!("CARGO_BIN_EXE_fake-agent");
const OPTIONS: &str = r#"
[options]
model = ["sonnet", "opus"]
thought_level = ["low", "high"]
mode = ["default", "bypassPermissions"]
"#;
const APP: &str = "mobius-test[bot]";

async fn connect(data_dir: &TempDir, github: &FakeGitHub, prompts: &str) -> Engine {
    github.add_manifest_code("manifest-code");
    github.add_repository(REPOSITORY);
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    install_fake_agent(
        data_dir.path(),
        FAKE_AGENT,
        &format!("{OPTIONS}\n{prompts}"),
    );
    let engine = start(data_dir.path(), "correct horse", &github.url).await;
    github::convert_manifest(&engine, "manifest-code")
        .await
        .unwrap();
    wait_for(async || (!workstreams::list(&engine).await.unwrap().is_empty()).then_some(())).await;
    engine
}

fn dispatch(github: &FakeGitHub, number: i64, title: &str) {
    github.add_issue(REPOSITORY, number, title);
    github.add_sub_issue(REPOSITORY, 12, number);
    github.add_label(REPOSITORY, number, "mobius:ready", "owner");
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

async fn all_prompts(engine: &Engine, role: &str) -> Vec<String> {
    let mut all = Vec::new();
    for session in sessions(engine, role).await {
        all.extend(prompts(&transcript(engine, session.id).await));
    }
    all
}

async fn mcp_results(engine: &Engine, role: &str) -> Vec<Value> {
    let mut results = Vec::new();
    for session in sessions(engine, role).await {
        for row in transcript(engine, session.id).await {
            if row.kind == "mcp_call" {
                let call: Value = serde_json::from_str(&row.json).unwrap();
                results.push(call);
            }
        }
    }
    results
}

async fn messages(engine: &Engine) -> Vec<ChatMessage> {
    engine
        .store
        .chat_messages()
        .list(REPOSITORY, 12)
        .await
        .unwrap()
}

#[tokio::test]
async fn the_lead_asks_a_question_and_a_trusted_reply_goes_to_the_lead_as_the_next_event() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = "[[prompts]]\ncall = { tool = \"ask\", arguments = { n = 41, text = \"Cents or dollars?\" } }\n\n[[prompts]]\nreply = [\"Seen\"]\n\n[[prompts]]\nreply = [\"Seen\"]\n";
    let engine = connect(&data_dir, &github, script).await;

    dispatch(&github, 41, "Add plan model");

    let results = wait_for(async || {
        let results = mcp_results(&engine, "lead_event").await;
        (!results.is_empty()).then_some(results)
    })
    .await;
    assert_eq!(results[0]["result"], "Asked on #41.");
    assert_eq!(
        github.comments(REPOSITORY, 41),
        [(APP.to_string(), "Cents or dollars?".to_string())]
    );
    assert_eq!(
        github.labels(REPOSITORY, 41),
        ["mobius:working", "mobius:needs-human"]
    );
    let items = inbox::list(&engine).await.unwrap();
    assert_eq!(items.len(), 1, "{items:?}");
    assert_eq!(items[0].kind, InboxKind::Question);
    assert_eq!(
        (
            items[0].repository.as_str(),
            items[0].workstream,
            items[0].issue
        ),
        (REPOSITORY, 12, 41)
    );
    assert_eq!(items[0].text, "Cents or dollars?");
    assert_eq!(items[0].link, "https://github.com/owner/shop/issues/41");
    assert_eq!(items[0].dismissed_at, None);

    github.add_comment(REPOSITORY, 41, "owner", "Cents.");

    let prompts = wait_for(async || {
        let prompts = all_prompts(&engine, "lead_event").await;
        prompts
            .iter()
            .any(|prompt| {
                prompt.ends_with(" comment on #41 \"Add plan model\" by @owner:\n\n> Cents.")
            })
            .then_some(prompts)
    })
    .await;
    assert_eq!(github.labels(REPOSITORY, 41), ["mobius:working"]);
    assert!(prompts.len() >= 2, "{prompts:?}");
    assert_eq!(inbox::list(&engine).await.unwrap().len(), 1);
}

#[tokio::test]
async fn ask_on_an_issue_with_no_live_task_in_the_workstream_changes_nothing() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = "[[prompts]]\ncall = { tool = \"ask\", arguments = { n = 41, text = \"Cents or dollars?\" } }\n";
    let engine = connect(&data_dir, &github, script).await;
    github.add_issue(REPOSITORY, 41, "Add plan model");
    github.add_sub_issue(REPOSITORY, 12, 41);

    dispatch(&github, 40, "Plan API");

    let results = wait_for(async || {
        let results = mcp_results(&engine, "lead_event").await;
        (!results.is_empty()).then_some(results)
    })
    .await;
    assert_eq!(
        results[0]["error"],
        "#41 has no live task in this Workstream."
    );
    assert!(github.comments(REPOSITORY, 41).is_empty());
    assert!(github.labels(REPOSITORY, 41).is_empty());
    assert!(inbox::list(&engine).await.unwrap().is_empty());
}

#[tokio::test]
async fn tell_owner_adds_a_chat_message_and_an_inbox_item() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = "[[prompts]]\ncall = { tool = \"tell_owner\", arguments = { text = \"#41 needs a decision.\" } }\n";
    let engine = connect(&data_dir, &github, script).await;
    let mut feed = activity::feed(&engine, None).await.unwrap();

    dispatch(&github, 41, "Add plan model");

    let messages = wait_for(async || {
        let messages = messages(&engine).await;
        (!messages.is_empty()).then_some(messages)
    })
    .await;
    assert_eq!(messages.len(), 1);
    assert_eq!(messages[0].author, Author::TellOwner);
    assert_eq!(messages[0].text, "#41 needs a decision.");
    assert_eq!(
        engine.store.chat_messages().unread().await.unwrap(),
        [Unread {
            repository: REPOSITORY.to_string(),
            workstream: 12,
            count: 1,
        }]
    );
    let item = wait_for(async || inbox::list(&engine).await.unwrap().pop()).await;
    assert_eq!(item.kind, InboxKind::Lead);
    assert_eq!((item.workstream, item.issue), (12, 12));
    assert_eq!(item.text, "#41 needs a decision.");
    assert_eq!(item.link, "https://github.com/owner/shop/issues/12");
    tokio::time::timeout(Duration::from_secs(5), async {
        while feed.next().await.unwrap() != Live::Inbox(item.clone()) {}
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn dismiss_removes_the_item_from_the_inbox() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = "[[prompts]]\ncall = { tool = \"tell_owner\", arguments = { text = \"#41 needs a decision.\" } }\n";
    let engine = connect(&data_dir, &github, script).await;
    dispatch(&github, 41, "Add plan model");
    let item = wait_for(async || inbox::list(&engine).await.unwrap().pop()).await;
    let mut feed = activity::feed(&engine, None).await.unwrap();

    inbox::dismiss(&engine, item.id).await.unwrap();

    assert!(inbox::list(&engine).await.unwrap().is_empty());
    tokio::time::timeout(Duration::from_secs(5), async {
        loop {
            if let Live::Inbox(dismissed) = feed.next().await.unwrap() {
                assert_eq!(dismissed.id, item.id);
                assert!(dismissed.dismissed_at.is_some());
                break;
            }
        }
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn only_the_event_session_has_tell_owner() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github, "[[prompts]]\nlist_tools = true\n").await;

    dispatch(&github, 41, "Add plan model");

    let reply = wait_for(async || {
        let session = sessions(&engine, "lead_event").await.pop()?;
        transcript(&engine, session.id)
            .await
            .iter()
            .find_map(|row| {
                let json: Value = serde_json::from_str(&row.json).unwrap();
                (json["update"]["sessionUpdate"] == "agent_message_chunk").then(|| {
                    json["update"]["content"]["text"]
                        .as_str()
                        .unwrap()
                        .to_string()
                })
            })
    })
    .await;
    let tools: Vec<Value> = serde_json::from_str(&reply).unwrap();
    let names: Vec<&str> = tools
        .iter()
        .map(|tool| tool["name"].as_str().unwrap())
        .collect();
    assert_eq!(
        names,
        [
            "list_tasks",
            "read_issue",
            "start_implementer",
            "ask",
            "decline",
            "create_issue",
            "mark_ready",
            "comment_pull_request",
            "tell_owner"
        ]
    );
}

#[tokio::test]
async fn the_chat_session_gets_the_new_tell_owner_messages_before_the_next_owner_message() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let script = "[[prompts]]\nhang = true\n\n[[prompts]]\nreply = [\"Seen\"]\n";
    let engine = connect(&data_dir, &github, script).await;
    dispatch(&github, 41, "Add plan model");
    wait_for(async || (all_prompts(&engine, "lead_event").await.len() == 1).then_some(())).await;
    let event_url = fs::read_to_string(data_dir.path().join("harnesses/mcp_url")).unwrap();
    chat::send(&engine, REPOSITORY, 12, "Plan it")
        .await
        .unwrap();
    wait_for(async || (all_prompts(&engine, "lead_chat").await.len() == 1).then_some(())).await;
    let client = ().serve(StreamableHttpClientTransport::from_uri(event_url)).await.unwrap();
    let result = client
        .call_tool(
            CallToolRequestParams::new("tell_owner").with_arguments(
                json!({ "text": "#41 needs a decision." })
                    .as_object()
                    .unwrap()
                    .clone(),
            ),
        )
        .await
        .unwrap();
    assert_ne!(result.is_error, Some(true));
    client.cancel().await.unwrap();

    chat::send(&engine, REPOSITORY, 12, "Go on").await.unwrap();
    chat::stop(&engine, REPOSITORY, 12).unwrap();

    let prompts = wait_for(async || {
        let prompts = all_prompts(&engine, "lead_chat").await;
        (prompts.len() >= 2).then_some(prompts)
    })
    .await;
    assert!(
        !prompts[0].contains("#41 needs a decision."),
        "{}",
        prompts[0]
    );
    assert!(
        prompts[1].starts_with("# Event session messages\n\ntell_owner ("),
        "{}",
        prompts[1]
    );
    assert!(
        prompts[1].ends_with("):\n#41 needs a decision.\n\n# Owner message\n\nGo on"),
        "{}",
        prompts[1]
    );
}
