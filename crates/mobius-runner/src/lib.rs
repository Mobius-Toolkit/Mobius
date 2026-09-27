use std::env;
use std::ffi::OsStr;
use std::fs;
use std::io;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::Stdio;

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::schema::v1::{
    CancelNotification, ContentBlock, InitializeRequest, NewSessionRequest, NewSessionResponse,
    PermissionOptionKind, PromptRequest, RequestPermissionOutcome, RequestPermissionRequest,
    RequestPermissionResponse, SelectedPermissionOutcome, SessionConfigKind, SessionConfigOption,
    SessionConfigOptionCategory, SessionConfigSelectOptions, SessionId,
    SetSessionConfigOptionRequest, TextContent,
};
use agent_client_protocol::{
    Agent, ByteStreams, Client, ConnectionTo, Error, Responder, UntypedMessage,
};
use mobius_domain::Harness;
use serde_json::Value;
use tokio::process::{Child, Command};
use tokio::sync::{mpsc, oneshot};
use tokio::task::JoinHandle;
use tokio_util::compat::{TokioAsyncReadCompatExt, TokioAsyncWriteCompatExt};

const MEMORY_LINES: usize = 200;

pub fn program(harness: Harness) -> &'static str {
    match harness {
        Harness::ClaudeCode => "claude-agent-acp",
        Harness::Antigravity => "agy_acp_server",
        Harness::Devin => "devin",
    }
}

pub fn on_path(program: &str, path: &OsStr) -> bool {
    env::split_paths(path).any(|dir| {
        fs::metadata(dir.join(program))
            .is_ok_and(|metadata| metadata.is_file() && metadata.permissions().mode() & 0o111 != 0)
    })
}

fn agent_env(data_dir: &Path) -> PathBuf {
    data_dir.join("agent-env")
}

pub fn prepare(data_dir: &Path) -> io::Result<()> {
    let agent_env = agent_env(data_dir);
    fs::create_dir_all(agent_env.join("gh-config"))?;
    fs::create_dir_all(agent_env.join("bin"))?;
    // An empty helper clears the credential helpers of the system git config.
    fs::write(agent_env.join("gitconfig"), "[credential]\n\thelper =\n")?;
    let gh = agent_env.join("bin/gh");
    fs::write(
        &gh,
        "#!/bin/sh\necho \"Do not use gh. Use the Mobius tools.\" >&2\nexit 1\n",
    )?;
    fs::set_permissions(&gh, fs::Permissions::from_mode(0o755))
}

pub fn lead_dir(data_dir: &Path, repository: &str, workstream: i64) -> io::Result<PathBuf> {
    let dir = data_dir
        .join("leads")
        .join(repository)
        .join(workstream.to_string());
    fs::create_dir_all(&dir)?;
    Ok(dir)
}

pub fn memory(dir: &Path) -> io::Result<String> {
    let text = match fs::read_to_string(dir.join("MEMORY.md")) {
        Ok(text) => text,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(String::new()),
        Err(error) => return Err(error),
    };
    let lines: Vec<&str> = text.lines().collect();
    if lines.len() <= MEMORY_LINES {
        return Ok(text);
    }
    Ok(format!(
        "{}\nMEMORY.md is too long. Make it shorter.\n",
        lines[..MEMORY_LINES].join("\n")
    ))
}

fn command(harness: Harness, cwd: &Path, data_dir: &Path, path: &OsStr) -> Command {
    let agent_env = agent_env(data_dir);
    let mut dirs = vec![agent_env.join("bin")];
    dirs.extend(env::split_paths(path));
    let mut command = Command::new(program(harness));
    if harness == Harness::Devin {
        command.arg("acp");
    }
    command
        .current_dir(cwd)
        .env("GH_CONFIG_DIR", agent_env.join("gh-config"))
        .env("GIT_CONFIG_GLOBAL", agent_env.join("gitconfig"))
        .env("GIT_TERMINAL_PROMPT", "0")
        .env_remove("GH_TOKEN")
        .env_remove("GITHUB_TOKEN")
        .env("PATH", env::join_paths(dirs).unwrap_or_default())
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .kill_on_drop(true);
    command
}

pub struct Session {
    connection: ConnectionTo<Agent>,
    id: SessionId,
    harness: Harness,
    config_options: Vec<SessionConfigOption>,
    stop: oneshot::Sender<()>,
    task: JoinHandle<Result<(), Error>>,
    child: Child,
}

fn describe(error: &Error) -> String {
    match &error.data {
        Some(data) => format!("{} {data}", error.message),
        None => error.message.clone(),
    }
}

// The receiver gives the `params` object of each `session/update` notification of the session.
pub async fn start(
    harness: Harness,
    cwd: &Path,
    data_dir: &Path,
    path: &OsStr,
) -> Result<(Session, mpsc::UnboundedReceiver<Value>), String> {
    let mut child = command(harness, cwd, data_dir, path)
        .spawn()
        .map_err(|error| format!("{}: {error}", program(harness)))?;
    let (Some(stdin), Some(stdout)) = (child.stdin.take(), child.stdout.take()) else {
        return Err(format!("{}: no stdio pipes", program(harness)));
    };
    let transport = ByteStreams::new(stdin.compat_write(), stdout.compat());
    let (updates_sender, updates) = mpsc::unbounded_channel();
    let (ready_sender, ready) = oneshot::channel();
    let (stop, stopped) = oneshot::channel::<()>();
    let cwd = cwd.to_path_buf();
    let connection = Client
        .builder()
        .on_receive_notification(
            async move |message: UntypedMessage, _connection| {
                if message.method == "session/update" {
                    // With no receiver, the session is at its end and the update has no reader.
                    let _ = updates_sender.send(message.params);
                }
                Ok(())
            },
            agent_client_protocol::on_receive_notification!(),
        )
        .on_receive_request(
            async move |request: RequestPermissionRequest,
                        responder: Responder<RequestPermissionResponse>,
                        _connection| {
                let allow = request.options.iter().find(|option| {
                    matches!(
                        option.kind,
                        PermissionOptionKind::AllowOnce | PermissionOptionKind::AllowAlways
                    )
                });
                let outcome = match allow {
                    Some(option) => RequestPermissionOutcome::Selected(
                        SelectedPermissionOutcome::new(option.option_id.clone()),
                    ),
                    None => RequestPermissionOutcome::Cancelled,
                };
                responder.respond(RequestPermissionResponse::new(outcome))
            },
            agent_client_protocol::on_receive_request!(),
        )
        // The connection holds an unhandled request of a session until a handler takes it, so this last handler refuses all other requests.
        .on_receive_request(
            async move |_request: UntypedMessage, responder: Responder<Value>, _connection| {
                responder.respond_with_error(Error::method_not_found())
            },
            agent_client_protocol::on_receive_request!(),
        )
        .connect_with(transport, async move |connection: ConnectionTo<Agent>| {
            let opened = open(&connection, cwd).await;
            let failed = opened.is_err();
            let _ = ready_sender.send(opened.map(|response| (connection.clone(), response)));
            if !failed {
                tokio::select! {
                    _ = stopped => {}
                    () = connection.incoming_closed() => {}
                }
            }
            Ok(())
        });
    let task = tokio::spawn(connection);
    let (connection, response) = ready
        .await
        .map_err(|_| format!("{} exited before session/new", program(harness)))?
        .map_err(|error| describe(&error))?;
    Ok((
        Session {
            connection,
            id: response.session_id,
            harness,
            config_options: response.config_options.unwrap_or_default(),
            stop,
            task,
            child,
        },
        updates,
    ))
}

async fn open(connection: &ConnectionTo<Agent>, cwd: PathBuf) -> Result<NewSessionResponse, Error> {
    connection
        .send_request(InitializeRequest::new(ProtocolVersion::V1))
        .block_task()
        .await?;
    connection
        .send_request(NewSessionRequest::new(cwd))
        .block_task()
        .await
}

fn values(option: &SessionConfigOption) -> Vec<String> {
    let SessionConfigKind::Select(select) = &option.kind else {
        return Vec::new();
    };
    match &select.options {
        SessionConfigSelectOptions::Ungrouped(options) => options
            .iter()
            .map(|option| option.value.to_string())
            .collect(),
        SessionConfigSelectOptions::Grouped(groups) => groups
            .iter()
            .flat_map(|group| &group.options)
            .map(|option| option.value.to_string())
            .collect(),
        _ => Vec::new(),
    }
}

fn full_auto_mode(harness: Harness) -> Option<&'static str> {
    match harness {
        Harness::ClaudeCode => Some("bypassPermissions"),
        Harness::Antigravity => Some("yolo"),
        Harness::Devin => None,
    }
}

impl Session {
    pub fn acp_id(&self) -> &str {
        &self.id.0
    }

    pub async fn configure(&mut self, model: &str, effort: Option<&str>) -> Result<(), String> {
        self.set(SessionConfigOptionCategory::Model, "model", model)
            .await?;
        if let Some(effort) = effort {
            self.set(
                SessionConfigOptionCategory::ThoughtLevel,
                "thought_level",
                effort,
            )
            .await?;
        }
        if let Some(mode) = full_auto_mode(self.harness) {
            self.set(SessionConfigOptionCategory::Mode, "mode", mode)
                .await?;
        }
        Ok(())
    }

    async fn set(
        &mut self,
        category: SessionConfigOptionCategory,
        name: &str,
        value: &str,
    ) -> Result<(), String> {
        let option = self
            .config_options
            .iter()
            .find(|option| option.category.as_ref() == Some(&category))
            .ok_or_else(|| format!("The Harness has no {name} option."))?;
        let values = values(option);
        let refused = || {
            format!(
                "The Harness refuses {name} \"{value}\". The Harness has: {}.",
                values.join(", ")
            )
        };
        if !values.iter().any(|known| known == value) {
            return Err(refused());
        }
        let response = self
            .connection
            .send_request(SetSessionConfigOptionRequest::new(
                self.id.clone(),
                option.id.clone(),
                value,
            ))
            .block_task()
            .await
            .map_err(|error| format!("{} {}", refused(), describe(&error)))?;
        self.config_options = response.config_options;
        Ok(())
    }

    // Holds until the turn ends. A turn that `cancel` stops also gives `Ok`.
    pub async fn prompt(&self, text: &str) -> Result<(), String> {
        self.connection
            .send_request(PromptRequest::new(
                self.id.clone(),
                vec![ContentBlock::Text(TextContent::new(text))],
            ))
            .block_task()
            .await
            .map(|_| ())
            .map_err(|error| describe(&error))
    }

    pub fn cancel(&self) {
        // A failed send means that the connection is closed, so no turn runs.
        let _ = self
            .connection
            .send_notification(CancelNotification::new(self.id.clone()));
    }

    pub async fn close(mut self) {
        let _ = self.stop.send(());
        let _ = self.task.await;
        let _ = self.child.kill().await;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn memory_is_empty_with_no_file() {
        let dir = tempfile::tempdir().unwrap();

        assert_eq!(memory(dir.path()).unwrap(), "");
    }

    #[test]
    fn memory_keeps_a_short_file() {
        let dir = tempfile::tempdir().unwrap();
        fs::write(dir.path().join("MEMORY.md"), "- [Plans](plans.md)\n").unwrap();

        assert_eq!(memory(dir.path()).unwrap(), "- [Plans](plans.md)\n");
    }

    #[test]
    fn memory_keeps_the_first_200_lines_of_a_long_file() {
        let dir = tempfile::tempdir().unwrap();
        let lines: Vec<String> = (1..=201).map(|line| format!("line {line}")).collect();
        fs::write(dir.path().join("MEMORY.md"), lines.join("\n")).unwrap();

        let memory = memory(dir.path()).unwrap();

        assert!(memory.starts_with("line 1\nline 2\n"));
        assert!(memory.ends_with("line 200\nMEMORY.md is too long. Make it shorter.\n"));
        assert!(!memory.contains("line 201"));
    }
}
