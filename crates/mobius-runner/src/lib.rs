use std::env;
use std::ffi::OsStr;
use std::fmt;
use std::fs;
use std::io;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::time::Duration;

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::schema::v1::{
    AuthenticateRequest, CancelNotification, ContentBlock, ErrorCode, InitializeRequest, McpServer,
    McpServerHttp, NewSessionRequest, NewSessionResponse, PermissionOptionKind, PromptRequest,
    RequestPermissionOutcome, RequestPermissionRequest, RequestPermissionResponse,
    SelectedPermissionOutcome, SessionConfigKind, SessionConfigOption, SessionConfigOptionCategory,
    SessionConfigSelectOptions, SessionId, SetSessionConfigOptionRequest, TextContent,
};
use agent_client_protocol::{
    Agent, ByteStreams, Client, ConnectionTo, Error, Responder, UntypedMessage,
};
use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use mobius_domain::Harness;
use serde_json::Value;
use tokio::io::AsyncReadExt;
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

pub fn find(program: &str, path: &OsStr) -> Option<PathBuf> {
    env::split_paths(path)
        .map(|dir| dir.join(program))
        .find(|file| {
            fs::metadata(file).is_ok_and(|metadata| {
                metadata.is_file() && metadata.permissions().mode() & 0o111 != 0
            })
        })
}

fn agent_env(data_dir: &Path) -> PathBuf {
    data_dir.join("agent-env")
}

fn write_script(file: &Path, text: &str) -> io::Result<()> {
    fs::write(file, text)?;
    fs::set_permissions(file, fs::Permissions::from_mode(0o755))
}

pub fn prepare(data_dir: &Path, gh: &Path) -> io::Result<()> {
    let agent_env = agent_env(data_dir);
    fs::create_dir_all(agent_env.join("gh-config"))?;
    fs::create_dir_all(agent_env.join("bin"))?;
    fs::create_dir_all(agent_env.join("chat-bin"))?;
    // An empty helper clears the credential helpers of the system git config.
    fs::write(agent_env.join("gitconfig"), "[credential]\n\thelper =\n")?;
    write_script(
        &agent_env.join("bin/gh"),
        "#!/bin/sh\necho \"Do not use gh. Use the Mobius tools.\" >&2\nexit 1\n",
    )?;
    write_script(
        &agent_env.join("chat-bin/gh"),
        &format!(
            "#!/bin/sh\nif ! token=$(curl -sS --fail-with-body \"$MOBIUS_GH_TOKEN_URL\"); then\n    echo \"$token\" >&2\n    exit 1\nfi\nGH_TOKEN=$token exec '{}' \"$@\"\n",
            gh.display()
        ),
    )
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

// The token goes to git only in the environment of the process, so no file and no process list shows it. No hook runs, so no code of the repository gets that environment.
fn git(dir: &Path, data_dir: &Path, token: Option<&str>) -> Command {
    let mut command = Command::new("git");
    command
        .args(["-c", "core.hooksPath=/dev/null"])
        .current_dir(dir)
        .env("GIT_CONFIG_GLOBAL", agent_env(data_dir).join("gitconfig"))
        .env("GIT_TERMINAL_PROMPT", "0")
        .kill_on_drop(true);
    if let Some(token) = token {
        let credentials = STANDARD.encode(format!("x-access-token:{token}"));
        command
            .env("GIT_CONFIG_COUNT", "1")
            .env("GIT_CONFIG_KEY_0", "http.extraHeader")
            .env(
                "GIT_CONFIG_VALUE_0",
                format!("AUTHORIZATION: basic {credentials}"),
            );
    }
    command
}

async fn run(command: &mut Command) -> Result<String, String> {
    let args: Vec<String> = command
        .as_std()
        .get_args()
        .map(|arg| arg.to_string_lossy().into_owned())
        .collect();
    let output = command
        .output()
        .await
        .map_err(|error| format!("git {}: {error}", args.join(" ")))?;
    if !output.status.success() {
        return Err(format!(
            "git {}: {}",
            args.join(" "),
            String::from_utf8_lossy(&output.stderr).trim()
        ));
    }
    Ok(String::from_utf8_lossy(&output.stdout).trim().to_string())
}

async fn has_ref(dir: &Path, data_dir: &Path, name: &str) -> Result<bool, String> {
    let status = git(dir, data_dir, None)
        .args(["show-ref", "--verify", "--quiet", name])
        .status()
        .await
        .map_err(|error| format!("git show-ref {name}: {error}"))?;
    Ok(status.success())
}

pub async fn head_contains(data_dir: &Path, worktree: &Path, commit: &str) -> Result<bool, String> {
    let status = git(worktree, data_dir, None)
        .args(["merge-base", "--is-ancestor", commit, "HEAD"])
        .status()
        .await
        .map_err(|error| format!("git merge-base --is-ancestor {commit} HEAD: {error}"))?;
    Ok(status.success())
}

pub async fn rev_parse(data_dir: &Path, worktree: &Path, name: &str) -> Result<String, String> {
    run(git(worktree, data_dir, None).args(["rev-parse", name])).await
}

fn bare_dir(data_dir: &Path, repository: &str) -> PathBuf {
    data_dir.join("repos").join(format!("{repository}.git"))
}

pub fn task_dir(data_dir: &Path, repository: &str, number: i64) -> PathBuf {
    data_dir
        .join("worktrees")
        .join(repository)
        .join(format!("task-{number}"))
}

// Each item is a repository and the name of one of its directories below `worktrees/`, for example ("owner/shop", "task-41").
pub fn worktree_dirs(data_dir: &Path) -> io::Result<Vec<(String, String)>> {
    let mut dirs = Vec::new();
    for owner in subdirectories(&data_dir.join("worktrees"))? {
        for repository in subdirectories(&data_dir.join("worktrees").join(&owner))? {
            let path = data_dir.join("worktrees").join(&owner).join(&repository);
            for name in subdirectories(&path)? {
                dirs.push((format!("{owner}/{repository}"), name));
            }
        }
    }
    Ok(dirs)
}

pub fn scratch_ids(data_dir: &Path) -> io::Result<Vec<String>> {
    subdirectories(&data_dir.join("scratch"))
}

// A directory that does not exist has no subdirectories.
fn subdirectories(dir: &Path) -> io::Result<Vec<String>> {
    if !dir.exists() {
        return Ok(Vec::new());
    }
    let mut names = Vec::new();
    for entry in fs::read_dir(dir)? {
        let entry = entry?;
        if entry.file_type()?.is_dir() {
            names.push(entry.file_name().to_string_lossy().into_owned());
        }
    }
    Ok(names)
}

// A directory of a worktree can go away with `fs`, and `git worktree prune` then removes its record from the bare clone.
pub async fn prune(data_dir: &Path, repository: &str) -> Result<(), String> {
    let bare = bare_dir(data_dir, repository);
    if bare.exists() {
        run(git(&bare, data_dir, None).args(["worktree", "prune"])).await?;
    }
    Ok(())
}

pub fn scratch_dir(data_dir: &Path, id: i64) -> PathBuf {
    data_dir.join("scratch").join(id.to_string())
}

pub fn research_dir(data_dir: &Path, repository: &str, id: i64) -> PathBuf {
    data_dir
        .join("worktrees")
        .join(repository)
        .join(format!("research-{id}"))
}

pub fn judge_dir(data_dir: &Path, repository: &str, id: i64) -> PathBuf {
    data_dir
        .join("worktrees")
        .join(repository)
        .join(format!("judge-{id}"))
}

pub fn review_dir(data_dir: &Path, repository: &str, id: i64) -> PathBuf {
    data_dir
        .join("worktrees")
        .join(repository)
        .join(format!("review-{id}"))
}

// The clone gets its config in a temporary directory, so a failed start leaves no bare clone with part of its config.
pub async fn fetch(
    data_dir: &Path,
    repository: &str,
    clone_url: &str,
    token: &str,
) -> Result<(), String> {
    let bare = bare_dir(data_dir, repository);
    if !bare.exists() {
        let new = bare.with_extension("new");
        if new.exists() {
            fs::remove_dir_all(&new).map_err(|error| format!("{}: {error}", new.display()))?;
        }
        run(git(data_dir, data_dir, Some(token))
            .args(["clone", "--bare", clone_url])
            .arg(&new))
        .await?;
        run(git(&new, data_dir, None).args([
            "config",
            "remote.origin.fetch",
            "+refs/heads/*:refs/remotes/origin/*",
        ]))
        .await?;
        // With `extensions.worktreeConfig`, a `core.bare` in the shared config applies to each worktree.
        run(git(&new, data_dir, None).args(["config", "extensions.worktreeConfig", "true"]))
            .await?;
        run(git(&new, data_dir, None).args(["config", "--worktree", "core.bare", "true"])).await?;
        run(git(&new, data_dir, None).args(["config", "--unset", "core.bare"])).await?;
        fs::rename(&new, &bare).map_err(|error| format!("{}: {error}", bare.display()))?;
    }
    run(git(&bare, data_dir, Some(token)).args(["fetch", "--prune", "origin"])).await?;
    Ok(())
}

// Makes `task-<n>` on the first branch of `mobius/<n>`, `mobius/<n>-2`, ... that is free locally and on `origin`, and gives the branch.
pub async fn add_worktree(
    data_dir: &Path,
    repository: &str,
    number: i64,
    base: &str,
    author_name: &str,
    author_email: &str,
) -> Result<String, String> {
    let bare = bare_dir(data_dir, repository);
    let dir = task_dir(data_dir, repository, number);
    if dir.exists() {
        run(git(&bare, data_dir, None)
            .args(["worktree", "remove", "--force"])
            .arg(&dir))
        .await?;
    }
    let mut branch = format!("mobius/{number}");
    let mut next = 2;
    while has_ref(&bare, data_dir, &format!("refs/heads/{branch}")).await?
        || has_ref(&bare, data_dir, &format!("refs/remotes/origin/{branch}")).await?
    {
        branch = format!("mobius/{number}-{next}");
        next += 1;
    }
    run(git(&bare, data_dir, None)
        .args(["worktree", "add", "--no-track", "-b", &branch])
        .arg(&dir)
        .arg(format!("origin/{base}")))
    .await?;
    run(git(&dir, data_dir, None).args(["config", "--worktree", "user.name", author_name])).await?;
    run(git(&dir, data_dir, None).args(["config", "--worktree", "user.email", author_email]))
        .await?;
    Ok(branch)
}

pub async fn add_detached_worktree(
    data_dir: &Path,
    repository: &str,
    dir: &Path,
    commit: &str,
) -> Result<(), String> {
    run(git(&bare_dir(data_dir, repository), data_dir, None)
        .args(["worktree", "add", "--detach"])
        .arg(dir)
        .arg(commit))
    .await?;
    Ok(())
}

pub async fn remove_worktree(data_dir: &Path, repository: &str, dir: &Path) -> Result<(), String> {
    run(git(&bare_dir(data_dir, repository), data_dir, None)
        .args(["worktree", "remove", "--force"])
        .arg(dir))
    .await?;
    Ok(())
}

pub async fn merge_base(
    data_dir: &Path,
    repository: &str,
    one: &str,
    other: &str,
) -> Result<String, String> {
    run(git(&bare_dir(data_dir, repository), data_dir, None).args(["merge-base", one, other])).await
}

// Merges `origin/<branch>` from the last `fetch`, also when the two branches diverged.
pub async fn pull(data_dir: &Path, worktree: &Path, branch: &str) -> Result<(), String> {
    let remote = format!("refs/remotes/origin/{branch}");
    if has_ref(worktree, data_dir, &remote).await? {
        run(git(worktree, data_dir, None).args(["merge", "--no-edit", &remote])).await?;
    }
    Ok(())
}

pub async fn push(
    data_dir: &Path,
    worktree: &Path,
    token: &str,
    branch: &str,
) -> Result<String, String> {
    run(git(worktree, data_dir, Some(token)).args([
        "push",
        "origin",
        &format!("HEAD:refs/heads/{branch}"),
    ]))
    .await?;
    run(git(worktree, data_dir, None).args(["rev-parse", "HEAD"])).await
}

fn repository_command(
    program: &str,
    cwd: &Path,
    data_dir: &Path,
    bin: PathBuf,
    path: &OsStr,
) -> Command {
    let agent_env = agent_env(data_dir);
    let mut dirs = vec![bin];
    dirs.extend(env::split_paths(path));
    let mut command = Command::new(program);
    command
        .current_dir(cwd)
        .env("GH_CONFIG_DIR", agent_env.join("gh-config"))
        .env("GIT_CONFIG_GLOBAL", agent_env.join("gitconfig"))
        .env("GIT_TERMINAL_PROMPT", "0")
        .env_remove("GH_TOKEN")
        .env_remove("GITHUB_TOKEN")
        .env("PATH", env::join_paths(dirs).unwrap_or_default())
        .kill_on_drop(true);
    command
}

fn command(
    harness: Harness,
    cwd: &Path,
    data_dir: &Path,
    path: &OsStr,
    gh_token_url: Option<&str>,
) -> Command {
    let bin = match gh_token_url {
        Some(_) => agent_env(data_dir).join("chat-bin"),
        None => agent_env(data_dir).join("bin"),
    };
    let mut command = repository_command(program(harness), cwd, data_dir, bin, path);
    if let Some(url) = gh_token_url {
        command.env("MOBIUS_GH_TOKEN_URL", url);
    }
    if harness == Harness::Devin {
        command.arg("acp");
    }
    command
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null());
    command
}

pub enum Check {
    Passed,
    Failed(String),
}

// With no `.mobius/check` in the worktree, the check passes. The output has stdout and stderr in the order of the writes.
pub async fn check(
    data_dir: &Path,
    worktree: &Path,
    path: &OsStr,
    timeout: Duration,
) -> Result<Check, String> {
    if !worktree.join(".mobius/check").exists() {
        return Ok(Check::Passed);
    }
    let mut child = repository_command(
        "/bin/sh",
        worktree,
        data_dir,
        agent_env(data_dir).join("bin"),
        path,
    )
    .args(["-c", "exec ./.mobius/check 2>&1"])
    .stdin(Stdio::null())
    .stdout(Stdio::piped())
    .stderr(Stdio::null())
    .process_group(0)
    .spawn()
    .map_err(|error| format!(".mobius/check: {error}"))?;
    let (Some(id), Some(mut stdout)) = (child.id(), child.stdout.take()) else {
        return Err(".mobius/check: the child has no id or no stdout pipe".to_string());
    };
    let reader = tokio::spawn(async move {
        let mut output = Vec::new();
        stdout.read_to_end(&mut output).await.map(|_| output)
    });
    let finished = tokio::time::timeout(timeout, child.wait()).await;
    // The check leads its own process group, so the kill also ends each process that it started. Until then, such a process can hold the pipe open.
    let _ = Command::new("kill")
        .args(["-KILL", "--", &format!("-{id}")])
        .status()
        .await;
    let _ = child.kill().await;
    let output = reader
        .await
        .map_err(|error| format!(".mobius/check: {error}"))?
        .map_err(|error| format!(".mobius/check: {error}"))?;
    let mut log = String::from_utf8_lossy(&output).into_owned();
    match finished {
        Ok(status) => {
            let status = status.map_err(|error| format!(".mobius/check: {error}"))?;
            if status.success() {
                return Ok(Check::Passed);
            }
        }
        Err(_) => log.push_str(&format!("\n.mobius/check did not end in {timeout:?}.\n")),
    }
    Ok(Check::Failed(log))
}

// Gives the free space in bytes of the file system of the directory. `df` comes from `path`.
pub async fn free_space(dir: &Path, path: &OsStr) -> Result<u64, String> {
    let output = Command::new("df")
        .arg("-Pk")
        .arg(dir)
        .env("PATH", path)
        .output()
        .await
        .map_err(|error| format!("df -Pk: {error}"))?;
    String::from_utf8_lossy(&output.stdout)
        .lines()
        .nth(1)
        .and_then(|line| line.split_whitespace().nth(3))
        .and_then(|kib| kib.parse::<u64>().ok())
        .map(|kib| kib * 1024)
        .ok_or_else(|| format!("df -Pk: {}", String::from_utf8_lossy(&output.stderr).trim()))
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

pub struct Choices {
    pub values: Vec<String>,
    pub current: String,
}

#[derive(Debug)]
pub struct PromptError {
    pub code: i32,
    pub message: String,
    pub data: Option<Value>,
}

impl fmt::Display for PromptError {
    fn fmt(&self, formatter: &mut fmt::Formatter) -> fmt::Result {
        match &self.data {
            Some(data) => write!(formatter, "{} {data}", self.message),
            None => write!(formatter, "{}", self.message),
        }
    }
}

impl std::error::Error for PromptError {}

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
    mcp_url: &str,
    gh_token_url: Option<&str>,
) -> Result<(Session, mpsc::UnboundedReceiver<Value>), String> {
    let mut child = command(harness, cwd, data_dir, path, gh_token_url)
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
    let mcp_url = mcp_url.to_string();
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
            let opened = open(&connection, cwd, mcp_url).await;
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

// Antigravity has no login command. Its login is ACP `authenticate`, which writes a Google link to stderr and waits 300 s for the browser. The function gives `false` when Antigravity is already logged in.
pub async fn log_in_antigravity(cwd: &Path, data_dir: &Path, path: &OsStr) -> Result<bool, String> {
    let harness = Harness::Antigravity;
    let mut child = command(harness, cwd, data_dir, path, None)
        .stderr(Stdio::inherit())
        .spawn()
        .map_err(|error| format!("{}: {error}", program(harness)))?;
    let (Some(stdin), Some(stdout)) = (child.stdin.take(), child.stdout.take()) else {
        return Err(format!("{}: no stdio pipes", program(harness)));
    };
    let transport = ByteStreams::new(stdin.compat_write(), stdout.compat());
    let cwd = cwd.to_path_buf();
    let logged_in = Client
        .builder()
        .connect_with(transport, async move |connection: ConnectionTo<Agent>| {
            connection
                .send_request(InitializeRequest::new(ProtocolVersion::V1))
                .block_task()
                .await?;
            match connection
                .send_request(NewSessionRequest::new(cwd))
                .block_task()
                .await
            {
                Ok(_) => Ok(false),
                Err(error) if error.code == ErrorCode::AuthRequired => {
                    connection
                        .send_request(AuthenticateRequest::new("oauth-personal"))
                        .block_task()
                        .await?;
                    Ok(true)
                }
                Err(error) => Err(error),
            }
        })
        .await;
    let _ = child.kill().await;
    logged_in.map_err(|error| describe(&error))
}

async fn open(
    connection: &ConnectionTo<Agent>,
    cwd: PathBuf,
    mcp_url: String,
) -> Result<NewSessionResponse, Error> {
    connection
        .send_request(InitializeRequest::new(ProtocolVersion::V1))
        .block_task()
        .await?;
    connection
        .send_request(
            NewSessionRequest::new(cwd)
                .mcp_servers(vec![McpServer::Http(McpServerHttp::new("mobius", mcp_url))]),
        )
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

    pub fn models(&self) -> Option<Choices> {
        self.choices(SessionConfigOptionCategory::Model)
    }

    pub fn efforts(&self) -> Option<Choices> {
        self.choices(SessionConfigOptionCategory::ThoughtLevel)
    }

    fn choices(&self, category: SessionConfigOptionCategory) -> Option<Choices> {
        let option = self
            .config_options
            .iter()
            .find(|option| option.category.as_ref() == Some(&category))?;
        let SessionConfigKind::Select(select) = &option.kind else {
            return None;
        };
        Some(Choices {
            values: values(option),
            current: select.current_value.to_string(),
        })
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
    pub async fn prompt(&self, text: &str) -> Result<(), PromptError> {
        self.connection
            .send_request(PromptRequest::new(
                self.id.clone(),
                vec![ContentBlock::Text(TextContent::new(text))],
            ))
            .block_task()
            .await
            .map(|_| ())
            .map_err(|error| PromptError {
                code: i32::from(error.code),
                message: error.message,
                data: error.data,
            })
    }

    pub fn harness(&self) -> Harness {
        self.harness
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

    fn env(command: &Command, name: &str) -> Option<String> {
        command
            .as_std()
            .get_envs()
            .find(|(key, _)| *key == name)
            .and_then(|(_, value)| value)
            .map(|value| value.to_string_lossy().into_owned())
    }

    fn first_path_dir(command: &Command) -> PathBuf {
        env::split_paths(&env(command, "PATH").unwrap())
            .next()
            .unwrap()
    }

    #[test]
    fn find_gives_the_path_of_an_executable_file() {
        let dir = tempfile::tempdir().unwrap();
        let program = dir.path().join("gh");
        fs::write(&program, "#!/bin/sh\n").unwrap();
        fs::set_permissions(&program, fs::Permissions::from_mode(0o755)).unwrap();
        fs::write(dir.path().join("curl"), "#!/bin/sh\n").unwrap();
        let path = env::join_paths([dir.path()]).unwrap();

        assert_eq!(find("gh", &path), Some(program));
        assert_eq!(find("curl", &path), None);
        assert_eq!(find("git", &path), None);
    }

    #[test]
    fn a_session_with_no_gh_token_url_gets_the_gh_stub() {
        let data_dir = tempfile::tempdir().unwrap();
        prepare(data_dir.path(), Path::new("/usr/bin/gh")).unwrap();

        let command = command(
            Harness::ClaudeCode,
            data_dir.path(),
            data_dir.path(),
            OsStr::new("/usr/bin"),
            None,
        );

        let bin = first_path_dir(&command);
        assert_eq!(bin, data_dir.path().join("agent-env/bin"));
        assert_eq!(env(&command, "MOBIUS_GH_TOKEN_URL"), None);
        let gh = std::process::Command::new(bin.join("gh")).output().unwrap();
        assert!(!gh.status.success());
        assert_eq!(
            String::from_utf8(gh.stderr).unwrap(),
            "Do not use gh. Use the Mobius tools.\n"
        );
    }

    #[test]
    fn a_session_with_a_gh_token_url_gets_the_gh_wrapper() {
        let data_dir = tempfile::tempdir().unwrap();
        prepare(data_dir.path(), Path::new("/usr/bin/gh")).unwrap();

        let command = command(
            Harness::ClaudeCode,
            data_dir.path(),
            data_dir.path(),
            OsStr::new("/usr/bin"),
            Some("http://127.0.0.1:8080/gh-token/abc"),
        );

        assert_eq!(
            first_path_dir(&command),
            data_dir.path().join("agent-env/chat-bin")
        );
        assert_eq!(
            env(&command, "MOBIUS_GH_TOKEN_URL").as_deref(),
            Some("http://127.0.0.1:8080/gh-token/abc")
        );
    }

    #[test]
    fn a_git_command_gets_the_token_only_in_its_environment() {
        let data_dir = tempfile::tempdir().unwrap();

        let command = git(data_dir.path(), data_dir.path(), Some("ghs_secret"));

        assert_eq!(env(&command, "GIT_CONFIG_COUNT").as_deref(), Some("1"));
        assert_eq!(
            env(&command, "GIT_CONFIG_KEY_0").as_deref(),
            Some("http.extraHeader")
        );
        assert_eq!(
            env(&command, "GIT_CONFIG_VALUE_0").as_deref(),
            Some("AUTHORIZATION: basic eC1hY2Nlc3MtdG9rZW46Z2hzX3NlY3JldA==")
        );
        assert_eq!(
            env(&command, "GIT_CONFIG_GLOBAL"),
            Some(
                data_dir
                    .path()
                    .join("agent-env/gitconfig")
                    .display()
                    .to_string()
            )
        );
        assert_eq!(env(&command, "GIT_TERMINAL_PROMPT").as_deref(), Some("0"));
        let args: Vec<_> = command.as_std().get_args().collect();
        assert_eq!(args, ["-c", "core.hooksPath=/dev/null"]);
    }

    #[test]
    fn a_git_command_with_no_token_has_no_extra_header() {
        let data_dir = tempfile::tempdir().unwrap();

        let command = git(data_dir.path(), data_dir.path(), None);

        assert_eq!(env(&command, "GIT_CONFIG_COUNT"), None);
        assert_eq!(env(&command, "GIT_CONFIG_VALUE_0"), None);
    }

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
