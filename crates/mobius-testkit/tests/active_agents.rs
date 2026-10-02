use mobius_domain::ActiveAgents;
use mobius_engine::{Engine, agents, chat, github, workstreams};
use mobius_testkit::fake_github::FakeGitHub;
use mobius_testkit::{install_fake_harness, start_with, wait_for};
use tempfile::TempDir;

const REPOSITORY: &str = "owner/shop";
const FAKE_AGENT: &str = env!("CARGO_BIN_EXE_fake-agent");
const LEAD: &str = r##"
[options]
model = ["sonnet", "opus"]
thought_level = ["low", "high"]
mode = ["default", "bypassPermissions"]

[[prompts]]
when = "Keep me company"
hang = true

[[prompts]]
when = "dispatch of #41"
call = { tool = "start_implementer", arguments = { n = 41, instructions = "Store plans in cents." } }

[[prompts]]
when = "comment on #41"
call = { tool = "start_researcher", arguments = { question = "Where is the price?" } }
"##;
const HANGING: &str = r#"
[options]
model = ["swe-1.5", "gemini-3-pro"]
thought_level = ["high"]
mode = ["default", "yolo"]

[[prompts]]
hang = true
"#;

async fn connect(data_dir: &TempDir, github: &FakeGitHub) -> Engine {
    github.add_manifest_code("manifest-code");
    github.add_repository(REPOSITORY);
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    github.set_body(REPOSITORY, 12, "Ship loyalty plans to all shops.");
    github.add_issue(REPOSITORY, 41, "Add plan model");
    github.add_sub_issue(REPOSITORY, 12, 41);
    install_fake_harness(data_dir.path(), FAKE_AGENT, "claude-agent-acp", LEAD);
    install_fake_harness(data_dir.path(), FAKE_AGENT, "devin", HANGING);
    install_fake_harness(data_dir.path(), FAKE_AGENT, "agy_acp_server", HANGING);
    let engine = start_with(
        data_dir.path(),
        "correct horse",
        &github.url,
        "max_agents = 5",
        |config| {
            config.roles.implementer.max = 3;
            config.roles.researcher.max = 1;
            config.roles.judge.max = 2;
        },
    )
    .await;
    github::convert_manifest(&engine, "manifest-code")
        .await
        .unwrap();
    wait_for(async || (!workstreams::list(&engine).await.unwrap().is_empty()).then_some(())).await;
    engine
}

async fn overview(engine: &Engine) -> ActiveAgents {
    agents::groups(engine).await.unwrap()
}

#[tokio::test]
async fn the_page_counts_the_open_sessions_of_each_role_against_its_limit() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github).await;

    // The dispatch starts an Implementer for #41. The comment needs the task, so it comes after the start.
    github.add_label(REPOSITORY, 41, "mobius:ready", "owner");
    wait_for(async || (overview(&engine).await.groups[2].count == 1).then_some(())).await;

    // The comment starts a Researcher, and the message keeps a Lead chat open.
    github.add_comment(REPOSITORY, 41, "owner", "Where is the price?");
    chat::send(&engine, "owner", REPOSITORY, 12, "Keep me company")
        .await
        .unwrap();

    // The Lead event sessions end, so only the Lead chat holds a lead slot.
    let overview = wait_for(async || {
        let overview = overview(&engine).await;
        let rows: Vec<usize> = overview
            .groups
            .iter()
            .map(|group| group.agents.len())
            .collect();
        (overview.count == 2 && rows == [1, 0, 1, 1, 0, 0]).then_some(overview)
    })
    .await;

    assert_eq!(overview.max, 5);
    let groups: Vec<(&str, u32, u32)> = overview
        .groups
        .iter()
        .map(|group| (group.name.as_str(), group.count, group.max))
        .collect();
    assert_eq!(
        groups,
        [
            ("Lead", 1, 8),
            ("Triager", 0, 2),
            ("Implementer", 1, 3),
            ("Researcher", 1, 1),
            ("Reviewer", 0, 2),
            ("Judge", 0, 2),
        ]
    );

    // The Lead chat shows its Workstream.
    let lead = &overview.groups[0].agents[0];
    assert_eq!(
        (lead.role.as_str(), lead.title.as_str()),
        ("Lead", "chat session")
    );
    assert_eq!(lead.session.organization, "owner");
    assert_eq!(lead.session.repository, REPOSITORY);
    assert_eq!(lead.session.workstream, 12);
    assert_eq!(lead.session.issue, None);
    assert_eq!(lead.session.queue_reason, None);

    // The Implementer shows its issue.
    let implementer = &overview.groups[2].agents[0];
    assert_eq!(implementer.session.role, "implementer");
    assert_eq!(implementer.session.organization, "owner");
    assert_eq!(implementer.session.repository, REPOSITORY);
    assert_eq!(implementer.session.issue, Some(41));

    // The Researcher shows its Workstream.
    let researcher = &overview.groups[3].agents[0];
    assert_eq!(researcher.session.role, "researcher");
    assert_eq!(researcher.session.organization, "owner");
    assert_eq!(researcher.session.repository, REPOSITORY);
    assert_eq!(researcher.session.workstream, 12);
    assert_eq!(researcher.session.issue, None);
}
