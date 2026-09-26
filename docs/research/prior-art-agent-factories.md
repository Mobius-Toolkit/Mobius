# Prior art in agent factories

Research for issue #17. State of the products on 2026-09-26. Each claim links to a first-party source.

## Question

How do similar products divide the work between a human and coding agents? For each product: the trigger, the conversation surface, the progress reports, the review flow, the memory across tasks, and the agent hierarchy. What should Mobius copy, and what should it avoid?

## Short answer

Most products treat one mention or one issue as one task. The task runs in a cloud sandbox, reports in the thread or issue, and ends with a pull request. Two products go further and have a coordinator agent that starts and tracks Worker sessions:

- **Claude Code Projects**: one long conversation per body of work. Claude acts as coordinator, starts one thread per task, and shows the threads by state. This model is almost the same as the Mobius Workstream with its Lead.
- **Devin managed Devins**: a coordinator session starts child sessions, sends messages to them, and stops them.

Memory comes in two layers in every product:

- Repository files that humans write (`AGENTS.md`, `CLAUDE.md`, `.cursor/rules`, `REVIEW.md`).
- Notes that the agent writes when the human corrects it (Claude Tag channel memory, Claude Code project memory, CodeRabbit learnings, Bugbot learned rules, Copilot Memory).

AI reviewers post severity-tagged findings and do not block merges by default. OpenHands Agent Canvas already runs Claude Code and Codex through the Agent Client Protocol (ACP) on the user's subscription login. Thus, the Mobius harness approach has a working precedent.

## Cursor

Cursor renamed Background Agents to Cloud Agents. They run in isolated cloud VMs with the repository, dependencies, and secrets ([Cloud Agents](https://cursor.com/docs/cloud-agent), [help](https://cursor.com/help/ai-features/background-agents)).

- **Trigger**: Cursor Desktop, Cursor Web, iOS, Slack, Linear, `@cursor` in GitHub or Bitbucket pull request comments, and an API ([Cloud Agents](https://cursor.com/docs/cloud-agent)). Automations start agents on a cron schedule or on GitHub, Slack, Linear, and webhook events ([Automations](https://cursor.com/docs/cloud-agent/automations)).
- **Conversation surface**: In Slack, `@Cursor` with a prompt starts an agent. Inline options select the repository, branch, and model, for example `repo=acme/backend branch=dev model=opus`. `@Cursor` in the same thread adds a follow-up. The agent reads the whole thread for context ([Slack](https://cursor.com/docs/integrations/slack)). Admins can let teammates send follow-ups to an agent that another person started ([Cloud Agents](https://cursor.com/docs/cloud-agent)).
- **Progress reports**: For complex tasks, the agent posts a brief plan before it changes code. "Cursor updates a short status under the thread." At the end, Slack shows a notification with a link to the pull request. `@Cursor list my agents` lists the agents that run now ([Slack](https://cursor.com/docs/integrations/slack)). Agents attach screenshots, videos, and logs that show how they checked their work ([Cloud Agents](https://cursor.com/docs/cloud-agent)).
- **Review flow**: Bugbot reviews each pull request update, or reviews on a `bugbot run` comment. Bugbot Autofix starts a cloud agent that pushes fixes and comments with the result ([Bugbot](https://cursor.com/docs/bugbot)).
- **Memory**: `.cursor/rules`, `AGENTS.md` (nested files too), user rules, and team rules ([Rules](https://cursor.com/docs/context/rules)). `@cursor remember [fact]` on a pull request saves a Bugbot learned rule ([Bugbot](https://cursor.com/docs/bugbot)). An automation can read and write notes that persist across its runs ([Automations](https://cursor.com/docs/cloud-agent/automations)).
- **Hierarchy**: Inside one agent, subagents run in their own context windows and return a result to the parent. Custom subagents live in `.cursor/agents/` ([Subagents](https://cursor.com/docs/subagents)). No documented coordinator tracks many cloud agents.

## Anthropic

### Claude Code cloud sessions

- **Trigger**: claude.ai/code, the mobile app, the desktop app, `claude --cloud "<task>"` in the terminal, and routines. The session continues after the laptop closes ([cloud sessions](https://code.claude.com/docs/en/claude-code-on-the-web)).
- **Conversation surface**: A chat per session. `claude -p "<message>" --cloud <session-id>` queues a message from any machine. The user can take back a queued message before Claude reads it. `--teleport` pulls the session and its branch into a local terminal ([cloud sessions](https://code.claude.com/docs/en/claude-code-on-the-web)).
- **Review flow**: A diff view with inline comments that go to Claude with the next message. Auto-fix subscribes to GitHub events on the pull request. For a clear fix, Claude pushes and explains. For an ambiguous comment, Claude asks the user first. GitHub sends no webhook when the base branch causes a merge conflict, so auto-fix cannot react to conflicts. Replies go out under the user's GitHub account with a Claude Code label. The docs warn that these replies can trigger comment-driven automation ([auto-fix](https://code.claude.com/docs/en/claude-code-on-the-web#auto-fix-pull-requests)).
- **Hierarchy**: Subagents from `.claude/agents/` work in cloud sessions. Agent teams are experimental and off by default ([cloud sessions](https://code.claude.com/docs/en/claude-code-on-the-web)).

### Claude Code Projects

Projects are in public beta on Pro and Max plans ([Projects](https://code.claude.com/docs/en/claude-projects)). All facts in this section come from that page.

- **Model**: "A project is one ongoing conversation where Claude coordinates a stream of related work." The coordinator "sees what threads report back, not every step they take." Each thread is a separate session with its own context window. A thread works on its own branch and reports back when it finishes.
- **Trigger and route**: The user writes in the project conversation. Claude answers a quick question in place. New work goes to a new thread or to a current thread in that area, "and Claude tells you which". Several unrelated tasks in one message become separate threads. Claude can also propose threads in a "Suggested threads" list and wait for approval.
- **Progress reports**: The Overview pane groups threads by state: Ready for review, Waiting on you, Working, Landing, Idle, and Resolved. The desktop app sends a notification when Claude posts, when a thread fails, or when a thread needs input. The user can tell Claude "Only post when something finishes or is blocked."
- **Review flow**: A thread opens a pull request and then watches it with auto-fix on. It replies when checks pass and the pull request is ready. Buttons on the thread card send "Fix CI", "Address comments", or "Merge it" to the thread.
- **Memory**: Three layers. Project instructions are a brief of up to 16,000 characters that every new thread gets. Project memory is a set of files with a `MEMORY.md` index that every cloud thread reads at start. Repository `CLAUDE.md` files hold rules about one repository. When the user corrects a thread, the user tells Claude to remember the correction, and later threads start with it.
- **Limits**: A permission prompt waits inside its thread. "Telling Claude in the project conversation to go ahead doesn't reach it." Preferences such as "run at most two threads at a time" go to memory, but "a thread limit you give this way isn't a hard cap." The coordinator works from recent messages, recent threads, and memory, not from its full history.

### Claude in Slack and Claude Tag

The earlier Claude Code in Slack runs each session under the account of the user who asks. Anthropic retires it for Team and Enterprise plans in favor of Claude Tag ([Claude Code in Slack](https://code.claude.com/docs/en/slack)).

- **Trigger**: `@Claude` in a channel. Claude routes the message to a chat reply or to a coding session. A "Retry as Code" button fixes a wrong route ([Claude Code in Slack](https://code.claude.com/docs/en/slack)).
- **Progress reports**: Claude posts status updates in the thread. At the end, it mentions the user with a summary and the buttons "View Session" and "Create PR". The full transcript stays on claude.ai/code ([Claude Code in Slack](https://code.claude.com/docs/en/slack)).
- **Claude Tag session model**: Each thread binds to its own session and its own sandbox. The sandbox is released a few minutes after each turn. The next reply resumes the session in a fresh sandbox. Anyone in the channel can steer a session with a reply, without a new mention. For a longer task, the first reply is a checklist that Claude edits in place. Slack sends no notification for an edit, so the thread can look frozen. Code work usually ends as a draft pull request ([How Claude Tag works](https://claude.com/docs/claude-tag/concepts/how-it-works)).
- **Memory**: Memory belongs to the channel, not to a person. Claude saves a note when a user says "remember for this channel", and it also saves decisions on its own. Anyone in the channel can list, correct, or remove notes. The docs tell users to keep notes short and to prune stale notes with a weekly routine ([Claude Tag memory](https://claude.com/docs/claude-tag/users/memory)).
- **Proactive work**: Routines run on a schedule, watch channels, or subscribe to one pull request and post when CI finishes or a review lands ([routines](https://claude.com/docs/claude-tag/users/proactivity)).

### Claude Code GitHub Actions and Code Review

- **GitHub Actions trigger**: `@claude` in an issue or pull request comment, in a review, or in a new issue. Claude replies in one comment and updates it as it works. The action rejects bot actors unless `allowed_bots` lists them, "which keeps bots from triggering Claude in a loop." Repository standards go in `CLAUDE.md` ([GitHub Actions](https://code.claude.com/docs/en/github-actions)).
- **Code Review**: Several agents look for different classes of issue in parallel. A verification step then removes false positives. Findings are tagged Important, Nit, or Pre-existing and posted as inline comments. The check run always ends neutral, so it never blocks a merge. `REVIEW.md` tunes severity, nit volume, skip rules, and re-review behavior. Each review costs 15 to 25 USD on average. By default, automatic reviews skip draft pull requests ([Code Review](https://code.claude.com/docs/en/code-review)).

## OpenAI Codex cloud

- **Trigger**: The web app, the IDE, the CLI, `@codex` on GitHub, Slack, and Linear ([Codex cloud](https://learn.chatgpt.com/docs/cloud)).
- **Conversation surface**: In Slack, `@Codex` reads the thread, selects the best environment, and reacts with 👀 and a link to the cloud task. A follow-up needs a reply in the thread and a new mention. Long threads can need a summary from the user ([Codex in Slack](https://learn.chatgpt.com/docs/third-party/slack)).
- **Progress reports**: The user can watch the task logs or let the task run in the background. At the end, Codex shows a summary and a diff. The user can ask for changes or open a pull request ([Codex cloud](https://learn.chatgpt.com/docs/cloud)).
- **Review flow**: Automatic review of each pull request, or `@codex review`. Reviews flag only P0 and P1 issues. `@codex fix the P1 issue` starts a cloud task that pushes a fix to the branch ([Codex on GitHub](https://learn.chatgpt.com/docs/third-party/github)).
- **Memory**: `AGENTS.md` files, concatenated from the root down, with the nearest file last ([AGENTS.md guide](https://developers.openai.com/codex/guides/agents-md)). Review rules go in a `## Code Review Rules` section ([Codex on GitHub](https://learn.chatgpt.com/docs/third-party/github)). Local Codex clients keep memories under `~/.codex/memories/` ([Memories](https://learn.chatgpt.com/docs/customization/memories.md)).
- **Hierarchy**: Codex can start specialized subagents in parallel and collect their results in one response ([Subagents](https://developers.openai.com/codex/subagents.md)).

## Cognition Devin

- **Trigger**: `@Devin` in Slack, Linear, Jira, the web app, and the API ([Slack](https://docs.devin.ai/integrations/slack.md), [docs index](https://docs.devin.ai/llms.txt)).
- **Conversation surface**: The Slack thread is the session. Devin replies in the thread and asks questions there. Thread keywords control the session: `mute` makes Devin ignore new messages in the thread, and `sleep` and `archive` also exist ([Slack](https://docs.devin.ai/integrations/slack.md)).
- **Progress reports**: Status updates in the thread. In code channels, Devin shows chips for the pull requests it opens and its work log ([Slack](https://docs.devin.ai/integrations/slack.md)).
- **Review flow**: "Devin automatically responds to comments on PRs its sessions are working on, as long as the session has not been archived" ([GitHub](https://docs.devin.ai/integrations/gh.md)). Devin Review groups related edits, finds bugs by severity, and answers questions about the pull request. `/devin review` starts a review ([Devin Review](https://docs.devin.ai/work-with-devin/devin-review.md)).
- **Memory**: Knowledge items have a trigger description, and Devin fetches them only when relevant. Knowledge is deprecated and migrates automatically to Skills in Plugins ([Knowledge](https://docs.devin.ai/product-guides/knowledge.md)). Playbooks are reusable task instructions that a macro such as `!name` attaches to a session ([Playbooks](https://docs.devin.ai/product-guides/using-playbooks.md)).
- **Hierarchy**: Managed Devins. A coordinator session splits a large task, starts child sessions in separate VMs, sends messages to them, puts them to sleep or stops them, and compiles the results ([Advanced capabilities](https://docs.devin.ai/work-with-devin/advanced-capabilities.md)).

## OpenHands

- **Trigger**: On GitHub, `@openhands` in an issue comment, a pull request comment, or an inline review comment. The `openhands` label on an issue also starts the agent ([GitHub integration](https://docs.openhands.dev/enterprise/integrations/github.md)). In Slack, `@openhands` in a message or thread ([Slack](https://docs.openhands.dev/openhands/usage/cloud/slack-installation.md)).
- **Conversation surface**: In Slack, only the user who started the conversation can send follow-ups ([Slack](https://docs.openhands.dev/openhands/usage/cloud/slack-installation.md)).
- **Progress reports**: On GitHub, one "I'm on it!" comment at the start and one comment with the final answer at the end. Both link to the conversation ([GitHub integration](https://docs.openhands.dev/enterprise/integrations/github.md)).
- **Review flow**: The agent acts with the GitHub credentials of the user who triggered it ([GitHub integration](https://docs.openhands.dev/enterprise/integrations/github.md)). Agent Canvas ships a prebuilt pull request review automation ([automations](https://docs.openhands.dev/openhands/usage/agent-canvas/prebuilt-automations)).
- **Memory**: `AGENTS.md` loads at start. Skills in `.agents/skills/` load on demand, by keyword or by path ([Skills](https://docs.openhands.dev/overview/skills.md)). The SDK keeps a `MEMORY.md` index and daily logs per user and per project. The agent writes these files, but the prompt treats them as "unverified hints", because anyone with repository access can edit them ([Persistent memory](https://docs.openhands.dev/sdk/guides/persistent-memory.md)).
- **Hierarchy**: The SDK task tool starts a sub-agent and blocks until it returns. The sub-agent conversation is saved, so the parent can resume it ([TaskToolSet](https://docs.openhands.dev/sdk/guides/task-tool-set.md)).
- **Self-hosted ACP precedent**: Agent Canvas is "the self-hosted developer control center for coding agents and automations." It runs Claude Code, Codex, Gemini, or any ACP agent ([OpenHands repository](https://github.com/OpenHands/OpenHands)). The agent server starts the agent CLI as a subprocess. A subscription login on the same machine takes priority over an API key ([ACP agents](https://docs.openhands.dev/openhands/usage/agent-canvas/acp-agents)).

## GitHub Copilot cloud agent

GitHub now calls the Copilot coding agent the "Copilot cloud agent".

- **Trigger**: Assign an issue to Copilot, use the agents panel, mention `@copilot` in a pull request comment, or use Slack and Microsoft Teams ([About the cloud agent](https://docs.github.com/en/copilot/concepts/agents/coding-agent/about-coding-agent)).
- **Conversation surface**: In Slack, `@GitHub` in a thread starts work. Copilot can open a dedicated code channel where the team follows the plan, inspects diffs, and redirects or stops the session ([changelog 2026-08-21](https://github.blog/changelog/2026-08-21-the-new-github-copilot-experience-in-slack/)).
- **Progress reports**: The session log shows progress, token usage, and session length. A new instruction in the session takes effect "after it finishes its current tool call". "Stop session" keeps the commits that Copilot already pushed ([track sessions](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/cloud-agent/track-copilot-sessions)).
- **Review flow**: The user iterates with `@copilot` comments or pushes commits. Actions workflows do not run on Copilot pull requests until a human clicks "Approve and run workflows". The approval of the user who assigned the task does not count toward required approvals ([review Copilot PRs](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/cloud-agent/review-copilot-prs)).
- **Memory**: `.github/copilot-instructions.md`, path-specific `.instructions.md` files, `AGENTS.md` (nearest file wins), `CLAUDE.md`, or `GEMINI.md` ([repository instructions](https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions)). Copilot Memory stores repository facts with citations to code. Copilot checks each citation against the current branch before use. A fact that stays unused for 28 days is deleted ([Copilot Memory](https://docs.github.com/en/copilot/concepts/agents/copilot-memory)).
- **Hierarchy**: Custom agents specialize in different task types ([About the cloud agent](https://docs.github.com/en/copilot/concepts/agents/coding-agent/about-coding-agent)). No documented coordinator.

## CodeRabbit

- **Trigger**: A full review when a pull request opens, and an incremental review on each push ([review overview](https://docs.coderabbit.ai/guides/code-review-overview.md)). Draft pull requests are skipped by default (`reviews.auto_review.drafts: false`) ([configuration](https://docs.coderabbit.ai/reference/configuration)).
- **Conversation surface**: `@coderabbitai` comments on the pull request for questions and for the commands pause, resume, and resolve ([review overview](https://docs.coderabbit.ai/guides/code-review-overview.md)). A Slack agent investigates, writes coding plans from a thread, and opens pull requests ([Slack agent](https://docs.coderabbit.ai/overview/slack-agent.md)).
- **Progress reports**: A walkthrough comment at the top of the pull request summarizes the changes, with sequence diagrams and an effort estimate ([review overview](https://docs.coderabbit.ai/guides/code-review-overview.md)).
- **Review flow**: Inline comments with category badges and severity from Critical to Info. The user can apply a suggestion with one click ([review overview](https://docs.coderabbit.ai/guides/code-review-overview.md)). `@coderabbitai autofix` reads the "Prompt for AI Agents" blocks of unresolved threads and pushes a commit or opens a stacked pull request ([Autofix](https://docs.coderabbit.ai/finishing-touches/autofix.md)). Auto-approval is off by default (`reviews.request_changes_workflow: false`) ([configuration](https://docs.coderabbit.ai/reference/configuration)).
- **Memory**: When a user replies to a review comment with a team preference, CodeRabbit can save a learning and show a "Learnings Added" section. Learnings load before each comment, by repository or organization scope and by file pattern. Admins edit them at `app.coderabbit.ai/learnings` ([Learnings](https://docs.coderabbit.ai/knowledge-base/learnings.md)). CodeRabbit also reads `.cursorrules`, `CLAUDE.md`, `.github/copilot-instructions.md`, and other agent files as code guidelines ([knowledge base](https://docs.coderabbit.ai/integrations/knowledge-base)).
- **Hierarchy**: None documented.

## Comparison

| Product | Trigger | Conversation surface | Progress reports | Review flow | Memory across tasks | Hierarchy |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| Cursor Cloud Agents | App, Slack, Linear, PR comment, API, automations | Slack thread, web | Plan first, one status line, PR link at end | Bugbot on each push, Autofix agent | Rules, `AGENTS.md`, Bugbot learned rules, automation notes | Subagents in one agent |
| Claude Code cloud sessions | Web, mobile, CLI `--cloud`, routines | Session chat, CLI message queue | Live transcript | Diff comments, auto-fix on PR events | `CLAUDE.md` | Subagents, experimental teams |
| Claude Code Projects | Project conversation | One coordinator chat | Overview by state, notifications | Thread watches its PR with auto-fix | Instructions, `MEMORY.md` files, `CLAUDE.md` | Coordinator and threads |
| Claude Tag | `@Claude` in channel, routines | Slack thread, anyone can steer | Checklist edited in place | Draft PR, PR subscription | Channel and workspace notes | Channel session and thread sessions |
| Claude GitHub Actions and Code Review | `@claude` comment, PR open or push | Issue or PR comment | One comment updated in place | Parallel agents, verification, neutral check | `CLAUDE.md`, `REVIEW.md` | Parallel review agents |
| Codex cloud | Web, IDE, CLI, `@codex`, Slack, Linear | Web task, Slack thread | 👀 reaction, link, result post | P0 and P1 review, `@codex fix` | `AGENTS.md`, local memories | Subagents |
| Devin | `@Devin` in Slack, Linear, Jira, API | Slack thread is the session | Thread updates, PR chips | Answers PR comments, Devin Review | Knowledge (now Skills), playbooks | Managed Devins |
| OpenHands | `@openhands`, `openhands` label, Slack | Web, Slack thread | Start and end comments with link | Acts as the user, review automation | `AGENTS.md`, skills, `MEMORY.md` | Sub-agents that block the parent |
| Copilot cloud agent | Assign issue, `@copilot`, Slack, Teams | Session log, Slack code channel | Session log, mid-session instructions | `@copilot` comments, human runs CI | Instruction files, cited Copilot Memory | Custom agents |
| CodeRabbit | PR open and push | PR comments, Slack | Walkthrough comment | Severity comments, autofix | Learnings, agent rule files | None |

## What Mobius should copy

1. **Copy the Claude Code Projects shape for the Workstream.** One Lead conversation per Workstream matches the Projects coordinator. Workers report results to the Lead, not every step. The Lead tells the Owner which Worker got each message, and it answers quick questions without a Worker.
2. **Show Workers by state, not by time.** The Projects Overview groups are Ready for review, Waiting on you, Working, Landing, Idle, and Resolved. The Owner opens the chat and sees at once what needs the Owner.
3. **Keep memory in two layers.** Repository rules stay in `AGENTS.md` or `CLAUDE.md`, which every harness already reads. Workstream memory is a small set of Markdown notes with an index, and each Worker reads it at start. The Owner saves a note with an explicit "remember" and can list, edit, and prune notes.
4. **Guard memory against staleness.** Copilot Memory cites code for each fact and checks the citation before use. OpenHands treats memory as unverified hints. A Mobius note should name its source, and the Lead should check it before it acts on the note.
5. **Report progress as one message that changes in place.** Claude Tag edits one checklist, Cursor keeps one short status line, and Claude GitHub Actions updates one comment. Mobius should send a new message only when a Worker finishes, fails, or needs the Owner.
6. **Post an acknowledgement with a link when a Worker starts.** OpenHands posts "I'm on it!" with a link on the issue. Mobius should post the same kind of comment when it takes a `mobius:ready` issue. The label trigger itself has precedent in the OpenHands `openhands` label and in the Copilot issue assignment.
7. **Let the Implementer watch its own pull request.** Claude auto-fix pushes clear fixes for CI failures and review comments, and it asks the human when a comment is ambiguous. GitHub sends no webhook for base-branch conflicts, so Mobius must check for conflicts on a timer.
8. **Build the Reviewer like Claude Code Review.** Use a verification step before the Reviewer posts a finding. Tag each finding with a severity, and keep the focus on correctness (Codex posts only P0 and P1). Give the Reviewer a review-only instruction file such as `REVIEW.md`.
9. **Queue the Owner instructions to a Worker.** Copilot applies a new instruction after the current tool call. Claude Code lets the user take back a queued message. The Lead chat needs the same queue for each Worker.
10. **Reuse the OpenHands ACP pattern for harnesses.** Agent Canvas proves that a self-hosted server can start Claude Code and Codex as ACP subprocesses on the Owner's subscription login.

## What Mobius should avoid

1. **Do not make each message a separate one-off task.** Cursor, Codex, OpenHands, and the earlier Claude in Slack start a new task per mention. The follow-up stays in one thread, and no agent owns the larger goal. The Lead must own the Workstream task list across many Workers.
2. **Do not trap Worker approvals in the Worker.** In Projects, a permission prompt waits inside its thread, and a "go ahead" in the coordinator chat does not reach it. Mobius should bring each Worker question and approval up to the Lead chat.
3. **Do not store limits as agent memory.** In Projects, a thread limit that the user states is a preference, not a hard cap. Mobius should enforce the Worker count and the cost limits in code.
4. **Do not let agent comments trigger agents.** Claude GitHub Actions rejects bot actors by default to stop loops. Claude auto-fix warns that its replies under the user's account can start comment-driven workflows. Mobius agents use the Owner's GitHub access, so Mobius must mark each agent comment and ignore its own events.
5. **Do not depend on chat edits or long chat context.** Slack sends no notification for an edited message, so a Claude Tag checklist can look frozen. Claude Tag reads only a window of a long thread, and Codex asks for a summary of long threads. The Lead should work from the Workstream memory and the task list, not from the full chat history. Claude Projects uses the same approach.
6. **Do not review on every push without a limit.** Claude Code Review costs 15 to 25 USD per review on average, and a review on each push multiplies the cost. The Mobius Reviewer should review once when the Implementer marks the draft done, and review again only on request.
7. **Do not keep results only in the sandbox.** Claude Tag releases the sandbox a few minutes after each turn, and files that exist only there are lost. Workers should push branches early.
8. **Do not build a proprietary knowledge store.** Devin deprecated Knowledge and moved it to Skills. Plain Markdown files in the repository or the Workstream stay portable across harnesses.
9. **Do not make the reviewer a merge gate by default.** Claude Code Review ends neutral, CodeRabbit auto-approval is off by default, and the approval of the Copilot task owner does not count. The Owner stays the only person who merges.

## Note on draft pull requests

Claude Code Review and CodeRabbit skip draft pull requests by default. The Mobius Reviewer reviews drafts on purpose, before the Owner sees them. If the Owner also installs one of these products, both reviewers can comment on the same pull request after the draft becomes ready.
