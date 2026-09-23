//! GitHub signal source driven by the `gh` CLI (authenticated via the user's
//! own `gh auth login` — Mobius never handles tokens). Polls issues, PRs and
//! comments updated since a cursor timestamp and maps them to [`Signal`]s.

use chrono::{DateTime, Duration, Utc};
use mobius_core::{
    DomainEvent, RepoProvider, Repository, RepositoryId, Signal, SignalId, SignalKind, SourceKind,
    Store,
};
use mobius_ingest::{IngestError, PollFuture, SignalSource, SourceCursor, ingest_signals};
use serde::Deserialize;
use std::collections::HashMap;
use std::future::Future;
use std::pin::Pin;
use std::sync::Arc;

/// Executes `gh` and returns stdout. Boxed future keeps the trait
/// dyn-compatible so tests can inject a fake.
pub trait GhRunner: Send + Sync {
    fn run<'a>(
        &'a self,
        args: &'a [&'a str],
    ) -> Pin<Box<dyn Future<Output = Result<String, IngestError>> + Send + 'a>>;
}

/// Runs the real `gh` binary (path configurable via `gh_binary` config).
pub struct CliGhRunner {
    pub binary: String,
}

impl CliGhRunner {
    pub fn new(binary: impl Into<String>) -> Self {
        Self {
            binary: binary.into(),
        }
    }
}

impl Default for CliGhRunner {
    fn default() -> Self {
        Self::new("gh")
    }
}

impl GhRunner for CliGhRunner {
    fn run<'a>(
        &'a self,
        args: &'a [&'a str],
    ) -> Pin<Box<dyn Future<Output = Result<String, IngestError>> + Send + 'a>> {
        Box::pin(async move {
            let output = tokio::process::Command::new(&self.binary)
                .args(args)
                .output()
                .await
                .map_err(|e| IngestError::Source(format!("spawn {}: {e}", self.binary)))?;
            if !output.status.success() {
                return Err(IngestError::Source(format!(
                    "{} {} failed: {}",
                    self.binary,
                    args.join(" "),
                    String::from_utf8_lossy(&output.stderr)
                )));
            }
            String::from_utf8(output.stdout).map_err(|e| IngestError::Source(format!("utf8: {e}")))
        })
    }
}

/// `gh --version` probe; the source is skipped when this fails.
pub async fn gh_available(binary: &str) -> bool {
    tokio::process::Command::new(binary)
        .arg("--version")
        .output()
        .await
        .map(|o| o.status.success())
        .unwrap_or(false)
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct GhIssue {
    pub number: u64,
    pub title: String,
    #[serde(default)]
    pub body: String,
    #[serde(default)]
    pub labels: Vec<GhLabel>,
    pub updated_at: DateTime<Utc>,
    pub created_at: DateTime<Utc>,
    #[serde(default)]
    pub url: String,
}

#[derive(Debug, Deserialize)]
pub struct GhLabel {
    pub name: String,
}

#[derive(Debug, Deserialize)]
pub struct GhAuthor {
    #[serde(default)]
    pub login: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct GhComment {
    pub id: String,
    #[serde(default)]
    pub body: String,
    pub created_at: DateTime<Utc>,
    #[serde(default)]
    pub author: Option<GhAuthor>,
}

#[derive(Debug, Deserialize)]
pub struct GhComments {
    #[serde(default)]
    pub comments: Vec<GhComment>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct GhPr {
    pub number: u64,
    pub title: String,
    #[serde(default)]
    pub body: String,
    pub updated_at: DateTime<Utc>,
    pub created_at: DateTime<Utc>,
    #[serde(default)]
    pub review_requests: Vec<GhAuthor>,
    #[serde(default)]
    pub url: String,
}

fn base_signal(repository: &Repository, kind: SignalKind, dedupe_key: String) -> Signal {
    let now = Utc::now();
    Signal {
        id: SignalId::new(),
        source: SourceKind::GitHub,
        kind,
        repository_id: Some(repository.id),
        project_id: None,
        dedupe_key,
        title: String::new(),
        body: String::new(),
        payload: serde_json::Value::Null,
        occurred_at: now,
        ingested_at: now,
    }
}

/// Map `gh issue list` output + per-issue comments into signals.
/// `since` is the previous cursor timestamp.
pub fn map_issues(
    repository: &Repository,
    issues: &[GhIssue],
    comments: &[(u64, Vec<GhComment>)],
    since: DateTime<Utc>,
) -> Vec<Signal> {
    let mut out = Vec::new();
    for issue in issues {
        let key_base = format!(
            "github:{}/{}:issue:{}",
            repository.owner, repository.name, issue.number
        );
        let is_new = issue.created_at > since;
        if is_new {
            let mut s = base_signal(repository, SignalKind::IssueOpened, key_base.clone());
            s.title = format!("Issue #{}: {}", issue.number, issue.title);
            s.body = issue.body.clone();
            s.occurred_at = issue.created_at;
            s.payload = serde_json::json!({ "url": issue.url, "number": issue.number });
            out.push(s);
        } else {
            // Existing issue that was updated: emit IssueLabeled per label
            // (dedupe keys make this fire once per label ever).
            for label in &issue.labels {
                let mut s = base_signal(
                    repository,
                    SignalKind::IssueLabeled,
                    format!("{key_base}:labeled:{}", label.name),
                );
                s.title = format!(
                    "Issue #{} labeled {:?}: {}",
                    issue.number, label.name, issue.title
                );
                s.occurred_at = issue.updated_at;
                s.payload = serde_json::json!({
                    "url": issue.url,
                    "number": issue.number,
                    "label": label.name,
                });
                out.push(s);
            }
        }
        for (number, comments) in comments.iter().filter(|(n, _)| *n == issue.number) {
            for c in comments.iter().filter(|c| c.created_at > since) {
                let mut s = base_signal(
                    repository,
                    SignalKind::IssueCommented,
                    format!("{key_base}:comment:{}", c.id),
                );
                s.title = format!("Comment on issue #{}: {}", number, issue.title);
                s.body = c.body.clone();
                s.occurred_at = c.created_at;
                s.payload = serde_json::json!({
                    "url": issue.url,
                    "issue": number,
                    "comment_id": c.id,
                    "author": c.author.as_ref().map(|a| a.login.clone()),
                });
                out.push(s);
            }
        }
    }
    out
}

pub fn map_prs(repository: &Repository, prs: &[GhPr], since: DateTime<Utc>) -> Vec<Signal> {
    let mut out = Vec::new();
    for pr in prs {
        let key_base = format!(
            "github:{}/{}:pr:{}",
            repository.owner, repository.name, pr.number
        );
        if pr.created_at > since {
            let mut s = base_signal(repository, SignalKind::PullRequestOpened, key_base.clone());
            s.title = format!("PR #{}: {}", pr.number, pr.title);
            s.body = pr.body.clone();
            s.occurred_at = pr.created_at;
            s.payload = serde_json::json!({ "url": pr.url, "number": pr.number });
            out.push(s);
        }
        if pr.updated_at > since {
            for req in &pr.review_requests {
                let mut s = base_signal(
                    repository,
                    SignalKind::PullRequestReviewRequested,
                    format!("{key_base}:review:{}", req.login),
                );
                s.title = format!("Review requested on PR #{}: {}", pr.number, pr.title);
                s.occurred_at = pr.updated_at;
                s.payload = serde_json::json!({
                    "url": pr.url,
                    "number": pr.number,
                    "reviewer": req.login,
                });
                out.push(s);
            }
        }
    }
    out
}

/// Polls one repository through `gh` CLI calls.
pub struct GithubPollingSource {
    runner: Arc<dyn GhRunner>,
    repository: Repository,
    source_name: String,
    /// First poll with no cursor covers this look-back window.
    initial_lookback: Duration,
}

impl GithubPollingSource {
    pub fn new(runner: Arc<dyn GhRunner>, repository: Repository) -> Self {
        let source_name = format!("github:{}/{}", repository.owner, repository.name);
        Self {
            runner,
            repository,
            source_name,
            initial_lookback: Duration::days(7),
        }
    }

    pub fn with_lookback(mut self, d: Duration) -> Self {
        self.initial_lookback = d;
        self
    }
}

fn cursor_since(cursor: &Option<SourceCursor>, lookback: Duration) -> DateTime<Utc> {
    cursor
        .as_ref()
        .and_then(|c| c.0.get("since").and_then(|v| v.as_str()).map(String::from))
        .and_then(|s| DateTime::parse_from_rfc3339(&s).ok())
        .map(|d| d.with_timezone(&Utc))
        .unwrap_or_else(|| Utc::now() - lookback)
}

impl SignalSource for GithubPollingSource {
    fn name(&self) -> &str {
        // Per-repo name so SourceRegistry cursors don't collide.
        &self.source_name
    }

    fn poll(&mut self, cursor: Option<SourceCursor>) -> PollFuture<'_> {
        Box::pin(async move {
            let since = cursor_since(&cursor, self.initial_lookback);
            let repo_slug = format!("{}/{}", self.repository.owner, self.repository.name);
            let since_iso = since.to_rfc3339();
            let search = format!("updated:>={since_iso}");

            let issues_json = self
                .runner
                .run(&[
                    "issue",
                    "list",
                    "-R",
                    &repo_slug,
                    "--state",
                    "all",
                    "--limit",
                    "200",
                    "--json",
                    "number,title,body,labels,updatedAt,createdAt,author,url",
                    "--search",
                    &search,
                ])
                .await?;
            let issues: Vec<GhIssue> = serde_json::from_str(&issues_json)
                .map_err(|e| IngestError::Source(format!("parse issues: {e}")))?;

            let mut comments: Vec<(u64, Vec<GhComment>)> = Vec::new();
            let mut max_seen = since;
            for issue in &issues {
                if issue.updated_at > max_seen {
                    max_seen = issue.updated_at;
                }
                let number = issue.number.to_string();
                let comments_json = self
                    .runner
                    .run(&[
                        "issue", "view", &number, "-R", &repo_slug, "--json", "comments",
                    ])
                    .await?;
                let parsed: GhComments = serde_json::from_str(&comments_json)
                    .map_err(|e| IngestError::Source(format!("parse comments: {e}")))?;
                comments.push((issue.number, parsed.comments));
            }

            let prs_json = self
                .runner
                .run(&[
                    "pr", "list", "-R", &repo_slug, "--state", "all",
                    "--limit", "200",
                    "--json",
                    "number,title,body,labels,updatedAt,createdAt,author,url,reviewRequests,isDraft",
                    "--search", &search,
                ])
                .await?;
            let prs: Vec<GhPr> = serde_json::from_str(&prs_json)
                .map_err(|e| IngestError::Source(format!("parse prs: {e}")))?;
            for pr in &prs {
                if pr.updated_at > max_seen {
                    max_seen = pr.updated_at;
                }
            }

            let mut signals = map_issues(&self.repository, &issues, &comments, since);
            signals.extend(map_prs(&self.repository, &prs, since));

            Ok((
                signals,
                SourceCursor(serde_json::json!({ "since": max_seen.to_rfc3339() })),
            ))
        })
    }
}

/// Manages one [`GithubPollingSource`] per eligible repository, re-reading the
/// repository list from the store on every tick so REST CRUD changes take
/// effect without a restart. Cursors are kept per [`RepositoryId`].
pub struct GithubSources {
    runner: Arc<dyn GhRunner>,
    /// repo id → (source, last cursor)
    sources: HashMap<RepositoryId, (GithubPollingSource, Option<SourceCursor>)>,
}

impl GithubSources {
    pub fn new(runner: Arc<dyn GhRunner>) -> Self {
        Self {
            runner,
            sources: HashMap::new(),
        }
    }

    fn eligible(repo: &Repository) -> bool {
        repo.provider == RepoProvider::GitHub && repo.owner != "local"
    }

    /// One poll cycle: sync the source set with the store, then poll each
    /// source once (dedupe → insert → emit). Returns new signals ingested.
    pub async fn tick<S, F>(&mut self, store: &S, emit: &F) -> Result<usize, IngestError>
    where
        S: Store,
        F: Fn(DomainEvent),
    {
        let repos = store.list_repositories().await?;
        let eligible: std::collections::HashSet<RepositoryId> = repos
            .iter()
            .filter(|r| Self::eligible(r))
            .map(|r| r.id)
            .collect();
        self.sources.retain(|id, _| eligible.contains(id));
        for repo in repos.into_iter().filter(Self::eligible) {
            self.sources
                .entry(repo.id)
                .or_insert_with(|| (GithubPollingSource::new(self.runner.clone(), repo), None));
        }

        let mut count = 0;
        for (id, (source, cursor)) in self.sources.iter_mut() {
            match source.poll(cursor.clone()).await {
                Ok((signals, next)) => {
                    *cursor = Some(next);
                    count += ingest_signals(store, signals, emit).await?;
                }
                Err(e) => {
                    tracing::warn!(repo = %id, error = %e, "github poll failed");
                }
            }
        }
        Ok(count)
    }

    /// Poll forever at `interval`. Designed to be `tokio::spawn`ed.
    pub async fn run<S, F>(mut self, interval: std::time::Duration, store: S, emit: F)
    where
        S: Store,
        F: Fn(DomainEvent),
    {
        loop {
            if let Err(e) = self.tick(&store, &emit).await {
                tracing::warn!(error = %e, "github poll cycle failed");
            }
            tokio::time::sleep(interval).await;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use mobius_core::{OrganizationId, RepoProvider, RepositoryRepo};
    use std::collections::HashMap;
    use std::sync::Mutex;

    struct FakeRunner {
        /// args-joined -> stdout
        responses: Mutex<HashMap<String, String>>,
        calls: Mutex<Vec<String>>,
    }

    impl FakeRunner {
        fn new() -> Self {
            Self {
                responses: Mutex::new(HashMap::new()),
                calls: Mutex::new(Vec::new()),
            }
        }
        fn respond(&self, needle: &str, body: &str) {
            if let Ok(mut m) = self.responses.lock() {
                m.insert(needle.to_string(), body.to_string());
            }
        }
    }

    impl GhRunner for FakeRunner {
        fn run<'a>(
            &'a self,
            args: &'a [&'a str],
        ) -> Pin<Box<dyn Future<Output = Result<String, IngestError>> + Send + 'a>> {
            Box::pin(async move {
                let joined = args.join(" ");
                if let Ok(mut calls) = self.calls.lock() {
                    calls.push(joined.clone());
                }
                if let Ok(map) = self.responses.lock() {
                    // Most specific match wins: `issue view N` before `issue list`.
                    let mut best: Option<&String> = None;
                    for (k, v) in map.iter() {
                        if joined.contains(k.as_str()) && best.is_none_or(|b| k.len() > b.len()) {
                            best = Some(v);
                        }
                    }
                    if let Some(v) = best {
                        return Ok(v.clone());
                    }
                }
                Ok("[]".to_string())
            })
        }
    }

    fn repo() -> Repository {
        Repository {
            id: mobius_core::RepositoryId::new(),
            organization_id: OrganizationId::new(),
            owner: "acme".into(),
            name: "widget".into(),
            provider: RepoProvider::GitHub,
            default_branch: "main".into(),
            local_path: None,
            created_at: Utc::now(),
        }
    }

    #[test]
    fn issue_mapping() {
        let repo = repo();
        let since = "2026-09-20T00:00:00Z".parse::<DateTime<Utc>>().expect("ts");
        let issues = vec![
            GhIssue {
                number: 1,
                title: "new bug".into(),
                body: "b".into(),
                labels: vec![],
                updated_at: "2026-09-21T00:00:00Z".parse().expect("t"),
                created_at: "2026-09-21T00:00:00Z".parse().expect("t"),
                url: "https://x/1".into(),
            },
            GhIssue {
                number: 2,
                title: "old issue".into(),
                body: "b".into(),
                labels: vec![GhLabel { name: "bug".into() }],
                updated_at: "2026-09-21T12:00:00Z".parse().expect("t"),
                created_at: "2026-09-01T00:00:00Z".parse().expect("t"),
                url: "https://x/2".into(),
            },
        ];
        let comments = vec![(
            2u64,
            vec![GhComment {
                id: "IC_1".into(),
                body: "hi".into(),
                created_at: "2026-09-21T13:00:00Z".parse().expect("t"),
                author: None,
            }],
        )];
        let signals = map_issues(&repo, &issues, &comments, since);
        let kinds: Vec<_> = signals.iter().map(|s| &s.kind).collect();
        assert!(kinds.contains(&&SignalKind::IssueOpened));
        assert!(kinds.contains(&&SignalKind::IssueLabeled));
        assert!(kinds.contains(&&SignalKind::IssueCommented));
        let keys: Vec<_> = signals.iter().map(|s| s.dedupe_key.clone()).collect();
        assert!(keys.contains(&"github:acme/widget:issue:1".to_string()));
        assert!(keys.contains(&"github:acme/widget:issue:2:labeled:bug".to_string()));
        assert!(keys.contains(&"github:acme/widget:issue:2:comment:IC_1".to_string()));
    }

    #[test]
    fn pr_mapping() {
        let repo = repo();
        let since = "2026-09-20T00:00:00Z".parse::<DateTime<Utc>>().expect("ts");
        let prs = vec![GhPr {
            number: 7,
            title: "feat".into(),
            body: "b".into(),
            updated_at: "2026-09-21T00:00:00Z".parse().expect("t"),
            created_at: "2026-09-21T00:00:00Z".parse().expect("t"),
            review_requests: vec![GhAuthor {
                login: "alice".into(),
            }],
            url: "u".into(),
        }];
        let signals = map_prs(&repo, &prs, since);
        let keys: Vec<_> = signals.iter().map(|s| s.dedupe_key.clone()).collect();
        assert!(keys.contains(&"github:acme/widget:pr:7".to_string()));
        assert!(keys.contains(&"github:acme/widget:pr:7:review:alice".to_string()));
    }

    #[tokio::test]
    async fn polling_advances_cursor() {
        let runner = Arc::new(FakeRunner::new());
        runner.respond(
            "issue list",
            r#"[{"number":1,"title":"t","body":"b","labels":[],
               "updatedAt":"2026-09-21T10:00:00Z","createdAt":"2026-09-21T10:00:00Z",
               "author":{"login":"x"},"url":"u"}]"#,
        );
        runner.respond("issue view", r#"{"comments":[]}"#);
        runner.respond("pr list", "[]");
        let mut source = GithubPollingSource::new(runner, repo());
        let (signals, cursor) = source.poll(None).await.expect("poll");
        assert_eq!(signals.len(), 1);
        assert_eq!(signals[0].kind, SignalKind::IssueOpened);
        assert_eq!(
            cursor.0["since"].as_str(),
            Some("2026-09-21T10:00:00+00:00")
        );

        // Second poll: gh still lists the issue (updated_at == since), but
        // created_at is not > since, so no new IssueOpened signal is emitted
        // and the cursor doesn't move.
        let (signals, cursor2) = source.poll(Some(cursor.clone())).await.expect("poll2");
        assert_eq!(signals.len(), 0);
        assert_eq!(cursor2.0["since"], cursor.0["since"]);
    }

    /// GithubSources re-reads repositories from the store on every tick: a
    /// repo inserted between two ticks is polled on the second tick.
    #[tokio::test]
    async fn sources_follow_repository_crud() {
        let store = mobius_core::InMemoryStore::new();
        let runner = Arc::new(FakeRunner::new());
        runner.respond("issue list", "[]");
        runner.respond("pr list", "[]");
        let mut sources = GithubSources::new(runner.clone());
        let emit = |_e: DomainEvent| {};

        // Tick 1: no repositories → nothing polled.
        let n = sources.tick(&store, &emit).await.expect("tick1");
        assert_eq!(n, 0);
        assert!(
            runner.calls.lock().expect("calls").is_empty(),
            "no gh calls with no repos"
        );

        // Insert an eligible repo between ticks → polled on tick 2.
        let repo = repo();
        store.insert_repository(&repo).await.expect("insert repo");
        let n = sources.tick(&store, &emit).await.expect("tick2");
        assert_eq!(n, 0);
        let calls = runner.calls.lock().expect("calls").clone();
        assert!(
            calls.iter().any(|c| c.contains("issue list")),
            "repo polled on second tick: {calls:?}"
        );

        // Delete it → no further calls beyond the ones already made.
        store.delete_repository(repo.id).await.expect("delete repo");
        let before = calls.len();
        let n = sources.tick(&store, &emit).await.expect("tick3");
        assert_eq!(n, 0);
        assert_eq!(runner.calls.lock().expect("calls").len(), before);
    }
}
