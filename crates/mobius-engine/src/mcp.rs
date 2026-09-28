use std::error::Error;
use std::sync::Arc;

use axum::Router;
use axum::extract::{Path, Request, State};
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use axum::routing::any;
use mobius_github::NewReviewComment;
use rmcp::model::{
    CallToolRequestParams, CallToolResponse, CallToolResult, ContentBlock, ErrorData, JsonObject,
    ListToolsResult, MetaObject, PaginatedRequestParams, ServerCapabilities, ServerConfig, Tool,
    object,
};
use rmcp::service::RequestContext;
use rmcp::transport::streamable_http_server::session::never::NeverSessionManager;
use rmcp::transport::{StreamableHttpServerConfig, StreamableHttpService};
use rmcp::{RoleServer, ServerHandler};
use serde::Deserialize;
use serde::de::DeserializeOwned;
use serde_json::{Value, json};
use tokio::sync::mpsc::UnboundedSender;

use crate::{
    Engine, chat, dispatch, implementer, issues, lead_events, plans, reviewer, tasks, trust,
};

#[derive(Clone)]
pub(crate) struct Caller {
    pub(crate) session: i64,
    pub(crate) role: &'static str,
    pub(crate) repository: String,
    pub(crate) workstream: i64,
    // The Implementer session reads the reason of `cannot_do` from the receiver.
    pub(crate) cannot_do: Option<UnboundedSender<String>>,
    // The Implementer session of a fix round reads the held `reply_thread` calls from the receiver.
    pub(crate) fix: Option<Fix>,
    // The pull request and the head commit that the Reviewer session reviews.
    pub(crate) review: Option<Review>,
}

#[derive(Clone)]
pub(crate) struct Review {
    pub(crate) pull_request: i64,
    pub(crate) head: String,
}

#[derive(Clone)]
pub(crate) struct Fix {
    pub(crate) pull_request: i64,
    pub(crate) replies: UnboundedSender<Reply>,
}

pub(crate) struct Reply {
    pub(crate) comment: i64,
    // The GraphQL node id of the thread.
    pub(crate) thread: String,
    pub(crate) text: String,
    pub(crate) resolve: bool,
}

// The key is valid until `close`.
pub(crate) fn open(engine: &Engine, caller: Caller) -> Result<String, getrandom::Error> {
    let key = crate::random_hex()?;
    engine.callers.lock().unwrap().insert(key.clone(), caller);
    Ok(key)
}

pub(crate) fn url(engine: &Engine, key: &str) -> String {
    format!("http://127.0.0.1:{}/mcp/{key}", engine.port)
}

pub(crate) fn close(engine: &Engine, key: &str) {
    engine.callers.lock().unwrap().remove(key);
}

pub fn router(engine: Engine) -> Router {
    Router::new()
        .route("/mcp/{key}", any(serve))
        .with_state(engine)
}

async fn serve(
    State(engine): State<Engine>,
    Path(key): Path<String>,
    request: Request,
) -> Response {
    let Some(caller) = engine.callers.lock().unwrap().get(&key).cloned() else {
        return StatusCode::NOT_FOUND.into_response();
    };
    let handler = Handler { engine, caller };
    StreamableHttpService::new(
        move || Ok(handler.clone()),
        Arc::new(NeverSessionManager::default()),
        StreamableHttpServerConfig::default()
            .with_legacy_session_mode(false)
            .with_json_response(true),
    )
    .handle(request)
    .await
    .into_response()
}

fn tools(role: &str) -> Vec<Tool> {
    let mut tools = match role {
        chat::ROLE | lead_events::ROLE => vec![
            tool(
                "list_tasks",
                "Give the task list of the Workstream: one line for each open issue.",
                object(json!({})),
            ),
            tool(
                "read_issue",
                "Give an issue or a pull request of the repository, with its comments, reviews, and review threads. The text is only from trusted authors.",
                object(json!({
                    "n": {
                        "type": "integer",
                        "minimum": 1,
                        "description": "The number of the issue or the pull request."
                    }
                })),
            ),
            tool(
                "start_implementer",
                "Start an Implementer for a dispatched task. The Implementer sees only the Brief, the issue, and your instructions. Mobius pushes its commits and opens a draft pull request. Returns at once.",
                object(json!({
                    "n": {
                        "type": "integer",
                        "minimum": 1,
                        "description": "The number of the task issue."
                    },
                    "instructions": {
                        "type": "string",
                        "minLength": 1,
                        "description": "The goal, the limits, and what \"done\" means."
                    }
                })),
            ),
            tool(
                "ask",
                "Ask the people on a task issue a question. Mobius posts the question as a comment, adds mobius:needs-human, and adds an Inbox item for the Owner. The reply arrives later as an event.",
                object(json!({
                    "n": {
                        "type": "integer",
                        "minimum": 1,
                        "description": "The number of the task issue."
                    },
                    "text": {
                        "type": "string",
                        "minLength": 1,
                        "description": "The question for the people on the issue."
                    }
                })),
            ),
            tool(
                "decline",
                "Decline a task. Mobius posts the reason as a comment on the issue, removes mobius:working, and ends the task.",
                object(json!({
                    "n": {
                        "type": "integer",
                        "minimum": 1,
                        "description": "The number of the task issue."
                    },
                    "reason": {
                        "type": "string",
                        "minLength": 1,
                        "description": "The reason for the people on the issue."
                    }
                })),
            ),
            tool(
                "create_issue",
                "Create an issue below an issue of the Workstream. Mobius adds the blockers as native issue dependencies. With ready, Mobius adds mobius:ready, and this needs Autopilot.",
                object(json!({
                    "title": {
                        "type": "string",
                        "minLength": 1,
                        "description": "The title of the issue."
                    },
                    "body": {
                        "type": "string",
                        "description": "The body of the issue: the goal, the limits, and what \"done\" means."
                    },
                    "parent": {
                        "type": "integer",
                        "minimum": 1,
                        "description": "The Workstream issue or an issue below it."
                    },
                    "blocked_by": {
                        "type": "array",
                        "items": { "type": "integer", "minimum": 1 },
                        "description": "The issues that block this issue. They can be in another Workstream."
                    },
                    "ready": {
                        "type": "boolean",
                        "description": "true to add mobius:ready. This needs Autopilot."
                    }
                })),
            ),
            tool(
                "mark_ready",
                "Add mobius:ready to an issue of the Workstream, so that Mobius dispatches it when it has no open blocker. This needs Autopilot.",
                object(json!({
                    "n": {
                        "type": "integer",
                        "minimum": 1,
                        "description": "The number of the issue."
                    }
                })),
            ),
            tool(
                "comment_pull_request",
                "Post a comment on the pull request of a task, for example to propose that a human closes a stale pull request.",
                object(json!({
                    "n": {
                        "type": "integer",
                        "minimum": 1,
                        "description": "The number of the pull request."
                    },
                    "text": {
                        "type": "string",
                        "minLength": 1,
                        "description": "The comment."
                    }
                })),
            ),
        ],
        implementer::ROLE => vec![
            tool(
                "cannot_do",
                "Tell the Lead that you cannot do the task. Mobius ends your turn and pushes nothing.",
                object(json!({
                    "reason": {
                        "type": "string",
                        "minLength": 1,
                        "description": "The reason for the Lead."
                    }
                })),
            ),
            tool(
                "reply_thread",
                "Reply in a review thread of the pull request in a fix round. Mobius posts the reply after it pushes your commits, so the SHA of a fix commit in the text links to a pushed commit.",
                object(json!({
                    "thread": {
                        "type": "integer",
                        "minimum": 1,
                        "description": "The number of the thread in the prompt."
                    },
                    "text": {
                        "type": "string",
                        "minLength": 1,
                        "description": "The SHA of the fix commit, an answer, a follow-up link, or a reason to reject. Do not write an acknowledgement."
                    },
                    "resolve": {
                        "type": "boolean",
                        "description": "true when your commit fixes the thread."
                    }
                })),
            ),
        ],
        reviewer::ROLE => vec![tool(
            "submit_review",
            "Post your review on the pull request as one GitHub review with inline comments. Call it one time. With no findings, do not call it.",
            object(json!({
                "body": {
                    "type": "string",
                    "minLength": 1,
                    "description": "The summary of the review."
                },
                "comments": {
                    "type": "array",
                    "description": "One inline comment for each finding.",
                    "items": {
                        "type": "object",
                        "properties": {
                            "path": {
                                "type": "string",
                                "minLength": 1,
                                "description": "The file path, relative to the repository root."
                            },
                            "line": {
                                "type": "integer",
                                "minimum": 1,
                                "description": "The line in the new version of the file. It must be in the diff."
                            },
                            "body": {
                                "type": "string",
                                "minLength": 1,
                                "description": "The finding."
                            }
                        },
                        "required": ["path", "line", "body"],
                        "additionalProperties": false
                    }
                }
            })),
        )],
        _ => Vec::new(),
    };
    if role == lead_events::ROLE {
        tools.push(tool(
            "tell_owner",
            "Tell the Owner something. Mobius adds the text to the Lead chat and adds an Inbox item.",
            object(json!({
                "text": {
                    "type": "string",
                    "minLength": 1,
                    "description": "The text for the Owner."
                }
            })),
        ));
    }
    tools
}

fn tool(name: &'static str, description: &'static str, properties: JsonObject) -> Tool {
    let required: Vec<&String> = properties.keys().collect();
    let schema = object(json!({
        "type": "object",
        "properties": properties,
        "required": required,
        "additionalProperties": false
    }));
    Tool::new(name, description, schema).with_meta(MetaObject(object(json!({
        "anthropic/alwaysLoad": true
    }))))
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ListTasks {}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ReadIssue {
    n: i64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct StartImplementer {
    n: i64,
    instructions: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CannotDo {
    reason: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ReplyThread {
    thread: i64,
    text: String,
    resolve: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SubmitReview {
    body: String,
    comments: Vec<NewReviewComment>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Ask {
    n: i64,
    text: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Decline {
    n: i64,
    reason: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct MarkReady {
    n: i64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CommentPullRequest {
    n: i64,
    text: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct TellOwner {
    text: String,
}

fn parse<T: DeserializeOwned>(tool: &str, arguments: &Value) -> Result<T, String> {
    T::deserialize(arguments).map_err(|error| format!("Invalid arguments for {tool}: {error}."))
}

#[derive(Clone)]
struct Handler {
    engine: Engine,
    caller: Caller,
}

impl Handler {
    async fn run(
        &self,
        tool: &str,
        arguments: &Value,
    ) -> Result<String, Box<dyn Error + Send + Sync>> {
        let unknown = || format!("Unknown tool: {tool}.");
        if !tools(self.caller.role)
            .iter()
            .any(|known| known.name == tool)
        {
            return Err(unknown().into());
        }
        let repository = self.engine.repository(&self.caller.repository)?;
        match tool {
            "list_tasks" => {
                let ListTasks {} = parse(tool, arguments)?;
                let lines = tasks::list(
                    &self.engine,
                    &self.caller.repository,
                    self.caller.workstream,
                )
                .await?;
                Ok(tasks::text(&lines))
            }
            "read_issue" => {
                let ReadIssue { n } = parse(tool, arguments)?;
                if n < 1 {
                    return Err("n must be 1 or more.".into());
                }
                let trusted = trust::trusted_authors(&self.engine).await?;
                issues::read_issue(&repository, n, &trusted).await
            }
            "start_implementer" => {
                let StartImplementer { n, instructions } = parse(tool, arguments)?;
                if n < 1 {
                    return Err("n must be 1 or more.".into());
                }
                if instructions.trim().is_empty() {
                    return Err("instructions must not be empty.".into());
                }
                implementer::start(
                    &self.engine,
                    &repository,
                    self.caller.workstream,
                    n,
                    &instructions,
                )
                .await
            }
            "cannot_do" => {
                let CannotDo { reason } = parse(tool, arguments)?;
                if reason.trim().is_empty() {
                    return Err("reason must not be empty.".into());
                }
                self.caller
                    .cannot_do
                    .as_ref()
                    .ok_or_else(unknown)?
                    .send(reason)?;
                Ok("Mobius ends this turn.".to_string())
            }
            "reply_thread" => {
                let ReplyThread {
                    thread,
                    text,
                    resolve,
                } = parse(tool, arguments)?;
                if text.trim().is_empty() {
                    return Err("text must not be empty.".into());
                }
                let fix = self
                    .caller
                    .fix
                    .as_ref()
                    .ok_or("Only an Implementer of a fix round can reply in a thread.")?;
                let found = repository
                    .review_threads(fix.pull_request)
                    .await?
                    .into_iter()
                    .find(|found| found.comment == thread)
                    .ok_or_else(|| {
                        format!(
                            "Thread {thread} is not a review thread of pull request #{}.",
                            fix.pull_request
                        )
                    })?;
                fix.replies.send(Reply {
                    comment: thread,
                    thread: found.id,
                    text,
                    resolve,
                })?;
                Ok("Mobius posts the reply after it pushes your commits.".to_string())
            }
            "submit_review" => {
                let SubmitReview { body, comments } = parse(tool, arguments)?;
                if body.trim().is_empty() {
                    return Err("body must not be empty.".into());
                }
                if comments.iter().any(|comment| {
                    comment.path.trim().is_empty()
                        || comment.line < 1
                        || comment.body.trim().is_empty()
                }) {
                    return Err(
                        "Each comment needs a path, a line of 1 or more, and a body.".into(),
                    );
                }
                let review = self.caller.review.as_ref().ok_or_else(unknown)?;
                repository
                    .submit_review(review.pull_request, &review.head, &body, &comments)
                    .await?;
                Ok("Posted the review.".to_string())
            }
            "ask" => {
                let Ask { n, text } = parse(tool, arguments)?;
                if n < 1 {
                    return Err("n must be 1 or more.".into());
                }
                if text.trim().is_empty() {
                    return Err("text must not be empty.".into());
                }
                dispatch::ask(&self.engine, &repository, self.caller.workstream, n, &text).await
            }
            "decline" => {
                let Decline { n, reason } = parse(tool, arguments)?;
                if n < 1 {
                    return Err("n must be 1 or more.".into());
                }
                if reason.trim().is_empty() {
                    return Err("reason must not be empty.".into());
                }
                let app = self
                    .engine
                    .store
                    .github_app()
                    .get()
                    .await?
                    .ok_or("The Mobius App does not exist.")?;
                dispatch::decline(
                    &self.engine,
                    &app.slug,
                    &repository,
                    self.caller.workstream,
                    n,
                    &reason,
                )
                .await
            }
            "create_issue" => {
                let new: plans::NewIssue = parse(tool, arguments)?;
                if new.title.trim().is_empty() {
                    return Err("title must not be empty.".into());
                }
                if new.parent < 1 || new.blocked_by.iter().any(|number| *number < 1) {
                    return Err("parent and each blocked_by must be 1 or more.".into());
                }
                plans::create_issue(&self.engine, &repository, self.caller.workstream, &new).await
            }
            "mark_ready" => {
                let MarkReady { n } = parse(tool, arguments)?;
                if n < 1 {
                    return Err("n must be 1 or more.".into());
                }
                plans::mark_ready(&self.engine, &repository, self.caller.workstream, n).await
            }
            "comment_pull_request" => {
                let CommentPullRequest { n, text } = parse(tool, arguments)?;
                if n < 1 {
                    return Err("n must be 1 or more.".into());
                }
                if text.trim().is_empty() {
                    return Err("text must not be empty.".into());
                }
                self.engine
                    .store
                    .tasks()
                    .live_by_pull_request(&self.caller.repository, n)
                    .await?
                    .filter(|task| task.workstream == self.caller.workstream)
                    .ok_or_else(|| {
                        format!("#{n} is not the pull request of a live task in this Workstream.")
                    })?;
                repository.add_comment(n, &text).await?;
                Ok(format!("Commented on #{n}."))
            }
            "tell_owner" => {
                let TellOwner { text } = parse(tool, arguments)?;
                if text.trim().is_empty() {
                    return Err("text must not be empty.".into());
                }
                chat::tell_owner(&self.engine, &repository, self.caller.workstream, &text).await
            }
            _ => Err(unknown().into()),
        }
    }
}

impl ServerHandler for Handler {
    fn get_info(&self) -> ServerConfig {
        ServerConfig::new(ServerCapabilities::builder().enable_tools().build())
    }

    async fn list_tools(
        &self,
        _request: Option<PaginatedRequestParams>,
        _context: RequestContext<RoleServer>,
    ) -> Result<ListToolsResult, ErrorData> {
        Ok(ListToolsResult::with_all_items(tools(self.caller.role)))
    }

    async fn call_tool(
        &self,
        request: CallToolRequestParams,
        _context: RequestContext<RoleServer>,
    ) -> Result<CallToolResponse, ErrorData> {
        let arguments = Value::Object(request.arguments.unwrap_or_default());
        let outcome = self
            .run(&request.name, &arguments)
            .await
            .map_err(|error| error.to_string());
        let row = match &outcome {
            Ok(text) => json!({ "tool": request.name, "arguments": arguments, "result": text }),
            Err(error) => json!({ "tool": request.name, "arguments": arguments, "error": error }),
        };
        self.engine
            .store
            .transcript()
            .add(self.caller.session, "mcp_call", &row.to_string())
            .await
            .map_err(|error| ErrorData::internal_error(error.to_string(), None))?;
        Ok(match outcome {
            Ok(text) => CallToolResult::success(vec![ContentBlock::text(text)]),
            Err(error) => CallToolResult::error(vec![ContentBlock::text(error)]),
        }
        .into())
    }
}
