# Context loading over ACP

V = verified in docs or source. U = unverified or inferred. [n] = source.

| | Claude Code (`claude-agent-acp` 0.81.2) | Antigravity (`agy_acp_server` 1.2.1) | Devin (`devin acp` 3000.11.3) |
|---|---|---|---|
| A. Instructions | Managed, `~/.claude/CLAUDE.md`, `CLAUDE.md` / `.claude/CLAUDE.md` / `CLAUDE.local.md` in cwd and all parents; subdirs on demand. `AGENTS.md` only if no `CLAUDE.md` is found (v2.1.277+) V [4]. Adapter passes `settingSources: ["user","project","local"]`, the CLI default, so ACP = CLI V [1][3]. A client can override it with `_meta.claudeCode.options`, which spreads after it V [1]. | Docs: `AGENTS.md`/`GEMINI.md`/`.agents/rules/*.md` up to workspace root; global `~/.gemini/{AGENTS,GEMINI}.md`, `~/.gemini/config/rules/*.md` V [8]. ACP server ships its own harness, not `agy`. Harness text says "walks up from cwd to repo root" for `GEMINI.md`/`AGENTS.md` V [11]. Global root in ACP = `$GEMINI_HOME/config` U [10][11]. | `AGENTS.md`, `AGENTS.local.md`, `AGENT.md`, `.windsurfrules`, `CLAUDE.md`, `.devin/rules/`, `.windsurf/`, `.cursor/rules/` at root and dirs up to cwd; global `~/.config/devin/AGENTS.md`, `~/.devin/rules/`, `~/.claude/CLAUDE.md` V [13]. Docs do not state an ACP difference U. |
| B. Skills | Yes. `~/.claude/skills/`, `.claude/skills/` in cwd and parents, nested dirs V [5]. No `.agents/skills` in docs U. Extra dir: ACP `additionalDirectories` → SDK `--add-dir` → loads its `.claude/skills/` V [1][5]. Also `_meta.claudeCode.options.skills` / `plugins` V [3]. | Yes. ACP: project `.agents/skills/`, `.gemini/skills` under cwd; global `$GEMINI_HOME/config/skills/` and `$GEMINI_HOME/antigravity-cli/skills/` V [10]. Extra dir: `skills.json` `entries` in a customization root V [11]; move `GEMINI_HOME` V [10]. No env var for one extra dir U. | Yes. `.agents/skills/`, `.devin/skills/`, `.windsurf/skills/`, `.claude/skills/`, `~/.agents/skills/`, `~/.config/devin/skills/`, `~/.codeium/<channel>/skills/` V [14][15]. ACP accepts `additionalDirectories` V [18]; skill load from it U. |
| C. System prompt at `session/new` | Yes. `_meta.systemPrompt`: a string replaces the preset; an object keeps preset `claude_code` and forwards `append` V [1][3]. | No client field found. Server builds `system_instructions` itself; `_meta` is read only for API keys U [10]. Use the first `session/prompt`, or `AGENTS.md` in cwd. | No ACP field found U [18]. Alternatives: `SessionStart` hook `additionalContext` V [17]; `devin acp --agent-type` V [16]. Else the first `session/prompt`. |
| D. Config-dir env var | `CLAUDE_CONFIG_DIR` V [6]; adapter reads it too V [2]. Credentials follow it: file and macOS Keychain entry are keyed to the dir, so a new login is necessary V [7]. | `GEMINI_HOME` moves the whole tree V [10]. Token file `<home>/antigravity-acp/acp_token.json` follows it; docs say "re-authenticate" V [10]. macOS Keychain entry (`gemini`/`antigravity-acp`) is not keyed to the home, so login can survive on macOS U [10]. ACP login is separate from the `agy` CLI login V [10]. `agy` CLI has no `GEMINI_HOME` string U. | No Devin env var in docs. Binary reads `XDG_CONFIG_HOME` and `XDG_DATA_HOME` V [18]. Config is in `~/.config/devin/`; credentials are in `~/.local/share/devin/credentials.toml` V [18]. So `XDG_CONFIG_HOME` keeps login, `XDG_DATA_HOME` loses it U. |

## Sources

1. claude-agent-acp `src/acp-agent.ts` L8308-8325, L8471-8476 @5dbb453: https://github.com/agentclientprotocol/claude-agent-acp/blob/5dbb453c63a89746627799b2b06b31ba01a1b674/src/acp-agent.ts#L8308-L8325
2. Same file, L276-277 (`CLAUDE_CONFIG_DIR`).
3. `@anthropic-ai/claude-agent-sdk` 0.3.280 `sdk.d.ts` (`settingSources`, `skills`, `systemPrompt`): https://www.npmjs.com/package/@anthropic-ai/claude-agent-sdk/v/0.3.280
4. https://code.claude.com/docs/en/memory.md
5. https://code.claude.com/docs/en/skills.md
6. https://code.claude.com/docs/en/env-vars.md
7. https://code.claude.com/docs/en/authentication.md (credential management)
8. https://antigravity.google/docs/rules.md
9. https://antigravity.google/docs/skills.md (CLI global skills: `~/.gemini/antigravity-cli/skills/`)
10. `strings` of `agy_acp_server.par` 1.2.1 darwin-arm64 (modules `paths.py`, `server.py`, `settings.py`, `ccpa_connection/oauth_manager.py`, `oauth/credential_store.py`). Download from registry: https://github.com/agentclientprotocol/registry/blob/main/antigravity-acp/agent.json
11. `strings` of `localharness_external` in the same zip (customization roots, `skills.json`, rule discovery).
12. https://antigravity.google/docs/cli/install.md (CLI login in OS keyring)
13. https://docs.devin.ai/cli/extensibility/rules.md
14. https://docs.devin.ai/cli/extensibility/skills/overview.md
15. https://docs.devin.ai/cli/reference/configuration/read-config-from.md
16. https://docs.devin.ai/cli/reference/commands.md (`devin acp`)
17. https://docs.devin.ai/cli/extensibility/hooks/lifecycle-hooks.md
18. `strings` of local `devin` 3000.11.3 binary, and `ls` of `~/.config/devin` and `~/.local/share/devin`.
