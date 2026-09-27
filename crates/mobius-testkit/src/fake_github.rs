use std::collections::{BTreeMap, HashMap, HashSet};
use std::hash::{DefaultHasher, Hash, Hasher};
use std::sync::{Arc, Mutex};

use axum::extract::{Path, Query, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use serde::Deserialize;
use serde_json::{Value, json};
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;
use tokio::net::TcpListener;

pub const APP_ID: i64 = 7;
pub const APP_SLUG: &str = "mobius-test";
pub const APP_PRIVATE_KEY: &str = include_str!("app_private_key.pem");
pub const APP_CLIENT_ID: &str = "Iv23test";
pub const APP_CLIENT_SECRET: &str = "client-secret";

#[derive(Default)]
struct Records {
    account_types: HashMap<String, &'static str>,
    manifest_codes: HashSet<String>,
    user_codes: HashMap<String, String>,
    user_tokens: HashMap<String, String>,
    tokens_given: u32,
    repositories: Vec<String>,
    issues: BTreeMap<(String, i64), Issue>,
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
}

impl FakeGitHub {
    pub async fn start() -> FakeGitHub {
        let state = Shared::new(Mutex::new(Records {
            clock: OffsetDateTime::now_utc().unix_timestamp(),
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
            .route(
                "/repos/{owner}/{repo}/issues/{number}/events",
                get(issue_events),
            )
            .route(
                "/repos/{owner}/{repo}/issues/{number}/comments",
                get(issue_comments),
            )
            .route("/repos/{owner}/{repo}/pulls/{number}/reviews", get(reviews))
            .route(
                "/repos/{owner}/{repo}/pulls/{number}/comments",
                get(review_comments),
            )
            .with_state(state.clone());
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!("http://{}", listener.local_addr().unwrap());
        tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        FakeGitHub { url, state }
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

    pub fn add_repository(&self, full_name: &str) {
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
                labels: Vec::new(),
                updated_at,
                events: Vec::new(),
                comments: Vec::new(),
                reviews: Vec::new(),
                review_comments: Vec::new(),
            },
        );
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
        let mut records = self.state.lock().unwrap();
        let now = records.tick();
        records
            .issues
            .get_mut(&(repository.to_string(), number))
            .unwrap()
            .comments
            .push(json!({
                "user": { "login": author },
                "body": body,
                "created_at": timestamp(now)
            }));
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
        let mut records = self.state.lock().unwrap();
        let now = records.tick();
        records.last_review_comment_id += 1;
        let id = records.last_review_comment_id;
        records
            .issues
            .get_mut(&(repository.to_string(), number))
            .unwrap()
            .review_comments
            .push(json!({
                "id": id,
                "user": { "login": author },
                "body": body,
                "path": "src/plan.rs",
                "line": 12,
                "in_reply_to_id": in_reply_to,
                "created_at": timestamp(now)
            }));
        id
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
        let mut records = self.state.lock().unwrap();
        let now = records.tick();
        let issue = records
            .issues
            .get_mut(&(repository.to_string(), number))
            .unwrap();
        issue.labels.push(label.to_string());
        issue.updated_at = now;
        issue.events.push(json!({
            "event": "labeled",
            "actor": { "login": actor },
            "label": { "name": label },
            "created_at": timestamp(now)
        }));
    }

    pub fn not_modified_count(&self) -> u32 {
        self.state.lock().unwrap().not_modified
    }
}

fn issue_json(repository: &str, number: i64, issue: &Issue) -> Value {
    let mut json = json!({
        "number": number,
        "title": issue.title,
        "body": issue.body,
        "user": { "login": issue.author },
        "html_url": format!("https://github.com/{repository}/issues/{number}"),
        "state": issue.state,
        "updated_at": timestamp(issue.updated_at),
        "labels": issue.labels.iter().map(|name| json!({ "name": name })).collect::<Vec<_>>()
    });
    if issue.pull_request {
        json["pull_request"] =
            json!({ "url": format!("https://api.github.com/repos/{repository}/pulls/{number}") });
    }
    json
}

fn not_found() -> Response {
    (
        StatusCode::NOT_FOUND,
        Json(json!({ "message": "Not Found" })),
    )
        .into_response()
}

async fn account(State(state): State<Shared>, Path(name): Path<String>) -> Response {
    match state.lock().unwrap().account_types.get(&name) {
        Some(account_type) => Json(json!({ "login": name, "type": account_type })).into_response(),
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
    code: String,
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
    let Some(login) = records.user_codes.remove(&exchange.code) else {
        return Json(json!({
            "error": "bad_verification_code",
            "error_description": "The code passed is incorrect or expired."
        }))
        .into_response();
    };
    records.tokens_given += 1;
    let number = records.tokens_given;
    records.user_tokens.insert(format!("ghu_{number}"), login);
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
            "token": "ghs_installation",
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
            .map(|full_name| json!({ "full_name": full_name }))
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
    let body = Value::Array(
        page.of(found
            .into_iter()
            .map(|((_, number), issue)| issue_json(&repository, *number, issue))
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
    match state
        .lock()
        .unwrap()
        .issues
        .get(&(repository.clone(), number))
    {
        Some(issue) => Json(issue_json(&repository, number, issue)).into_response(),
        None => not_found(),
    }
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
        .map(|child| {
            issue_json(
                &repository,
                *child,
                &records.issues[&(repository.clone(), *child)],
            )
        })
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
