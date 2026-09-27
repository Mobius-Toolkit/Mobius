use std::error::Error;
use std::sync::Arc;

use axum::Router;
use axum::extract::{Path, Request, State};
use axum::http::StatusCode;
use axum::response::{IntoResponse, Response};
use axum::routing::any;
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

use crate::{Engine, chat, dispatch, issues, lead_events, tasks, trust};

#[derive(Clone)]
pub(crate) struct Caller {
    pub(crate) session: i64,
    pub(crate) role: &'static str,
    pub(crate) repository: String,
    pub(crate) workstream: i64,
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
    match role {
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
        ],
        _ => Vec::new(),
    }
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
struct Decline {
    n: i64,
    reason: String,
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
