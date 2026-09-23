# 01 — Vision

Mobius is a self-hosted **agent swarm** for solopreneurs and tiny teams.

Software work inside a small codebase splits naturally into scopes — "landing
page", "payments", "cli". Each scope accumulates its own knowledge: conventions,
gotchas, past decisions. A single general-purpose agent loses that context
between sessions; a human reviewer becomes the bottleneck for delegation.

Mobius models scopes as **Projects** inside repositories, each owned by a
long-lived **Project Agent** that:

- accumulates scoped knowledge in **Memory** (Organization → Repository →
  Project, stored in SQLite, dumped to markdown),
- receives inbound **Signals** (GitHub issues/comments/PRs today; Sentry,
  chatbots later) and turns them into **Tasks**,
- executes tasks as **Runs** — one ACP session in a dedicated git worktree —
  and writes memory back.

Separate role agents handle **code review** and **housekeeping**. GitHub stays
the human collaboration surface (issues, PR reviews); humans can also talk to
any agent directly through the built-in chat.

Mobius deliberately does **not** implement its own LLM agent. It orchestrates
existing harnesses — Devin CLI, OpenCode, and Antigravity (`agy`)/Claude/Codex
via ACP adapters — through the Agent Client Protocol, so harness upgrades, auth, and
tool ecosystems stay upstream concerns. Mobius is a single self-hosted binary:
SQLite for state, `gh`/`git`/harness CLIs for side effects, an axum REST/SSE
API, and a Dioxus SPA.
