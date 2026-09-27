use std::collections::{BTreeMap, HashMap, HashSet};
use std::fs;
use std::hash::{DefaultHasher, Hash, Hasher};
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use axum::extract::{Path, Query, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{IntoResponse, Response};
use axum::routing::{delete, get, patch, post};
use axum::{Json, Router};
use serde::Deserialize;
use serde_json::{Value, json};
use tempfile::TempDir;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;
use tokio::net::TcpListener;

use crate::git;

pub const APP_ID: i64 = 7;
pub const APP_SLUG: &str = "mobius-test";
pub const APP_PRIVATE_KEY: &str = include_str!("app_private_key.pem");
pub const APP_CLIENT_ID: &str = "Iv23test";
pub const APP_CLIENT_SECRET: &str = "client-secret";
pub const BOT_USER_ID: i64 = 41898282;
pub const INSTALLATION_TOKEN: &str = "ghs_installation";

#[derive(Clone, Debug, PartialEq)]
pub struct PullRequest {
    pub number: i64,
    pub title: String,
    pub body: String,
    pub head: String,
    pub base: String,
    pub draft: bool,
}

#[derive(Clone, Debug, PartialEq)]
pub struct CheckRun {
    pub name: String,
    pub head_sha: String,
    pub status: String,
    pub conclusion: Option<String>,
    pub output: Option<CheckRunOutput>,
}

#[derive(Clone, Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct CheckRunOutput {
    pub title: String,
    pub summary: String,
}

#[derive(Clone, Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct SubmittedReview {
    pub commit_id: String,
    pub body: String,
    pub event: String,
    pub comments: Vec<InlineComment>,
}

#[derive(Clone, Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct InlineComment {
    pub path: String,
    pub line: i64,
    pub body: String,
}

#[derive(Default)]
struct Records {
    account_types: HashMap<String, &'static str>,
    manifest_codes: HashSet<String>,
    user_codes: HashMap<String, String>,
    user_tokens: HashMap<String, String>,
    refresh_tokens: HashMap<String, String>,
    tokens_given: u32,
    repositories: Vec<String>,
    remotes: PathBuf,
    issues: BTreeMap<(String, i64), Issue>,
    pull_requests: Vec<(String, PullRequest)>,
    // The id of a check run is its index plus 1.
    check_runs: Vec<(String, CheckRun)>,
    submitted_reviews: Vec<(String, i64, SubmittedReview)>,
    last_review_comment_id: i64,
    clock: i64,
    not_modified: u32,
}

struct Issue {
    title: String,
    body: String,
    author: String,
    pull_request: bool,
    state: &'static str,
    sub_issues: Vec<i64>,
    blocked_by: Vec<i64>,
    labels: Vec<String>,
    updated_at: i64,
    events: Vec<Value>,
    comments: Vec<Value>,
    reviews: Vec<Value>,
    review_comments: Vec<Value>,
}

impl Records {
    // Each write is one second after the last, so `since` compares exactly.
    fn tick(&mut self) -> i64 {
        self.clock += 1;
        self.clock
    }

    fn comment(
        &mut self,
        repository: &str,
        number: i64,
        author: &str,
        body: &str,
        app: Option<&str>,
    ) -> Value {
        let now = self.tick();
        let comment = json!({
            "user": { "login": author },
            "body": body,
            "created_at": timestamp(now),
            "performed_via_github_app": app.map(|slug| json!({ "slug": slug }))
        });
        let issue = self
            .issues
            .get_mut(&(repository.to_string(), number))
            .unwrap();
        issue.comments.push(comment.clone());
        issue.updated_at = now;
        comment
    }

    // Gives the id of the new review comment.
    fn review_comment(
        &mut self,
        repository: &str,
        number: i64,
        in_reply_to: Option<i64>,
        author: &str,
        comment: &InlineComment,
    ) -> i64 {
        let now = self.tick();
        self.last_review_comment_id += 1;
        let id = self.last_review_comment_id;
        self.issues
            .get_mut(&(repository.to_string(), number))
            .unwrap()
            .review_comments
            .push(json!({
                "id": id,
                "user": { "login": author },
                "body": comment.body,
                "path": comment.path,
                "line": comment.line,
                "in_reply_to_id": in_reply_to,
                "created_at": timestamp(now)
            }));
        id
    }

    fn label(&mut self, repository: &str, number: i64, label: &str, actor: &str) {
        let now = self.tick();
        let issue = self
            .issues
            .get_mut(&(repository.to_string(), number))
            .unwrap();
        if !issue.labels.iter().any(|name| name == label) {
            issue.labels.push(label.to_string());
        }
        issue.updated_at = now;
        issue.events.push(json!({
            "event": "labeled",
            "actor": { "login": actor },
            "label": { "name": label },
            "created_at": timestamp(now)
        }));
    }

    fn issue_json(&self, repository: &str, number: i64) -> Value {
        let issue = &self.issues[&(repository.to_string(), number)];
        let open_blockers = issue
            .blocked_by
            .iter()
            .filter(|blocker| self.issues[&(repository.to_string(), **blocker)].state == "open")
            .count();
        let mut json = json!({
            "number": number,
            "title": issue.title,
            "body": issue.body,
            "user": { "login": issue.author },
            "html_url": format!("https://github.com/{repository}/issues/{number}"),
            "state": issue.state,
            "updated_at": timestamp(issue.updated_at),
            "labels": issue.labels.iter().map(|name| json!({ "name": name })).collect::<Vec<_>>(),
            "issue_dependencies_summary": {
                "blocked_by": open_blockers,
                "total_blocked_by": issue.blocked_by.len()
            }
        });
        if issue.pull_request {
            json["pull_request"] = json!({
                "url": format!("https://api.github.com/repos/{repository}/pulls/{number}")
            });
        }
        json
    }
}

fn app_login() -> String {
    format!("{APP_SLUG}[bot]")
}

fn timestamp(seconds: i64) -> String {
    (OffsetDateTime::UNIX_EPOCH + time::Duration::seconds(seconds))
        .format(&Rfc3339)
        .unwrap()
}

type Shared = Arc<Mutex<Records>>;

pub struct FakeGitHub {
    pub url: String,
    state: Shared,
    remotes: TempDir,
}

impl FakeGitHub {
    pub async fn start() -> FakeGitHub {
        let remotes = TempDir::new().unwrap();
        let state = Shared::new(Mutex::new(Records {
            clock: OffsetDateTime::now_utc().unix_timestamp(),
            remotes: remotes.path().to_path_buf(),
            ..Records::default()
        }));
        let router = Router::new()
            .route("/users/{name}", get(account))
            .route("/app-manifests/{code}/conversions", post(convert_manifest))
            .route("/login/oauth/access_token", post(exchange_code))
            .route("/user", get(user))
            .route("/app/installations", get(installations))
            .route(
                "/app/installations/{id}/access_tokens",
                post(installation_token),
            )
            .route("/installation/repositories", get(installation_repositories))
            .route("/repos/{owner}/{repo}/issues", get(issues))
            .route("/repos/{owner}/{repo}/issues/{number}", get(issue))
            .route(
                "/repos/{owner}/{repo}/issues/{number}/sub_issues",
                get(sub_issues),
            )
            .route("/repos/{owner}/{repo}/issues/{number}/parent", get(parent))
            .route(
                "/repos/{owner}/{repo}/issues/{number}/events",
                get(issue_events),
            )
            .route(
                "/repos/{owner}/{repo}/issues/{number}/comments",
                get(issue_comments).post(add_issue_comment),
            )
            .route(
                "/repos/{owner}/{repo}/issues/{number}/labels",
                post(add_labels),
            )
            .route(
                "/repos/{owner}/{repo}/issues/{number}/labels/{name}",
                delete(remove_label),
            )
            .route("/repos/{owner}/{repo}/pulls", post(create_pull_request))
            .route("/repos/{owner}/{repo}/check-runs", post(create_check_run))
            .route(
                "/repos/{owner}/{repo}/check-runs/{id}",
                patch(update_check_run),
            )
            .route(
                "/repos/{owner}/{repo}/pulls/{number}/reviews",
                get(reviews).post(submit_review),
            )
            .route("/graphql", post(graphql))
            .route(
                "/repos/{owner}/{repo}/pulls/{number}/comments",
                get(review_comments),
            )
            .with_state(state.clone());
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!("http://{}", listener.local_addr().unwrap());
        tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        FakeGitHub {
            url,
            state,
            remotes,
        }
    }

    pub fn add_account(&self, login: &str, account_type: &'static str) {
        self.state
            .lock()
            .unwrap()
            .account_types
            .insert(login.to_string(), account_type);
    }

    pub fn add_manifest_code(&self, code: &str) {
        self.state
            .lock()
            .unwrap()
            .manifest_codes
            .insert(code.to_string());
    }

    pub fn add_user_code(&self, code: &str, login: &str) {
        self.state
            .lock()
            .unwrap()
            .user_codes
            .insert(code.to_string(), login.to_string());
    }

    // The repository gets a bare git repository with one commit on `main` as its `clone_url`.
    pub fn add_repository(&self, full_name: &str) {
        let remote = self.remote(full_name);
        std::fs::create_dir_all(&remote).unwrap();
        git(&remote, &["init", "--bare", "--initial-branch=main"]);
        let work = TempDir::new().unwrap();
        git(work.path(), &["init", "--initial-branch=main"]);
        git(
            work.path(),
            &["commit", "--allow-empty", "-m", "Initial commit"],
        );
        git(
            work.path(),
            &["push", remote.to_str().unwrap(), "main:refs/heads/main"],
        );
        self.state
            .lock()
            .unwrap()
            .repositories
            .push(full_name.to_string());
    }

    // The author is `owner`.
    pub fn add_issue(&self, repository: &str, number: i64, title: &str) {
        self.insert_issue(repository, number, title, false);
    }

    pub fn add_pull_request(&self, repository: &str, number: i64, title: &str) {
        self.insert_issue(repository, number, title, true);
    }

    fn insert_issue(&self, repository: &str, number: i64, title: &str, pull_request: bool) {
        let mut records = self.state.lock().unwrap();
        let updated_at = records.tick();
        records.issues.insert(
            (repository.to_string(), number),
            Issue {
                title: title.to_string(),
                body: String::new(),
                author: "owner".to_string(),
                pull_request,
                state: "open",
                sub_issues: Vec::new(),
                blocked_by: Vec::new(),
                labels: Vec::new(),
                updated_at,
                events: Vec::new(),
                comments: Vec::new(),
                reviews: Vec::new(),
                review_comments: Vec::new(),
            },
        );
    }

    pub fn remote(&self, full_name: &str) -> PathBuf {
        self.remotes.path().join(format!("{full_name}.git"))
    }

    // Pushes one new commit on top of `main` to `branch`, as a human push.
    pub fn push_commit(&self, full_name: &str, branch: &str, message: &str) {
        let remote = self.remote(full_name);
        let work = TempDir::new().unwrap();
        git(
            work.path(),
            &["clone", "--branch=main", remote.to_str().unwrap(), "."],
        );
        git(work.path(), &["commit", "--allow-empty", "-m", message]);
        git(
            work.path(),
            &["push", "origin", &format!("HEAD:refs/heads/{branch}")],
        );
    }

    // Commits `script` as an executable `.mobius/check` on `main`.
    pub fn set_check(&self, full_name: &str, script: &str) {
        let remote = self.remote(full_name);
        let work = TempDir::new().unwrap();
        git(
            work.path(),
            &["clone", "--branch=main", remote.to_str().unwrap(), "."],
        );
        let check = work.path().join(".mobius/check");
        fs::create_dir_all(check.parent().unwrap()).unwrap();
        fs::write(&check, format!("#!/bin/sh\n{script}\n")).unwrap();
        fs::set_permissions(&check, fs::Permissions::from_mode(0o755)).unwrap();
        git(work.path(), &["add", ".mobius/check"]);
        git(work.path(), &["commit", "-m", "Add the local check"]);
        git(work.path(), &["push", "origin", "HEAD:refs/heads/main"]);
    }

    pub fn pull_requests(&self, full_name: &str) -> Vec<PullRequest> {
        self.state
            .lock()
            .unwrap()
            .pull_requests
            .iter()
            .filter(|(name, _)| name == full_name)
            .map(|(_, pull_request)| pull_request.clone())
            .collect()
    }

    pub fn check_runs(&self, full_name: &str) -> Vec<CheckRun> {
        self.state
            .lock()
            .unwrap()
            .check_runs
            .iter()
            .filter(|(name, _)| name == full_name)
            .map(|(_, check_run)| check_run.clone())
            .collect()
    }

    pub fn submitted_reviews(&self, full_name: &str, number: i64) -> Vec<SubmittedReview> {
        self.state
            .lock()
            .unwrap()
            .submitted_reviews
            .iter()
            .filter(|(name, pull_request, _)| name == full_name && *pull_request == number)
            .map(|(_, _, review)| review.clone())
            .collect()
    }

    pub fn set_author(&self, repository: &str, number: i64, author: &str) {
        self.state
            .lock()
            .unwrap()
            .issues
            .get_mut(&(repository.to_string(), number))
            .unwrap()
            .author = author.to_string();
    }

    pub fn add_comment(&self, repository: &str, number: i64, author: &str, body: &str) {
        self.state
            .lock()
            .unwrap()
            .comment(repository, number, author, body, None);
    }

    // A comment that `author` posts through the Mobius App, as the `gh` of the Lead chat session does.
    pub fn add_app_comment(&self, repository: &str, number: i64, author: &str, body: &str) {
        self.state
            .lock()
            .unwrap()
            .comment(repository, number, author, body, Some(APP_SLUG));
    }

    // Gives the author and the body of each comment.
    pub fn comments(&self, repository: &str, number: i64) -> Vec<(String, String)> {
        self.state.lock().unwrap().issues[&(repository.to_string(), number)]
            .comments
            .iter()
            .map(|comment| {
                (
                    comment["user"]["login"].as_str().unwrap().to_string(),
                    comment["body"].as_str().unwrap().to_string(),
                )
            })
            .collect()
    }

    pub fn labels(&self, repository: &str, number: i64) -> Vec<String> {
        self.state.lock().unwrap().issues[&(repository.to_string(), number)]
            .labels
            .clone()
    }

    pub fn add_review(&self, repository: &str, number: i64, author: &str, state: &str, body: &str) {
        let mut records = self.state.lock().unwrap();
        let now = records.tick();
        records
            .issues
            .get_mut(&(repository.to_string(), number))
            .unwrap()
            .reviews
            .push(json!({
                "user": { "login": author },
                "body": body,
                "state": state,
                "submitted_at": timestamp(now)
            }));
    }

    // Gives the id of the new review comment. A reply has the id of the first comment of its thread in `in_reply_to`.
    pub fn add_review_comment(
        &self,
        repository: &str,
        number: i64,
        in_reply_to: Option<i64>,
        author: &str,
        body: &str,
    ) -> i64 {
        self.state.lock().unwrap().review_comment(
            repository,
            number,
            in_reply_to,
            author,
            &InlineComment {
                path: "src/plan.rs".to_string(),
                line: 12,
                body: body.to_string(),
            },
        )
    }

    pub fn set_body(&self, repository: &str, number: i64, body: &str) {
        let mut records = self.state.lock().unwrap();
        let now = records.tick();
        let issue = records
            .issues
            .get_mut(&(repository.to_string(), number))
            .unwrap();
        issue.body = body.to_string();
        issue.updated_at = now;
    }

    pub fn close_issue(&self, repository: &str, number: i64) {
        let mut records = self.state.lock().unwrap();
        let now = records.tick();
        let issue = records
            .issues
            .get_mut(&(repository.to_string(), number))
            .unwrap();
        issue.state = "closed";
        issue.updated_at = now;
    }

    pub fn add_sub_issue(&self, repository: &str, parent: i64, child: i64) {
        self.state
            .lock()
            .unwrap()
            .issues
            .get_mut(&(repository.to_string(), parent))
            .unwrap()
            .sub_issues
            .push(child);
    }

    pub fn add_label(&self, repository: &str, number: i64, label: &str, actor: &str) {
        self.state
            .lock()
            .unwrap()
            .label(repository, number, label, actor);
    }

    pub fn add_blocker(&self, repository: &str, number: i64, blocker: i64) {
        let mut records = self.state.lock().unwrap();
        let now = records.tick();
        let issue = records
            .issues
            .get_mut(&(repository.to_string(), number))
            .unwrap();
        issue.blocked_by.push(blocker);
        issue.updated_at = now;
    }

    pub fn not_modified_count(&self) -> u32 {
        self.state.lock().unwrap().not_modified
    }
}

fn not_found() -> Response {
    (
        StatusCode::NOT_FOUND,
        Json(json!({ "message": "Not Found" })),
    )
        .into_response()
}

async fn account(State(state): State<Shared>, Path(name): Path<String>) -> Response {
    if name == app_login() {
        return Json(json!({ "login": name, "id": BOT_USER_ID, "type": "Bot" })).into_response();
    }
    match state.lock().unwrap().account_types.get(&name) {
        Some(account_type) => {
            Json(json!({ "login": name, "id": 1, "type": account_type })).into_response()
        }
        None => not_found(),
    }
}

async fn convert_manifest(State(state): State<Shared>, Path(code): Path<String>) -> Response {
    if !state.lock().unwrap().manifest_codes.remove(&code) {
        return not_found();
    }
    (
        StatusCode::CREATED,
        Json(json!({
            "id": APP_ID,
            "slug": APP_SLUG,
            "pem": APP_PRIVATE_KEY,
            "client_id": APP_CLIENT_ID,
            "client_secret": APP_CLIENT_SECRET
        })),
    )
        .into_response()
}

#[derive(Deserialize)]
struct CodeExchange {
    client_id: String,
    client_secret: String,
    code: Option<String>,
    grant_type: Option<String>,
    refresh_token: Option<String>,
}

async fn exchange_code(
    State(state): State<Shared>,
    Json(exchange): Json<CodeExchange>,
) -> Response {
    let mut records = state.lock().unwrap();
    if exchange.client_id != APP_CLIENT_ID || exchange.client_secret != APP_CLIENT_SECRET {
        return Json(json!({
            "error": "incorrect_client_credentials",
            "error_description": "The client_id and/or client_secret passed are incorrect."
        }))
        .into_response();
    }
    let login = if exchange.grant_type.as_deref() == Some("refresh_token") {
        let Some(login) = exchange
            .refresh_token
            .and_then(|token| records.refresh_tokens.remove(&token))
        else {
            return Json(json!({
                "error": "bad_refresh_token",
                "error_description": "The refresh token passed is incorrect or expired."
            }))
            .into_response();
        };
        login
    } else {
        let Some(login) = exchange
            .code
            .and_then(|code| records.user_codes.remove(&code))
        else {
            return Json(json!({
                "error": "bad_verification_code",
                "error_description": "The code passed is incorrect or expired."
            }))
            .into_response();
        };
        login
    };
    records.tokens_given += 1;
    let number = records.tokens_given;
    records
        .user_tokens
        .insert(format!("ghu_{number}"), login.clone());
    records
        .refresh_tokens
        .insert(format!("ghr_{number}"), login);
    Json(json!({
        "access_token": format!("ghu_{number}"),
        "expires_in": 28800,
        "refresh_token": format!("ghr_{number}"),
        "refresh_token_expires_in": 15638400,
        "scope": "",
        "token_type": "bearer"
    }))
    .into_response()
}

async fn user(State(state): State<Shared>, headers: HeaderMap) -> Response {
    let token = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.strip_prefix("Bearer "))
        .unwrap_or_default();
    match state.lock().unwrap().user_tokens.get(token) {
        Some(login) => Json(json!({ "login": login })).into_response(),
        None => (
            StatusCode::UNAUTHORIZED,
            Json(json!({ "message": "Bad credentials" })),
        )
            .into_response(),
    }
}

async fn installations(State(state): State<Shared>) -> Response {
    if state.lock().unwrap().repositories.is_empty() {
        return Json(json!([])).into_response();
    }
    Json(json!([{ "id": 1 }])).into_response()
}

async fn installation_token() -> Response {
    (
        StatusCode::CREATED,
        Json(json!({
            "token": INSTALLATION_TOKEN,
            "expires_at": "2099-01-01T00:00:00Z",
            "permissions": {}
        })),
    )
        .into_response()
}

async fn installation_repositories(State(state): State<Shared>) -> Response {
    let records = state.lock().unwrap();
    Json(json!({
        "total_count": records.repositories.len(),
        "repositories": records
            .repositories
            .iter()
            .map(|full_name| json!({
                "full_name": full_name,
                "clone_url": format!("file://{}/{full_name}.git", records.remotes.display()),
                "default_branch": "main"
            }))
            .collect::<Vec<_>>()
    }))
    .into_response()
}

#[derive(Deserialize)]
struct Page {
    page: Option<usize>,
    per_page: Option<usize>,
}

impl Page {
    fn of(&self, items: Vec<Value>) -> Vec<Value> {
        let size = self.per_page.unwrap_or(30);
        let skip = (self.page.unwrap_or(1) - 1) * size;
        items.into_iter().skip(skip).take(size).collect()
    }
}

#[derive(Deserialize)]
struct IssueFilter {
    labels: Option<String>,
    since: Option<String>,
}

async fn issues(
    State(state): State<Shared>,
    Path((owner, repo)): Path<(String, String)>,
    Query(filter): Query<IssueFilter>,
    Query(page): Query<Page>,
    headers: HeaderMap,
) -> Response {
    let repository = format!("{owner}/{repo}");
    let since = filter.since.map(|since| {
        OffsetDateTime::parse(&since, &Rfc3339)
            .unwrap()
            .unix_timestamp()
    });
    let mut records = state.lock().unwrap();
    let mut found: Vec<_> = records
        .issues
        .iter()
        .filter(|((name, _), issue)| {
            *name == repository
                && filter
                    .labels
                    .as_ref()
                    .is_none_or(|label| issue.labels.contains(label))
                && since.is_none_or(|since| issue.updated_at >= since)
        })
        .collect();
    found.sort_by_key(|((_, number), issue)| (issue.updated_at, *number));
    let numbers: Vec<i64> = found.into_iter().map(|((_, number), _)| *number).collect();
    let body = Value::Array(
        page.of(numbers
            .into_iter()
            .map(|number| records.issue_json(&repository, number))
            .collect()),
    );
    let mut hasher = DefaultHasher::new();
    body.to_string().hash(&mut hasher);
    let etag = format!("\"{:x}\"", hasher.finish());
    if headers
        .get(header::IF_NONE_MATCH)
        .is_some_and(|value| value.as_bytes() == etag.as_bytes())
    {
        records.not_modified += 1;
        return StatusCode::NOT_MODIFIED.into_response();
    }
    ([(header::ETAG, etag)], Json(body)).into_response()
}

async fn issue(
    State(state): State<Shared>,
    Path((owner, repo, number)): Path<(String, String, i64)>,
) -> Response {
    let repository = format!("{owner}/{repo}");
    let records = state.lock().unwrap();
    if !records.issues.contains_key(&(repository.clone(), number)) {
        return not_found();
    }
    Json(records.issue_json(&repository, number)).into_response()
}

async fn parent(
    State(state): State<Shared>,
    Path((owner, repo, number)): Path<(String, String, i64)>,
) -> Response {
    let repository = format!("{owner}/{repo}");
    let records = state.lock().unwrap();
    let parent = records
        .issues
        .iter()
        .find(|((name, _), issue)| *name == repository && issue.sub_issues.contains(&number));
    match parent {
        Some(((_, parent), _)) => Json(records.issue_json(&repository, *parent)).into_response(),
        None => not_found(),
    }
}

#[derive(Deserialize)]
struct NewComment {
    body: String,
}

async fn add_issue_comment(
    State(state): State<Shared>,
    Path((owner, repo, number)): Path<(String, String, i64)>,
    Json(comment): Json<NewComment>,
) -> Response {
    let comment = state.lock().unwrap().comment(
        &format!("{owner}/{repo}"),
        number,
        &app_login(),
        &comment.body,
        None,
    );
    (StatusCode::CREATED, Json(comment)).into_response()
}

#[derive(Deserialize)]
struct NewLabels {
    labels: Vec<String>,
}

async fn add_labels(
    State(state): State<Shared>,
    Path((owner, repo, number)): Path<(String, String, i64)>,
    Json(new): Json<NewLabels>,
) -> Response {
    let repository = format!("{owner}/{repo}");
    let mut records = state.lock().unwrap();
    for label in &new.labels {
        records.label(&repository, number, label, &app_login());
    }
    Json(label_list(&records, &repository, number)).into_response()
}

#[derive(Deserialize)]
struct NewPullRequest {
    title: String,
    head: String,
    base: String,
    body: String,
    draft: bool,
}

async fn create_pull_request(
    State(state): State<Shared>,
    Path((owner, repo)): Path<(String, String)>,
    Json(new): Json<NewPullRequest>,
) -> Response {
    let repository = format!("{owner}/{repo}");
    let mut records = state.lock().unwrap();
    let number = records
        .issues
        .keys()
        .filter(|(name, _)| *name == repository)
        .map(|(_, number)| number + 1)
        .max()
        .unwrap_or(1);
    let updated_at = records.tick();
    records.issues.insert(
        (repository.clone(), number),
        Issue {
            title: new.title.clone(),
            body: new.body.clone(),
            author: app_login(),
            pull_request: true,
            state: "open",
            sub_issues: Vec::new(),
            blocked_by: Vec::new(),
            labels: Vec::new(),
            updated_at,
            events: Vec::new(),
            comments: Vec::new(),
            reviews: Vec::new(),
            review_comments: Vec::new(),
        },
    );
    records.pull_requests.push((
        repository.clone(),
        PullRequest {
            number,
            title: new.title,
            body: new.body,
            head: new.head,
            base: new.base,
            draft: new.draft,
        },
    ));
    (
        StatusCode::CREATED,
        Json(json!({
            "number": number,
            "node_id": format!("PR_{number}"),
            "html_url": format!("https://github.com/{repository}/pull/{number}")
        })),
    )
        .into_response()
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct NewCheckRun {
    name: String,
    head_sha: String,
    status: String,
    conclusion: Option<String>,
    output: Option<CheckRunOutput>,
}

async fn create_check_run(
    State(state): State<Shared>,
    Path((owner, repo)): Path<(String, String)>,
    Json(new): Json<NewCheckRun>,
) -> Response {
    let mut records = state.lock().unwrap();
    records.check_runs.push((
        format!("{owner}/{repo}"),
        CheckRun {
            name: new.name,
            head_sha: new.head_sha,
            status: new.status,
            conclusion: new.conclusion,
            output: new.output,
        },
    ));
    let id = records.check_runs.len();
    (StatusCode::CREATED, Json(json!({ "id": id }))).into_response()
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CheckRunUpdate {
    status: String,
    conclusion: String,
}

async fn update_check_run(
    State(state): State<Shared>,
    Path((owner, repo, id)): Path<(String, String, usize)>,
    Json(update): Json<CheckRunUpdate>,
) -> Response {
    let mut records = state.lock().unwrap();
    let Some((repository, check_run)) = records.check_runs.get_mut(id - 1) else {
        return not_found();
    };
    if *repository != format!("{owner}/{repo}") {
        return not_found();
    }
    check_run.status = update.status;
    check_run.conclusion = Some(update.conclusion);
    Json(json!({ "id": id })).into_response()
}

async fn submit_review(
    State(state): State<Shared>,
    Path((owner, repo, number)): Path<(String, String, i64)>,
    Json(review): Json<SubmittedReview>,
) -> Response {
    let repository = format!("{owner}/{repo}");
    let mut records = state.lock().unwrap();
    let now = records.tick();
    records
        .issues
        .get_mut(&(repository.clone(), number))
        .unwrap()
        .reviews
        .push(json!({
            "user": { "login": app_login() },
            "body": review.body,
            "state": "COMMENTED",
            "submitted_at": timestamp(now)
        }));
    for comment in &review.comments {
        records.review_comment(&repository, number, None, &app_login(), comment);
    }
    records.submitted_reviews.push((repository, number, review));
    Json(json!({ "id": 1 })).into_response()
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct GraphQl {
    query: String,
    variables: Value,
}

// Answers the review threads query and the mutation `markPullRequestReadyForReview`, each in one page.
async fn graphql(State(state): State<Shared>, Json(request): Json<GraphQl>) -> Response {
    let variables = &request.variables;
    let mut records = state.lock().unwrap();
    if request.query.contains("markPullRequestReadyForReview") {
        let Some((_, pull_request)) = records
            .pull_requests
            .iter_mut()
            .find(|(_, pull_request)| format!("PR_{}", pull_request.number) == variables["id"])
        else {
            return Json(
                json!({ "data": null, "errors": [{ "message": "Could not resolve to a node" }] }),
            )
            .into_response();
        };
        pull_request.draft = false;
        return Json(json!({
            "data": { "markPullRequestReadyForReview": { "clientMutationId": null } }
        }))
        .into_response();
    }
    let repository = format!(
        "{}/{}",
        variables["owner"].as_str().unwrap(),
        variables["name"].as_str().unwrap()
    );
    let number = variables["number"].as_i64().unwrap();
    let comments = &records.issues[&(repository, number)].review_comments;
    let threads: Vec<Value> = comments
        .iter()
        .filter(|root| root["in_reply_to_id"].is_null())
        .map(|root| {
            let authors: Vec<Value> = comments
                .iter()
                .filter(|comment| {
                    comment["id"] == root["id"] || comment["in_reply_to_id"] == root["id"]
                })
                .map(|comment| {
                    let login = comment["user"]["login"].as_str().unwrap();
                    match login.strip_suffix("[bot]") {
                        Some(bot) => json!({ "author": { "__typename": "Bot", "login": bot } }),
                        None => json!({ "author": { "__typename": "User", "login": login } }),
                    }
                })
                .collect();
            json!({ "isResolved": false, "comments": { "nodes": authors } })
        })
        .collect();
    Json(json!({
        "data": {
            "repository": {
                "pullRequest": {
                    "reviewThreads": {
                        "nodes": threads,
                        "pageInfo": { "hasNextPage": false, "endCursor": null }
                    }
                }
            }
        }
    }))
    .into_response()
}

async fn remove_label(
    State(state): State<Shared>,
    Path((owner, repo, number, name)): Path<(String, String, i64, String)>,
) -> Response {
    let repository = format!("{owner}/{repo}");
    let mut records = state.lock().unwrap();
    let now = records.tick();
    let issue = records
        .issues
        .get_mut(&(repository.clone(), number))
        .unwrap();
    if !issue.labels.contains(&name) {
        return not_found();
    }
    issue.labels.retain(|label| *label != name);
    issue.updated_at = now;
    issue.events.push(json!({
        "event": "unlabeled",
        "actor": { "login": app_login() },
        "label": { "name": name },
        "created_at": timestamp(now)
    }));
    Json(label_list(&records, &repository, number)).into_response()
}

fn label_list(records: &Records, repository: &str, number: i64) -> Vec<Value> {
    records.issues[&(repository.to_string(), number)]
        .labels
        .iter()
        .map(|name| json!({ "name": name }))
        .collect()
}

async fn sub_issues(
    State(state): State<Shared>,
    Path((owner, repo, number)): Path<(String, String, i64)>,
    Query(page): Query<Page>,
) -> Response {
    let repository = format!("{owner}/{repo}");
    let records = state.lock().unwrap();
    let Some(parent) = records.issues.get(&(repository.clone(), number)) else {
        return not_found();
    };
    let children = parent
        .sub_issues
        .iter()
        .map(|child| records.issue_json(&repository, *child))
        .collect();
    Json(page.of(children)).into_response()
}

async fn issue_events(
    State(state): State<Shared>,
    Path((owner, repo, number)): Path<(String, String, i64)>,
    Query(page): Query<Page>,
) -> Response {
    issue_list(&state, owner, repo, number, &page, |issue| &issue.events)
}

fn issue_list(
    state: &Shared,
    owner: String,
    repo: String,
    number: i64,
    page: &Page,
    list: impl Fn(&Issue) -> &Vec<Value>,
) -> Response {
    match state
        .lock()
        .unwrap()
        .issues
        .get(&(format!("{owner}/{repo}"), number))
    {
        Some(issue) => Json(page.of(list(issue).clone())).into_response(),
        None => not_found(),
    }
}

async fn issue_comments(
    State(state): State<Shared>,
    Path((owner, repo, number)): Path<(String, String, i64)>,
    Query(page): Query<Page>,
) -> Response {
    issue_list(&state, owner, repo, number, &page, |issue| &issue.comments)
}

async fn reviews(
    State(state): State<Shared>,
    Path((owner, repo, number)): Path<(String, String, i64)>,
    Query(page): Query<Page>,
) -> Response {
    issue_list(&state, owner, repo, number, &page, |issue| &issue.reviews)
}

async fn review_comments(
    State(state): State<Shared>,
    Path((owner, repo, number)): Path<(String, String, i64)>,
    Query(page): Query<Page>,
) -> Response {
    issue_list(&state, owner, repo, number, &page, |issue| {
        &issue.review_comments
    })
}
