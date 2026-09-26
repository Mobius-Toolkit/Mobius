# Subscription use through ACP

Ticket: [#14](https://github.com/Mobius-Toolkit/Mobius/issues/14). Research date: 2026-09-26.

## Question

Can each Harness run on the Owner's own subscription when a self-hosted program drives it through ACP or through its SDK? Which terms apply, and which limits apply?

This document is not legal advice. It records what the public terms and documents say on the research date.

## Short answer

Yes, for all four Harnesses, with conditions. The safe pattern is the same for each Harness:

1. The Owner installs the official, unmodified Harness binary on the Mobius server.
2. The Owner logs in through the Harness's own login flow.
3. Mobius starts the Harness process and speaks ACP to it. Mobius never reads, stores, or forwards the subscription token.
4. Only the Owner uses the result. Nobody else gets access through the Owner's subscription.

| Harness | ACP entry point | Subscription login in headless use | Main term risk | Risk |
| :-- | :-- | :-- | :-- | :-- |
| Claude Code | `claude-agent-acp` adapter (Claude Agent SDK, which runs the Claude Code binary) | Documented: `claude setup-token` for "CI pipelines and scripts" | Agent SDK products must use API keys; "ordinary, individual usage"; enforcement "without prior notice" | Medium |
| Codex | `codex-acp` adapter (Codex app-server) | Documented: ChatGPT login in app-server; `codex exec` and SDK are plan features | "automatically or programmatically extract data or Output" clause is broad | Low |
| Devin | `devin acp` (first-party) | Documented: `devin auth login`, stored credentials | "build a competitive product" clause | Low to medium |
| Gemini CLI | `gemini --acp` (first-party) | Documented: headless mode uses a cached login | Direct backend access with Gemini CLI OAuth is forbidden | Low (only through the unmodified CLI) |

## Claude Code (Claude Pro or Max)

### How Mobius connects

The ACP adapter `claude-agent-acp` "implements an ACP agent by using the official Claude Agent SDK" ([README](https://github.com/agentclientprotocol/claude-agent-acp)). The Agent SDK is "a library that runs the Claude Code binary" ([Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview)). The adapter shows the claude.ai login method by default. A flag `--hide-claude-auth` hides it for integrations that "must never bill a claude.ai subscription" ([source](https://github.com/agentclientprotocol/claude-agent-acp/blob/main/src/hide-claude-auth.ts)).

### Headless use on the subscription

Anthropic documents a subscription token for scripts. `claude setup-token` makes "a one-year OAuth token" for "CI pipelines, scripts, or other environments where interactive browser login isn't available". "This token authenticates with your Claude subscription and requires a Pro, Max, Team, or Enterprise plan" ([Authentication](https://code.claude.com/docs/en/authentication)).

The Claude Help Center (update of 2026-06-15) says: "Claude Agent SDK, `claude -p`, and third-party app usage still draw from your subscription's usage limits" ([Agent SDK with your Claude plan](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan)). Anthropic paused a plan to move this use to a separate monthly credit. Anthropic says: "When we have an update, we'll share it before anything takes effect."

### What the terms allow

The Claude Code legal page permits a user to sign in to the official binary. The restrictions do not "prevent an end user from signing in to the unmodified Claude Code binary with their own Claude subscription" ([Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance), undated, read 2026-09-26).

The Consumer Terms (effective 2025-10-08) forbid automated access, with an exception: "Except when you are accessing our Services via an Anthropic API Key or where we otherwise explicitly permit it, to access the Services through automated or non-human means, whether through a bot, script, or otherwise" ([Consumer Terms](https://www.anthropic.com/legal/consumer-terms)). The `setup-token` and `claude -p` documents are an explicit permission for scripted use of Claude Code.

### What the terms forbid

- Third-party login: "Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, or Max plan credentials on behalf of their users" ([Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)).
- Token custody: "developers may not collect, store, or intermediate Claude.ai credentials or session tokens — sign-in to a Claude account must complete through Anthropic's own flow" (same page).
- Agent SDK products: "Developers building products or services that interact with Claude's capabilities, including those using the Agent SDK, should use API key authentication" (same page). The SDK overview says: "Unless previously approved, Anthropic does not allow third party developers to offer claude.ai login or rate limits for their products, including agents built on the Claude Agent SDK" ([Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview)).
- Product offers: a product that runs Claude Code must not modify the binary, and "Customers may not pay for, resell, or intermediate Claude usage on their end users' behalf" ([Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)).
- Account share: "You may not share your Account login information ... or make your Account available to anyone else" ([Consumer Terms](https://www.anthropic.com/legal/consumer-terms)).
- Product name: a product must not use "Claude Code" or "Claude Code Agent" as a name ([Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview)).
- Enforcement: "Anthropic reserves the right to take measures to enforce these restrictions and may do so without prior notice" ([Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)).

### Limits

- "Advertised usage limits for Pro and Max plans assume ordinary, individual usage of Claude Code and the Agent SDK" ([Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)).
- Claude and Claude Code share one allowance: "all activity in both tools counts against the same usage limits" ([Claude Code with Pro or Max](https://support.claude.com/en/articles/11145838-using-claude-code-with-your-pro-or-max-plan)). Anthropic does not publish fixed numbers. `/status` shows the allowance that remains.
- The `setup-token` token "can only make model requests". Bare mode (`--bare`) does not read `CLAUDE_CODE_OAUTH_TOKEN` ([Authentication](https://code.claude.com/docs/en/authentication)).
- Anthropic publishes no limit on concurrent sessions.

### Ambiguous points

- **Is Mobius a "third-party developer" product?** Mobius is a program from a third party, and it uses the Agent SDK through the adapter. But Mobius does not offer its own login, does not hold tokens, and serves only the subscriber. The documents do not state this case directly. The closest permission is the "unmodified Claude Code binary" sentence.
- **"Ordinary, individual usage."** Several agents in parallel, all day, can exceed this. Anthropic does not define the term.
- **Commercial work.** Section 11 of the Consumer Terms has the title "Non-commercial use only" and says: "You agree that you will not use our Services for any commercial or business purposes". This clause sits in the liability section, and Anthropic markets Pro for use "at work and at home". An Owner who does employer work must check this. Team and Enterprise plans use the Commercial Terms.
- **Policy changes.** Anthropic changed Agent SDK and third-party rules several times in 2026. The June 2026 note promises an update before any new change.

## Codex (ChatGPT plans)

### How Mobius connects

The ACP adapter `codex-acp` is "built on the new Codex App Server" ([zed-industries/codex-acp](https://github.com/zed-industries/codex-acp)). It advertises "ChatGPT login" and API key methods ([agentclientprotocol/codex-acp](https://github.com/agentclientprotocol/codex-acp)). OpenAI documents app-server as the interface "when you want a deep integration inside your own product: authentication, conversation history, approvals, and streamed agent events" ([App Server](https://learn.chatgpt.com/docs/app-server)).

### Headless use on the subscription

- App-server has a ChatGPT mode: "Codex owns the ChatGPT OAuth flow, persists tokens, and refreshes them automatically". A device-code flow exists for machines without a browser ([App Server](https://learn.chatgpt.com/docs/app-server)).
- The plan table lists "Codex SDK, `codex exec`, and scriptable workflows" as a feature of ChatGPT plans ([Pricing](https://learn.chatgpt.com/docs/pricing)).
- For headless machines, OpenAI says: "prefer device code authentication (beta)" ([Authentication](https://learn.chatgpt.com/docs/auth)).
- OpenAI recommends API keys for CI but does not forbid ChatGPT login there: "Use API key authentication for programmatic Codex CLI workflows, such as CI/CD jobs" and "API keys are still the recommended default for automation" (same page).

### What the terms allow

OpenAI supports third-party harnesses in public. The Codex for Open Source page says: "Developers should code in the tools they prefer, whether that's Codex, OpenCode, Cline, pi, OpenClaw, or something else" ([Codex for OSS](https://developers.openai.com/community/codex-for-oss)). App-server also has an experimental mode for "host apps that already own the user's ChatGPT auth lifecycle" ([App Server](https://learn.chatgpt.com/docs/app-server)).

### What the terms forbid

The OpenAI Terms of Use (effective 2026-01-01) say ([Terms of Use](https://openai.com/policies/row-terms-of-use/)):

- "You may not share your account credentials or make your account available to anyone else".
- Users may not "Automatically or programmatically extract data or Output".
- Users may not "circumvent any rate limits or restrictions or bypass any protective measures".

The auth document says: "Treat `~/.codex/auth.json` like a password" and "Don't expose Codex execution in untrusted or public environments" ([Authentication](https://learn.chatgpt.com/docs/auth)).

### Limits

- Limits apply "per five-hour period". "Weekly limits may also apply" ([Pricing](https://learn.chatgpt.com/docs/pricing)).
- Example for GPT-6 Luna, local messages in five hours: Plus 350-3,000, Pro 5x 1,750-14,000, Pro 20x 7,000-56,000. "These estimates are not fixed message limits" (same page).
- "Local messages and cloud chats share your plan's usage allowance" (same page).
- App-server has `account/rateLimits/read` and a `account/rateLimits/updated` notification ([App Server](https://learn.chatgpt.com/docs/app-server)). Mobius can read the allowance that remains.
- OpenAI publishes no limit on concurrent sessions.

### Ambiguous points

- The "programmatically extract ... Output" clause is broad. The first-party `codex exec`, SDK, and app-server documents show that OpenAI permits programmatic use of Codex itself.
- The ChatGPT subscription for third-party tools has no formal clause in the Terms of Use. The support comes from product documents and the Codex for OSS page.

## Devin (Cognition)

### How Mobius connects

Devin CLI is a first-party ACP agent. "Run Devin as an Agent Client Protocol (ACP) server over stdio. This subcommand is intended to be invoked by an ACP-aware editor or IDE ... as a subprocess" ([Commands](https://docs.devin.ai/cli/reference/commands)).

### Headless use on the subscription

- "The ACP server reads credentials from `WINDSURF_API_KEY` if set, otherwise from the credentials stored by `devin auth login`. It can also accept credentials at runtime via the ACP `authenticate` request" ([Commands](https://docs.devin.ai/cli/reference/commands)).
- `devin auth login --force-manual-token-flow` supports "remote/SSH sessions" (same page).
- `devin -p` is a documented "non-interactive mode" (same page).
- Pro includes "A daily and weekly usage quota that covers Devin sessions, Devin CLI, and Devin Desktop" ([Self-serve plans](https://docs.devin.ai/admin/billing/self-serve)).

### What the terms allow

Cognition documents ACP use by external clients such as Zed, JetBrains, and Xcode ([Zed](https://docs.devin.ai/cli/acp/zed)). The Cognition terms do not mention automated access.

### What the terms forbid

- "The Pro and Max plans are individual plans. They cannot be shared across multiple users" ([Self-serve plans](https://docs.devin.ai/admin/billing/self-serve)).
- The Acceptable Use Policy (updated 2026-06-30) says ([AUP](https://cognition.com/legal/acceptable-use-policy)):
  - "You may not provide any other user access to your account or account credentials".
  - "Users must not reproduce, duplicate, copy, sell, or resell any portion of our Services."
  - "Users may not use the Services as a part of any effort to compete with us or build a competitive product."
  - "Users must adhere to any usage limits specified in their subscription or licensing agreement."
- The Platform Terms (updated 2026-06-30) forbid to "make the Services or Documentation available to anyone other than Authorized Users" ([Platform Terms](https://cognition.com/legal/platform-terms-of-service)).

### Limits

- Pro: daily and weekly quota. Max: "a significantly larger weekly usage quota (with no daily cap)" ([Self-serve plans](https://docs.devin.ai/admin/billing/self-serve)).
- Usage past the quota draws from prepaid on-demand credits (same page).
- For cloud sessions: "there are no concurrent session limits" ([Usage](https://docs.devin.ai/admin/billing/usage)).

### Ambiguous points

- **Competitive product.** Devin Desktop runs other ACP agents in one command center. Mobius does a similar job. The AUP does not define "competitive product". The clause targets the use of Devin to build a competitor. Mobius uses Devin as a Harness, not as a tool to build Mobius. The risk is low, but not zero.
- The Devin CLI quickstart does not state the plan that `devin acp` needs for self-serve accounts. The Devin Auth page says Devin auth for the CLI "is available to Devin Enterprise customers" ([Devin Auth](https://docs.devin.ai/cli/enterprise/devin-auth)). Zed documents browser login to "your Devin Cloud account" ([Zed](https://docs.devin.ai/cli/acp/zed)). Mobius must test self-serve login.

## Gemini CLI (Google account)

### How Mobius connects

Gemini CLI is a first-party ACP agent: "To start Gemini CLI in ACP mode, use the `--acp` flag" ([ACP mode](https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/acp-mode.md)).

### Headless use on the subscription

"Headless mode will use your existing authentication method, if an existing authentication credential is cached" ([Authentication](https://github.com/google-gemini/gemini-cli/blob/main/docs/get-started/authentication.mdx)). The same page recommends an API key or Vertex AI for headless mode. Thus a login that the Owner does once in the terminal works for later headless runs.

### What the terms allow

Google login gives access to Gemini Code Assist for individuals, Google AI Pro, or Google AI Ultra. The Google Terms of Service and the Google One Additional Terms apply ([Terms and privacy](https://github.com/google-gemini/gemini-cli/blob/main/docs/resources/tos-privacy.md)).

### What the terms forbid

- "Directly accessing the services powering Gemini CLI (for example, the Gemini Code Assist service) using third-party software, tools, or services (for example, using OpenClaw with Gemini CLI OAuth) is a violation of applicable terms and policies. Such actions may be grounds for suspension or termination of your account" ([Terms and privacy](https://github.com/google-gemini/gemini-cli/blob/main/docs/resources/tos-privacy.md), last change 2026-04-10).
- The Google Terms of Service (effective 2026-07-30) forbid "using automated means to access content from any of our services in violation of the machine-readable instructions on our web pages" ([Google Terms](https://policies.google.com/terms)).

### Limits

Requests per user per day with a Google login ([Quotas and pricing](https://github.com/google-gemini/gemini-cli/blob/main/docs/resources/quota-and-pricing.md), last change 2026-06-18):

| Tier | Requests per day |
| :-- | :-- |
| Gemini Code Assist for individuals (free) | 1,000 |
| Google AI Pro | 1,500 |
| Google AI Ultra | 2,000 |

- "Requests are limited per user per minute and are subject to the availability of the service in times of high demand" (same page).
- Google AI Plus is "not supported" (same page).
- "one prompt might result in multiple model requests". Gemini CLI and Code Assist agent mode share one quota ([Gemini Code Assist quotas](https://docs.cloud.google.com/gemini/docs/quotas), updated 2026-09-24).

### Ambiguous points

- The ban covers direct backend access with Gemini CLI OAuth. It does not cover a program that runs the unmodified `gemini --acp`. Zed and JetBrains use this path ([ACP mode](https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/acp-mode.md)).

## Implications for Mobius

### Risk per Harness

| Harness | Risk | Reason |
| :-- | :-- | :-- |
| Claude Code | Medium | Scripted subscription use is documented, but Agent SDK product rules point to API keys. Parallel agents can exceed "ordinary, individual usage". Anthropic changed the rules often in 2026 and enforces without notice. |
| Codex | Low | OpenAI documents ChatGPT login in app-server and supports third-party harnesses in public. |
| Devin | Low to medium | `devin acp` is first-party. The "competitive product" clause is vague. Self-serve login through the CLI needs a test. |
| Gemini CLI | Low | Safe through the unmodified CLI. High if Mobius touches the Gemini CLI OAuth token. |

### What Mobius must do

1. Run only the official, unmodified Harness binary or the official ACP adapter.
2. Leave login to the Harness. The Owner logs in on the Mobius server through the Harness's own flow (`claude /login` or `claude setup-token`, `codex login --device-auth`, `devin auth login`, `gemini`).
3. Let the Harness keep its own credential store. Mobius does not read `~/.claude/.credentials.json`, `~/.codex/auth.json`, or the Gemini and Devin credential files.
4. Offer an API key option for each Harness in the Role binding. This gives the Owner a path if a vendor closes subscription use.
5. Limit the number of parallel agents for each Harness. Show the Harness quota errors to the Owner.
6. Read the allowance that remains where the Harness exposes it, for example Codex `account/rateLimits/read`.
7. Tell the Owner in the setup guide that the Owner is responsible for the terms of each subscription.

### What Mobius must avoid

1. Do not add a "Sign in with Claude", "Sign in with ChatGPT", or "Sign in with Google" screen of Mobius.
2. Do not collect, store, copy, or forward subscription tokens. Do not send model requests to vendor backends directly.
3. Do not let any person other than the Owner use the Harnesses. Do not add a hosted or multi-user mode on subscriptions.
4. Do not rotate accounts or use other tricks to pass rate limits.
5. Do not use "Claude Code" or other vendor names in the Mobius product name or logo.
6. Do not modify or patch a Harness binary.

### Open items

- Watch the Claude Help Center note of 2026-06-15. Anthropic promised an update on Agent SDK use of subscriptions.
- Test `devin acp` login on a self-serve Pro plan.
- Owners who do employer work on a Claude Pro or Max plan must read the "Non-commercial use only" clause.

## Sources

- Anthropic, Claude Code legal and compliance: https://code.claude.com/docs/en/legal-and-compliance (read 2026-09-26)
- Anthropic, Claude Code authentication: https://code.claude.com/docs/en/authentication (read 2026-09-26)
- Anthropic, Agent SDK overview: https://code.claude.com/docs/en/agent-sdk/overview (read 2026-09-26)
- Anthropic, Consumer Terms of Service, effective 2025-10-08: https://www.anthropic.com/legal/consumer-terms
- Anthropic, Usage Policy, effective 2025-09-15: https://www.anthropic.com/legal/aup
- Claude Help Center, Agent SDK with your Claude plan, update 2026-06-15: https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan
- Claude Help Center, Claude Code with Pro or Max: https://support.claude.com/en/articles/11145838-using-claude-code-with-your-pro-or-max-plan
- ACP adapter for the Claude Agent SDK: https://github.com/agentclientprotocol/claude-agent-acp
- OpenAI, Terms of Use, effective 2026-01-01: https://openai.com/policies/row-terms-of-use/
- OpenAI, Codex authentication: https://learn.chatgpt.com/docs/auth (read 2026-09-26)
- OpenAI, Codex prices: https://learn.chatgpt.com/docs/pricing (read 2026-09-26)
- OpenAI, Codex App Server: https://learn.chatgpt.com/docs/app-server (read 2026-09-26)
- OpenAI, Codex for Open Source: https://developers.openai.com/community/codex-for-oss
- ACP adapter for Codex: https://github.com/agentclientprotocol/codex-acp
- Cognition, Devin CLI commands: https://docs.devin.ai/cli/reference/commands (read 2026-09-26)
- Cognition, Devin in Zed: https://docs.devin.ai/cli/acp/zed
- Cognition, Devin Auth: https://docs.devin.ai/cli/enterprise/devin-auth
- Cognition, Self-serve plans: https://docs.devin.ai/admin/billing/self-serve
- Cognition, Usage: https://docs.devin.ai/admin/billing/usage
- Cognition, Platform Terms of Service, updated 2026-06-30: https://cognition.com/legal/platform-terms-of-service
- Cognition, Acceptable Use Policy, updated 2026-06-30: https://cognition.com/legal/acceptable-use-policy
- Google, Gemini CLI terms and privacy: https://github.com/google-gemini/gemini-cli/blob/main/docs/resources/tos-privacy.md
- Google, Gemini CLI quotas and pricing: https://github.com/google-gemini/gemini-cli/blob/main/docs/resources/quota-and-pricing.md
- Google, Gemini CLI authentication: https://github.com/google-gemini/gemini-cli/blob/main/docs/get-started/authentication.mdx
- Google, Gemini CLI ACP mode: https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/acp-mode.md
- Google, Gemini Code Assist quotas, updated 2026-09-24: https://docs.cloud.google.com/gemini/docs/quotas
- Google, Terms of Service, effective 2026-07-30: https://policies.google.com/terms
