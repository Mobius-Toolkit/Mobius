# Install Mobius

This document takes you from a clean host to a Mobius server that is connected to GitHub. Mobius runs on an Apple silicon Mac, or on a Linux x86_64 server. Mobius is one program, and the web UI is in the program.

If the Rust version of Mobius (Mobius-Toolkit/Mobius-rust) runs on the host now, read [step 10](#10-switch-from-the-rust-version) first.

## 1. Prerequisites

Install these programs:

- `git`
- `curl`
- `tar`
- `gh`, the GitHub CLI
- Node 22 or later, for the Claude Code adapter

## 2. Install Mobius

1. Download the release archive for your host. The archive has one file: the program `mobius`.

   On an Apple silicon Mac:

   ```sh
   rm -rf ~/.mobius/app && mkdir -p ~/.mobius/app
   curl -fsSL https://github.com/Mobius-Toolkit/Mobius/releases/latest/download/mobius-aarch64-apple-darwin.tar.gz \
     | tar -xz -C ~/.mobius/app
   ```

   On Linux x86_64:

   ```sh
   rm -rf ~/.mobius/app && mkdir -p ~/.mobius/app
   curl -fsSL https://github.com/Mobius-Toolkit/Mobius/releases/latest/download/mobius-x86_64-unknown-linux-gnu.tar.gz \
     | tar -xz -C ~/.mobius/app
   ```

2. Add `~/.mobius/app` to `PATH` in your shell profile:

   ```sh
   export PATH="$HOME/.mobius/app:$PATH"
   ```

3. Make sure that the program starts:

   ```sh
   mobius --help
   ```

Use `curl`, not a browser. macOS blocks an unsigned program that a browser downloads. `curl` does not mark the file.

An upgrade replaces the program in its directory. Thus, your OS user must have write access to that directory.

## 3. Install and log in to the Harnesses

Read this caution about the Harness terms. This is not legal advice.

- Claude Code: the Agent SDK documentation says: "Unless previously approved, Anthropic does not allow third party developers to offer claude.ai login or rate limits for their products, including agents built on the Claude Agent SDK." Mobius offers no login. It runs the unmodified adapter on your own account, only for you.
- Antigravity: the FAQ says: "Using third party software, tools, or services to access Antigravity is a violation of our Terms of Service, and severely degrades the experience for legitimate product users."
- You decide on the risk. You can bind a Role to a different Harness.

Install each Harness that you want to use, as your own OS user. Mobius needs only the Harnesses that your config uses.

### Claude Code

1. Install Node 22 or later.
2. Install the adapter. The adapter includes its own Claude Code program.

   ```sh
   npm install -g @agentclientprotocol/claude-agent-acp
   ```

3. Install the `claude` CLI, and log in with `claude`. On a headless server, open the link on another device, and paste the code that the browser shows.

Do not use `claude setup-token` with `CLAUDE_CODE_OAUTH_TOKEN`. The token is then in the environment of Mobius.

### Devin

1. Install Devin:

   ```sh
   curl -fsSL https://cli.devin.ai/install.sh | bash
   ```

2. Log in:

   ```sh
   devin auth login
   ```

   On a headless server, add `--force-manual-token-flow`, and paste the token.

### Antigravity

1. Download the `agy_acp_server` zip for your host from the [`antigravity-acp` entry of the ACP registry](https://github.com/agentclientprotocol/registry/tree/main/registry/antigravity-acp).
2. Put `agy_acp_server` on `PATH`.
3. Log in with `mobius init` (step 4). `agy_acp_server` has no login command.

When Antigravity is not logged in, `mobius init` starts its Google login. Antigravity writes a link to the terminal and opens a browser. Log in within 300 seconds. If the login fails, Antigravity also removes its old login.

On a headless server, the browser cannot reach the server. Nobody tested these methods:

- After the login on another device, the browser opens an address such as `http://127.0.0.1:<port>/...`, and the page fails. Copy the full address. On the server, run `curl '<address>'` before the 300 seconds end.
- Log in on a Linux machine with a browser, then copy `~/.gemini/antigravity-acp/` to the server. A copy from macOS does not work, because macOS keeps the token in the Keychain.

### Skill directories

Each agent loads your own skills from the user skill directory of its Harness:

| Harness | Skill directory |
| --- | --- |
| Claude Code | `~/.claude/skills/` |
| Antigravity | `~/.gemini/config/skills/` |
| Devin | `~/.agents/skills/` or `~/.config/devin/skills/` |

## 4. `mobius init`

Run:

```sh
mobius init
```

`mobius init` writes the config file:

1. It asks for the access password two times. The password must have 8 characters or more.
2. It asks for the GitHub logins of the trusted users.
3. It starts each Harness on `PATH`, and reads its models and efforts. When Antigravity is not logged in, it starts the Antigravity login of step 3 first. It writes one line for each Harness that is not on `PATH` or that fails, and skips that Harness. All Harnesses together must give 2 models or more.
4. For each Role, it asks for the Harness, the model, and the effort. Enter selects the value in brackets. The Reviewer must have a different Harness or a different model than the Implementer.
5. It writes `~/.mobius/config.toml`. When you set `MOBIUS_CONFIG`, it writes to that path.

Use an access password that you use nowhere else.

`mobius init` writes only a new file. If the file exists, `mobius init` stops.

## 5. The config file

The config file is a TOML file. Mobius reads it one time when it starts, so you must restart Mobius after each change. Mobius does not start when the file has an unknown key or an incorrect value.

`mobius init` writes these keys:

- `access_password`: the password of the web UI.
- `trusted_users`: the GitHub logins of the users that the agents act for.
- `roles`: the `harness`, the `model` and the `effort` of each Role (`lead`, `triager`, `implementer`, `researcher`, `reviewer` and `judge`). A Devin or Claude Code Role must have an effort. An Antigravity Role must not have an effort.

You can add these keys:

| Key | Default | Value |
| --- | --- | --- |
| `trusted_bots` | `[]` | The GitHub logins of bots that Mobius trusts, as it trusts the trusted users |
| `data_dir` | `~/.mobius` | The directory of `mobius.db` and of the work directories of the agents |
| `max_agents` | `4` | The maximum number of agents of the Roles with `counts_in_max_agents` that run at the same time |
| `max_checks` | `1` | The maximum number of local checks that run at the same time |
| `max_fix_rounds` | `7` | The maximum number of fix rounds of a task |
| `max_check_attempts` | `3` | The maximum number of failed local checks in one Implementer round |
| `max_worker_restarts` | `3` | The maximum number of restarts of a failed Worker. After the last restart, the task goes to a human |
| `check_timeout` | `"15m"` | The time limit of one local check |
| `review_quiet_period` | `"10m"` | The Judge starts when the reviews of a pull request have no change for this time |
| `stale_pr_age` | `"168h"` | A pull request with a conflict that is older than this time gets no conflict round |
| `lead_idle_timeout` | `"1h"` | A Lead session with no new message or event ends after this time |
| `poll_interval` | `"30s"` | The time between two polls of GitHub |
| `housekeeper_interval` | `"1h"` | The time between two runs of the Housekeeper |

A duration is a text with a number and a unit for each part: `s`, `m` or `h`. For example, `"90s"`, `"1h30m"` or `"168h"`. Mobius does not accept a day unit or a space.

Each Role can also have these keys:

- `max`: the maximum number of agents of the Role that run at the same time. The default is 8 for the Lead, and 2 for each other Role.
- `counts_in_max_agents`: `true` when the agents of the Role count in `max_agents`. The default is `false` for the Lead and the Triager, and `true` for each other Role.

## 6. Start

Run `mobius` in a terminal, as your own OS user:

```sh
mobius
```

On a server, run it in `tmux` or in your own service manager.

Caution: "Workers run in full auto as your OS user and can read all your files."

Do not run Mobius as the root user. The Claude Code adapter refuses the full auto mode for root.

Mobius does not start when a Harness of your config, `gh`, `curl`, or `tar` is not on `PATH`.

If necessary, set these variables in the shell before you start Mobius:

- `IP` and `PORT` give the address. The default is `127.0.0.1:6363`. `IP` must be `127.0.0.1` or `0.0.0.0`.
- `MOBIUS_CONFIG` gives the path of the config file. The default is `~/.mobius/config.toml`.
- If your repositories use Rust, set `CARGO_TARGET_DIR` or `RUSTC_WRAPPER=sccache`. Without one of them, each worktree builds from zero.

## 7. Network and HTTPS

The session cookie of the UI is Secure. A browser keeps a Secure cookie only from an HTTPS address. Chrome also keeps it from `http://localhost`. Thus, a login from a phone or from another device works only over HTTPS.

On a laptop, open `http://localhost:6363` in Chrome.

On a server, or for a phone, use Tailscale Serve:

1. Install Tailscale on the server and on your devices, and add them to your tailnet.
2. On the server, run:

   ```sh
   tailscale serve --bg 6363
   ```

3. Open `https://<host>.<tailnet>.ts.net` on your device. Only the members of your tailnet can open the UI, and the UI has HTTPS.

Caution: "Mobius works behind each HTTPS tunnel to `127.0.0.1:6363`. Do not open the port to the internet."

## 8. Connect GitHub

1. Open the UI, and log in with the access password.
2. On the **Connect GitHub** page, type your personal account or your organization in **Account or organization**.
3. Type the **App name**. GitHub App names are unique on all of GitHub, so use a name that no other App has, for example with your account name.
4. Click **Create the App**. GitHub shows the form of the new App. Create the App within one hour.
5. On the **Connect GitHub** page, click **Install <App slug> on your repositories**, and select the repositories. When you install the App, GitHub also authorizes you as a user of the App.

Mobius polls GitHub and finds these repositories.

Caution: "The chat Lead acts on GitHub as you, through the Mobius App."

Protect the default branch of each repository. Then only a pull request can change the default branch.

## 9. Upgrade and backup

When a newer release exists, the sidebar shows the **Upgrade** button and the new version. To upgrade Mobius:

1. Click **Upgrade**. Mobius shows the changes of the new release.
2. Click **Upgrade** in the dialog. Mobius downloads the release and holds each new agent.
3. Wait. Mobius waits until no agent runs, and then it restarts with the new version. Mobius updates its database when it starts.

To stop the wait, click **Cancel upgrade**. Mobius then keeps the current version. If the upgrade fails, the sidebar shows the error and Mobius keeps the current version.

To upgrade by hand:

1. Stop Mobius.
2. Run the `rm` and `curl` lines of step 2 again.
3. Start Mobius. Mobius updates its database when it starts.

To make a backup, use one of these methods:

- Stop Mobius, and copy `~/.mobius`.
- While Mobius runs, use the `sqlite3` CLI: `sqlite3 ~/.mobius/mobius.db ".backup <backup file>"`.

## 10. Switch from the Rust version

This version of Mobius uses the same `~/.mobius` as the Rust version: the same config file and the same database. The database stays valid, and Mobius keeps its data.

1. Stop the Rust server. Mobius does not start while another Mobius server uses the data directory.
2. Make a backup: copy `~/.mobius`.
3. Do step 2 of this document. The `rm` line removes the Rust program and its `public` directory. The other files in `~/.mobius` stay.
4. Examine each duration in `~/.mobius/config.toml`. The Rust version accepts durations such as `"7days"` or `"1h 30m"`. Change them to the syntax of step 5, for example `"168h"` or `"1h30m"`.
5. Start `mobius`. On the first start, Mobius takes over the migration versions of the Rust database, and applies only the new migrations.
6. If you open the UI over HTTP from another device, set up HTTPS (step 7). The Rust version accepts a login over HTTP, but this version does not.

To go back to the Rust version, stop Mobius, put the backup back in `~/.mobius`, and install the Rust release again.
