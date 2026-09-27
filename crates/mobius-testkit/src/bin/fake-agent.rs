use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use agent_client_protocol::schema::v1::{
    CancelNotification, ConfigOptionUpdate, ContentBlock, ContentChunk, InitializeRequest,
    InitializeResponse, McpServer, NewSessionRequest, NewSessionResponse, PromptRequest,
    PromptResponse, SessionConfigKind, SessionConfigOption, SessionConfigOptionCategory,
    SessionConfigSelectOption, SessionId, SessionNotification, SessionUpdate,
    SetSessionConfigOptionRequest, SetSessionConfigOptionResponse, StopReason,
};
use agent_client_protocol::{Agent, Client, ConnectionTo, Error, Responder, Stdio};
use rmcp::ServiceExt;
use rmcp::model::CallToolRequestParams;
use rmcp::transport::StreamableHttpClientTransport;
use serde::Deserialize;
use serde_json::{Map, Value};
use tokio::sync::Notify;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Script {
    #[serde(default)]
    login_required: bool,
    options: BTreeMap<String, Vec<String>>,
    #[serde(default)]
    prompts: Vec<Prompt>,
}

// A prompt with `when` answers each prompt that contains its text. The other prompts answer in order.
#[derive(Clone, Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct Prompt {
    when: Option<String>,
    #[serde(default)]
    reply: Vec<String>,
    // The JSON of each `session/update` to send before the reply.
    #[serde(default)]
    updates: Vec<String>,
    #[serde(default)]
    hang: bool,
    // The Lead reply is the JSON of the Mobius tool list.
    #[serde(default)]
    list_tools: bool,
    // The Lead reply is the text of the tool result, after `error: ` for an error result. Each `{shell}` in a string argument becomes the trimmed stdout of `shell`.
    call: Option<Call>,
    // The Lead reply is the stdout and the stderr of `/bin/sh -c` with this command, then `exit <code>`. The command runs before `call`.
    shell: Option<String>,
}

#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
struct Call {
    tool: String,
    #[serde(default)]
    arguments: Map<String, Value>,
}

struct State {
    script: Script,
    options: Vec<SessionConfigOption>,
    prompts_done: usize,
    mcp_url: Option<String>,
    // The cancel signal of the turn that runs. A `session/cancel` with no turn has no effect.
    turn: Option<Arc<Notify>>,
}

type Shared = Arc<Mutex<State>>;

fn options(script: &Script) -> Vec<SessionConfigOption> {
    script
        .options
        .iter()
        .map(|(id, values)| {
            let choices: Vec<SessionConfigSelectOption> = values
                .iter()
                .map(|value| SessionConfigSelectOption::new(value.clone(), value.clone()))
                .collect();
            let category = match id.as_str() {
                "model" => SessionConfigOptionCategory::Model,
                "thought_level" => SessionConfigOptionCategory::ThoughtLevel,
                "mode" => SessionConfigOptionCategory::Mode,
                other => SessionConfigOptionCategory::Other(other.to_string()),
            };
            SessionConfigOption::select(id.clone(), id.clone(), values[0].clone(), choices)
                .category(category)
        })
        .collect()
}

async fn mobius_reply(mcp_url: &str, prompt: &Prompt, stdout: &str) -> Option<String> {
    if !prompt.list_tools && prompt.call.is_none() {
        return None;
    }
    let client = ().serve(StreamableHttpClientTransport::from_uri(mcp_url)).await.unwrap();
    let reply = match &prompt.call {
        Some(call) => {
            let arguments = call
                .arguments
                .iter()
                .map(|(name, value)| {
                    let value = match value {
                        Value::String(text) => Value::String(text.replace("{shell}", stdout)),
                        other => other.clone(),
                    };
                    (name.clone(), value)
                })
                .collect();
            let result = client
                .call_tool(CallToolRequestParams::new(call.tool.clone()).with_arguments(arguments))
                .await
                .unwrap();
            let text = &result.content[0].as_text().unwrap().text;
            match result.is_error {
                Some(true) => format!("error: {text}"),
                _ => text.clone(),
            }
        }
        None => serde_json::to_string(&client.list_all_tools().await.unwrap()).unwrap(),
    };
    client.cancel().await.unwrap();
    Some(reply)
}

async fn play(
    connection: &ConnectionTo<Client>,
    session: &SessionId,
    prompt: &Prompt,
    mcp_url: &str,
    cancel: &Notify,
) -> Result<StopReason, Error> {
    for update in &prompt.updates {
        let update: SessionUpdate = serde_json::from_str(update).unwrap();
        connection.send_notification(SessionNotification::new(session.clone(), update))?;
    }
    let output = prompt.shell.as_ref().map(|command| {
        std::process::Command::new("/bin/sh")
            .arg("-c")
            .arg(command)
            .output()
            .unwrap()
    });
    let stdout = output
        .as_ref()
        .map(|output| String::from_utf8_lossy(&output.stdout).trim().to_string())
        .unwrap_or_default();
    let mobius = mobius_reply(mcp_url, prompt, &stdout).await;
    let shell = output.map(|output| {
        format!(
            "{}{}exit {}",
            String::from_utf8_lossy(&output.stdout),
            String::from_utf8_lossy(&output.stderr),
            output.status.code().unwrap()
        )
    });
    for text in prompt.reply.iter().chain(&mobius).chain(&shell) {
        connection.send_notification(SessionNotification::new(
            session.clone(),
            SessionUpdate::AgentMessageChunk(ContentChunk::new(ContentBlock::from(text.as_str()))),
        ))?;
    }
    if prompt.hang {
        cancel.notified().await;
        return Ok(StopReason::Cancelled);
    }
    Ok(StopReason::EndTurn)
}

#[tokio::main]
async fn main() -> Result<(), Error> {
    let path = std::env::args()
        .nth(1)
        .expect("usage: fake-agent <script.toml>");
    let script: Script = toml::from_str(&std::fs::read_to_string(&path).unwrap()).unwrap();
    let mcp_url_path = std::path::Path::new(&path).with_file_name("mcp_url");
    let state = Shared::new(Mutex::new(State {
        options: options(&script),
        script,
        prompts_done: 0,
        mcp_url: None,
        turn: None,
    }));
    let new_session = state.clone();
    let set_option = state.clone();
    let prompt = state.clone();
    let cancel = state;
    Agent
        .builder()
        .on_receive_request(
            async move |request: InitializeRequest,
                        responder: Responder<InitializeResponse>,
                        _connection| {
                responder.respond(InitializeResponse::new(request.protocol_version))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: NewSessionRequest,
                        responder: Responder<NewSessionResponse>,
                        _connection| {
                let mut state = new_session.lock().unwrap();
                if state.script.login_required {
                    return responder.respond_with_error(Error::auth_required());
                }
                let mcp_url = request.mcp_servers.iter().find_map(|server| match server {
                    McpServer::Http(http) if http.name == "mobius" => Some(http.url.clone()),
                    _ => None,
                });
                std::fs::write(&mcp_url_path, mcp_url.as_deref().unwrap_or_default()).unwrap();
                state.mcp_url = mcp_url;
                responder.respond(
                    NewSessionResponse::new("fake-session").config_options(state.options.clone()),
                )
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: SetSessionConfigOptionRequest,
                        responder: Responder<SetSessionConfigOptionResponse>,
                        connection: ConnectionTo<Client>| {
                let options = {
                    let mut state = set_option.lock().unwrap();
                    let option = state
                        .options
                        .iter_mut()
                        .find(|option| option.id == request.config_id)
                        .ok_or_else(Error::invalid_params)?;
                    let SessionConfigKind::Select(select) = &mut option.kind else {
                        return responder.respond_with_error(Error::invalid_params());
                    };
                    select.current_value = request
                        .value
                        .as_value_id()
                        .ok_or_else(Error::invalid_params)?
                        .clone();
                    state.options.clone()
                };
                connection.send_notification(SessionNotification::new(
                    request.session_id,
                    SessionUpdate::ConfigOptionUpdate(ConfigOptionUpdate::new(options.clone())),
                ))?;
                responder.respond(SetSessionConfigOptionResponse::new(options))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: PromptRequest,
                        responder: Responder<PromptResponse>,
                        connection: ConnectionTo<Client>| {
                let (script_prompt, mcp_url, cancel) = {
                    let mut state = prompt.lock().unwrap();
                    let text: String = request
                        .prompt
                        .iter()
                        .filter_map(|block| match block {
                            ContentBlock::Text(text) => Some(text.text.as_str()),
                            _ => None,
                        })
                        .collect();
                    let prompts = &state.script.prompts;
                    let script_prompt = match prompts
                        .iter()
                        .find(|prompt| prompt.when.as_ref().is_some_and(|when| text.contains(when)))
                    {
                        Some(prompt) => prompt.clone(),
                        None => {
                            let prompt = prompts
                                .iter()
                                .filter(|prompt| prompt.when.is_none())
                                .nth(state.prompts_done)
                                .cloned()
                                .unwrap_or_default();
                            state.prompts_done += 1;
                            prompt
                        }
                    };
                    let cancel = Arc::new(Notify::new());
                    state.turn = Some(cancel.clone());
                    (
                        script_prompt,
                        state.mcp_url.clone().unwrap_or_default(),
                        cancel,
                    )
                };
                let state = prompt.clone();
                // The work leaves the dispatch loop, so a `session/cancel` can arrive during the turn.
                connection.spawn({
                    let connection = connection.clone();
                    async move {
                        let stop_reason = play(
                            &connection,
                            &request.session_id,
                            &script_prompt,
                            &mcp_url,
                            &cancel,
                        )
                        .await;
                        state.lock().unwrap().turn = None;
                        responder.respond_with_result(stop_reason.map(PromptResponse::new))
                    }
                })
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_notification(
            async move |_notification: CancelNotification, _connection| {
                if let Some(turn) = &cancel.lock().unwrap().turn {
                    turn.notify_one();
                }
                Ok(())
            },
            agent_client_protocol::on_receive_notification!(),
        )
        .connect_to(Stdio::new())
        .await
}
