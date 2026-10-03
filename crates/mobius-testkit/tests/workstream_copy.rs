use mobius_engine::{Engine, github};
use mobius_store::CopiedWorkstream;
use mobius_testkit::fake_github::FakeGitHub;
use mobius_testkit::{start, wait_for};
use tempfile::TempDir;

const REPOSITORY: &str = "owner/shop";

async fn connect(data_dir: &TempDir, github: &FakeGitHub) -> Engine {
    github.add_manifest_code("manifest-code");
    github.add_repository(REPOSITORY);
    let engine = start(data_dir.path(), "correct horse", &github.url).await;
    github::convert_manifest(&engine, "manifest-code")
        .await
        .unwrap();
    engine
}

// (number, title, body, autopilot) of each copied Workstream.
async fn workstreams(engine: &Engine) -> Vec<(i64, String, String, bool)> {
    wait_for(async || {
        let rows: Vec<(i64, String, String, bool)> = sqlx::query_as(
            "SELECT number, title, body, autopilot FROM copied_workstreams
             WHERE repository = ? ORDER BY number",
        )
        .bind(REPOSITORY)
        .fetch_all(&engine.store.pool)
        .await
        .unwrap();
        (!rows.is_empty()).then_some(rows)
    })
    .await
}

// (workstream, number, parent, state, author) of each copied issue in the walk order.
async fn issues(engine: &Engine) -> Vec<(i64, i64, i64, String, String)> {
    sqlx::query_as(
        "SELECT workstream, number, parent, state, author FROM copied_issues
         WHERE repository = ? ORDER BY workstream, position",
    )
    .bind(REPOSITORY)
    .fetch_all(&engine.store.pool)
    .await
    .unwrap()
}

async fn labels(engine: &Engine, number: i64) -> Vec<String> {
    sqlx::query_scalar(
        "SELECT l.name FROM copied_issue_labels l
         JOIN copied_issues i USING (repository, workstream, position)
         WHERE i.repository = ? AND i.number = ? ORDER BY l.name",
    )
    .bind(REPOSITORY)
    .bind(number)
    .fetch_all(&engine.store.pool)
    .await
    .unwrap()
}

// (issue, blocker, workstream of the blocker, title of that Workstream)
async fn blockers(engine: &Engine) -> Vec<(i64, i64, Option<i64>, Option<String>)> {
    sqlx::query_as(
        "SELECT i.number, b.number, b.blocker_workstream, b.blocker_workstream_title
         FROM copied_blockers b
         JOIN copied_issues i USING (repository, workstream, position)
         WHERE b.repository = ? ORDER BY i.number, b.number",
    )
    .bind(REPOSITORY)
    .fetch_all(&engine.store.pool)
    .await
    .unwrap()
}

#[tokio::test]
async fn the_full_sync_copies_the_open_workstreams() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    github.set_body(REPOSITORY, 12, "Brief of the plans");
    github.add_issue(REPOSITORY, 13, "Fix the footer");
    github.add_issue(REPOSITORY, 14, "Price rounding");
    github.add_label(REPOSITORY, 14, "mobius:workstream", "owner");
    github.add_issue(REPOSITORY, 15, "Old Workstream");
    github.add_label(REPOSITORY, 15, "mobius:workstream", "owner");
    github.close_issue(REPOSITORY, 15);

    let engine = connect(&data_dir, &github).await;

    assert_eq!(
        workstreams(&engine).await,
        [
            (
                12,
                "Integrate loyalty plans".to_string(),
                "Brief of the plans".to_string(),
                false
            ),
            (14, "Price rounding".to_string(), String::new(), false),
        ]
    );
}

#[tokio::test]
async fn the_full_sync_copies_autopilot_only_from_a_trusted_user() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    for (number, actor) in [(12, "owner"), (13, "mallory")] {
        github.add_issue(REPOSITORY, number, "Workstream");
        github.add_label(REPOSITORY, number, "mobius:workstream", "owner");
        github.add_label(REPOSITORY, number, "mobius:autopilot", actor);
    }
    github.add_issue(REPOSITORY, 14, "Workstream");
    github.add_label(REPOSITORY, 14, "mobius:workstream", "owner");

    let engine = connect(&data_dir, &github).await;

    let autopilot: Vec<(i64, bool)> = workstreams(&engine)
        .await
        .into_iter()
        .map(|(number, _, _, autopilot)| (number, autopilot))
        .collect();
    assert_eq!(autopilot, [(12, true), (13, false), (14, false)]);
}

#[tokio::test]
async fn the_full_sync_copies_the_nested_sub_issues_with_state_labels_and_author() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    github.add_issue(REPOSITORY, 41, "Add plan model");
    github.add_label(REPOSITORY, 41, "mobius:working", "owner");
    github.add_label(REPOSITORY, 41, "bug", "owner");
    github.add_sub_issue(REPOSITORY, 12, 41);
    github.add_issue(REPOSITORY, 50, "Store the price in cents");
    github.add_sub_issue(REPOSITORY, 41, 50);
    github.add_issue(REPOSITORY, 42, "Let customers change plans");
    github.close_issue(REPOSITORY, 42);
    github.add_sub_issue(REPOSITORY, 12, 42);
    github.add_issue(REPOSITORY, 43, "Mine the servers");
    github.set_author(REPOSITORY, 43, "mallory");
    github.add_sub_issue(REPOSITORY, 12, 43);
    github.add_issue(REPOSITORY, 51, "Task below the untrusted issue");
    github.add_sub_issue(REPOSITORY, 43, 51);

    let engine = connect(&data_dir, &github).await;

    workstreams(&engine).await;
    let open = || "open".to_string();
    let owner = || "owner".to_string();
    assert_eq!(
        issues(&engine).await,
        [
            (12, 41, 12, open(), owner()),
            (12, 50, 41, open(), owner()),
            (12, 42, 12, "closed".to_string(), owner()),
            (12, 43, 12, open(), "mallory".to_string()),
            (12, 51, 43, open(), owner()),
        ]
    );
    assert_eq!(labels(&engine, 41).await, ["bug", "mobius:working"]);
    assert_eq!(labels(&engine, 50).await, Vec::<String>::new());
}

#[tokio::test]
async fn the_full_sync_copies_the_open_blockers_with_the_workstream_of_each_blocker() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    github.add_issue(REPOSITORY, 13, "Billing");
    github.add_label(REPOSITORY, 13, "mobius:workstream", "owner");
    github.add_issue(REPOSITORY, 41, "Add plan model");
    github.add_sub_issue(REPOSITORY, 12, 41);
    github.add_issue(REPOSITORY, 42, "Let customers change plans");
    github.add_sub_issue(REPOSITORY, 12, 42);
    github.add_issue(REPOSITORY, 43, "A closed blocker");
    github.close_issue(REPOSITORY, 43);
    github.add_sub_issue(REPOSITORY, 12, 43);
    github.add_issue(REPOSITORY, 60, "Invoice model");
    github.add_sub_issue(REPOSITORY, 13, 60);
    github.add_issue(REPOSITORY, 61, "An issue of no Workstream");
    github.add_issue("other/repo", 70, "A blocker in another repository");
    github.add_blocker(REPOSITORY, 41, 42);
    github.add_blocker(REPOSITORY, 41, 60);
    github.add_blocker(REPOSITORY, 41, 61);
    github.add_blocker(REPOSITORY, 41, 43);
    github.add_blocker(REPOSITORY, 42, 60);

    let engine = connect(&data_dir, &github).await;

    workstreams(&engine).await;
    let billing = || Some("Billing".to_string());
    assert_eq!(
        blockers(&engine).await,
        [
            (
                41,
                42,
                Some(12),
                Some("Integrate loyalty plans".to_string())
            ),
            (41, 60, Some(13), billing()),
            (41, 61, None, None),
            (42, 60, Some(13), billing()),
        ]
    );
}

#[tokio::test]
async fn the_full_sync_copies_a_sub_issue_of_another_repository_as_a_leaf() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    github.add_issue("other/repo", 70, "A blocker in another repository");
    github.add_issue("other/repo", 77, "A task in another repository");
    github.add_blocker("other/repo", 77, 70);
    github.add_issue("other/repo", 78, "A child in another repository");
    github.add_foreign_sub_issue(REPOSITORY, 12, "other/repo", 77);
    github.add_foreign_sub_issue("other/repo", 77, "other/repo", 78);
    // The different issue 77 of this repository has a child and a blocker.
    github.add_issue(REPOSITORY, 77, "A different issue");
    github.add_issue(REPOSITORY, 90, "Child of the different issue");
    github.add_sub_issue(REPOSITORY, 77, 90);
    github.add_blocker(REPOSITORY, 77, 90);

    let engine = connect(&data_dir, &github).await;

    workstreams(&engine).await;
    assert_eq!(
        issues(&engine).await,
        [(12, 77, 12, "open".to_string(), "owner".to_string())]
    );
    let repository_url: String = sqlx::query_scalar(
        "SELECT repository_url FROM copied_issues WHERE repository = ? AND number = 77",
    )
    .bind(REPOSITORY)
    .fetch_one(&engine.store.pool)
    .await
    .unwrap();
    assert!(
        repository_url.ends_with("/repos/other/repo"),
        "{repository_url}"
    );
    assert_eq!(blockers(&engine).await, []);
}

#[tokio::test]
async fn the_full_sync_forgets_the_rows_of_a_repository_that_is_not_polled() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    github.add_manifest_code("manifest-code");
    github.add_repository(REPOSITORY);
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    let engine = start(data_dir.path(), "correct horse", &github.url).await;
    engine
        .store
        .workstream_copy()
        .replace(
            "owner/gone",
            &[CopiedWorkstream {
                number: 5,
                title: "An old Workstream".to_string(),
                body: String::new(),
                autopilot: false,
                issues: Vec::new(),
            }],
        )
        .await
        .unwrap();

    github::convert_manifest(&engine, "manifest-code")
        .await
        .unwrap();

    workstreams(&engine).await;
    let stale: Vec<i64> =
        sqlx::query_scalar("SELECT number FROM copied_workstreams WHERE repository = 'owner/gone'")
            .fetch_all(&engine.store.pool)
            .await
            .unwrap();
    assert!(stale.is_empty());
}
