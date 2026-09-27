use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use agent_client_protocol::schema::v1::{
    CancelNotification, ConfigOptionUpdate, ContentBlock, ContentChunk, InitializeRequest,
    InitializeResponse, NewSessionRequest, NewSessionResponse, PromptRequest, PromptResponse,
    SessionConfigKind, SessionConfigOption, SessionConfigOptionCategory, SessionConfigSelectOption,
    SessionId, SessionNotification, SessionUpdate, SetSessionConfigOptionRequest,
    SetSessionConfigOptionResponse, StopReason,
};
use agent_client_protocol::{Agent, Client, ConnectionTo, Error, Responder, Stdio};
use serde::Deserialize;
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

#[derive(Clone, Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct Prompt {
    #[serde(default)]
    reply: Vec<String>,
    #[serde(default)]
    hang: bool,
}

struct State {
    script: Script,
    options: Vec<SessionConfigOption>,
    prompts_done: usize,
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

async fn play(
    connection: &ConnectionTo<Client>,
    session: &SessionId,
    prompt: &Prompt,
    cancel: &Notify,
) -> Result<StopReason, Error> {
    for text in &prompt.reply {
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
    let script: Script = toml::from_str(&std::fs::read_to_string(path).unwrap()).unwrap();
    let state = Shared::new(Mutex::new(State {
        options: options(&script),
        script,
        prompts_done: 0,
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
            async move |_request: NewSessionRequest,
                        responder: Responder<NewSessionResponse>,
                        _connection| {
                let state = new_session.lock().unwrap();
                if state.script.login_required {
                    return responder.respond_with_error(Error::auth_required());
                }
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
                let (script_prompt, cancel) = {
                    let mut state = prompt.lock().unwrap();
                    let script_prompt = state
                        .script
                        .prompts
                        .get(state.prompts_done)
                        .cloned()
                        .unwrap_or_default();
                    state.prompts_done += 1;
                    let cancel = Arc::new(Notify::new());
                    state.turn = Some(cancel.clone());
                    (script_prompt, cancel)
                };
                let state = prompt.clone();
                // The work leaves the dispatch loop, so a `session/cancel` can arrive during the turn.
                connection.spawn({
                    let connection = connection.clone();
                    async move {
                        let stop_reason =
                            play(&connection, &request.session_id, &script_prompt, &cancel).await;
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
