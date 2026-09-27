use std::time::Duration;

use mobius_domain::{FeedRow, Workstream};
use mobius_engine::activity::{self, Feed};
use mobius_engine::{Engine, github, workstreams};
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

async fn next_row(feed: &mut Feed) -> FeedRow {
    tokio::time::timeout(Duration::from_secs(5), feed.next())
        .await
        .unwrap()
        .unwrap()
}

#[tokio::test]
async fn a_workstream_label_shows_the_issue_in_the_workstream_list() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github).await;
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_issue(REPOSITORY, 13, "Fix the footer");

    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");

    let list = wait_for(async || {
        let list = workstreams::list(&engine).await.unwrap();
        (!list.is_empty()).then_some(list)
    })
    .await;
    assert_eq!(
        list,
        [Workstream {
            repository: REPOSITORY.to_string(),
            number: 12,
            title: "Integrate loyalty plans".to_string(),
        }]
    );
}

#[tokio::test]
async fn a_new_workstream_adds_one_feed_row_that_goes_live() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github).await;
    let mut feed = activity::feed(&engine, None).await.unwrap();
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_issue(REPOSITORY, 13, "Price rounding");

    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    let first = next_row(&mut feed).await;
    github.add_label(REPOSITORY, 13, "mobius:workstream", "Owner");
    let second = next_row(&mut feed).await;

    assert_eq!(first.repository, REPOSITORY);
    assert_eq!((first.workstream, first.issue), (12, 12));
    assert_eq!(first.actor, "owner");
    assert_eq!(first.text, "New Workstream \"Integrate loyalty plans\"");
    assert_eq!(first.link, "https://github.com/owner/shop/issues/12");
    assert_eq!(second.issue, 13);
    assert_eq!(
        engine.store.events().latest(100).await.unwrap(),
        [first, second]
    );
}

#[tokio::test]
async fn a_workstream_label_from_an_untrusted_author_adds_no_feed_row() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github).await;
    let mut feed = activity::feed(&engine, None).await.unwrap();
    github.add_issue(REPOSITORY, 12, "Mine the servers");
    github.add_issue(REPOSITORY, 13, "Integrate loyalty plans");

    github.add_label(REPOSITORY, 12, "mobius:workstream", "mallory");
    github.add_label(REPOSITORY, 13, "mobius:workstream", "owner");

    assert_eq!(next_row(&mut feed).await.issue, 13);
}

#[tokio::test]
async fn a_feed_after_a_row_gives_the_rows_that_follow_it() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    let engine = connect(&data_dir, &github).await;
    let mut feed = activity::feed(&engine, None).await.unwrap();
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_issue(REPOSITORY, 13, "Price rounding");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");
    let first = next_row(&mut feed).await;
    github.add_label(REPOSITORY, 13, "mobius:workstream", "owner");
    let second = next_row(&mut feed).await;

    let mut resumed = activity::feed(&engine, Some(first.id)).await.unwrap();

    assert_eq!(next_row(&mut resumed).await, second);
}

#[tokio::test]
async fn a_workstream_label_from_before_the_first_poll_adds_a_feed_row() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    github.add_label(REPOSITORY, 12, "mobius:workstream", "owner");

    let engine = connect(&data_dir, &github).await;
    let mut feed = activity::feed(&engine, None).await.unwrap();

    assert_eq!(next_row(&mut feed).await.issue, 12);
}

#[tokio::test]
async fn an_unchanged_repository_gets_not_modified_answers() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    github.add_issue(REPOSITORY, 12, "Integrate loyalty plans");
    let _engine = connect(&data_dir, &github).await;

    wait_for(async || (github.not_modified_count() > 0).then_some(())).await;
}
