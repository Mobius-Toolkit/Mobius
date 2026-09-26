# GitHub integration for a self-hosted install

Research for issue #16. Sources checked on 2026-09-26. Each claim has a link to its primary source. "Live check" means a read-only `gh api` call against `Mobius-Toolkit/Mobius` on that date.

## Question

What is the best way for a Mobius server on a laptop or a home server to integrate with GitHub?

1. GitHub App or personal access token (PAT)?
2. Webhooks or polling?
3. Do the APIs that Mobius needs exist, and what are their limits?

## Short answer

- Use a GitHub App that the Owner registers with the manifest flow. The App gives a separate bot identity, short-lived tokens, and permissions per repository.
- Poll the REST API with conditional requests. Do not require a public URL. A `304` response costs no rate limit.
- All needed APIs exist. Sub-issues: 100 per parent, 8 levels. Dependencies: 50 per relationship type. "Ready for review" is GraphQL only.
- PRs that the App opens trigger GitHub Actions workflows. Workflows skip drafts with `if: github.event.pull_request.draft == false` and the `ready_for_review` type.

## 1. GitHub App or PAT

### Identity

| | GitHub App (installation token) | Fine-grained PAT | Classic PAT |
|---|---|---|---|
| Author of comments, reviews, PRs | `<app-slug>[bot]`, `user.type = "Bot"` | The Owner | The Owner |
| Owner can approve agent PRs | Yes | No: author and approver are the same person | No |
| Token life | 1 hour, Mobius refreshes it | Up to 366 days, or no expiry | Owner sets it |
| Scope | Selected repositories, per-permission | One user or organization, per-permission | Broad scopes (`repo`) |
| Notifications API | No | No | Yes |

- An installation access token attributes activity to the app. A user access token attributes activity to the user. Source: [About authentication with a GitHub App](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/about-authentication-with-a-github-app).
- Live check: `search/issues?q=author:app/dependabot+is:pr` returns PRs with `user.login = "dependabot[bot]"` and `user.type = "Bot"`. Mobius can use `user.type` or the bot login to ignore its own comments.
- With a PAT, every agent comment has the Owner as author. Mobius then needs a text marker to separate its own comments from the Owner's comments. A text marker is fragile.
- "Pull request authors cannot approve their own pull requests." Source: [Approving a pull request with required reviews](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/reviewing-changes-in-pull-requests/approving-a-pull-request-with-required-reviews). With a PAT, the Owner is the author of each agent PR and cannot approve it.
- The same rule applies to the App: if the Implementer and the Reviewer both act as one App, the Reviewer cannot approve the PR. The Reviewer can post a `COMMENT` review. For `APPROVE`, register a second App for the Reviewer.
- A machine user account is a third option. The GitHub terms allow "no more than one free machine account in addition to your free Personal Account". Source: [GitHub Terms of Service](https://docs.github.com/en/site-policy/github-terms/github-terms-of-service). A machine account needs its own email, login, and seat in an organization. The App is simpler.

### Tokens

- The App signs a JWT with its private key. The JWT expiry "must be no more than 10 minutes into the future". Source: [Generating a JWT for a GitHub App](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app).
- The App exchanges the JWT for an installation access token. "The installation access token will expire after 1 hour." Source: [Authenticating as a GitHub App installation](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation).
- A fine-grained PAT can have an expiry of 1 to 366 days, or none. Source: [Managing your personal access tokens](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens).
- A fine-grained PAT is "limited to access resources owned by a single user or organization". Same source.
- GitHub recommends an App for "a long-lived integration" and a PAT "for API testing or short-lived scripts". Source: [Deciding when to build a GitHub App](https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps/deciding-when-to-build-a-github-app).

### Permissions for the App

The permission tables in [Permissions required for GitHub Apps](https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps) give these repository permissions:

| Permission | Access | Needed for |
|---|---|---|
| Issues | write | Labels, issue comments, sub-issues, dependencies |
| Pull requests | write | Create PRs, reviews, review comments |
| Contents | write | Push branches with `git` |
| Metadata | read | Basic repository reads |

- The sub-issue and dependency endpoints (`POST .../sub_issues`, `POST .../dependencies/blocked_by`) need Issues write. Both token types (user and installation) work.
- The response header `X-Accepted-GitHub-Permissions` names the permission that an endpoint needs. Same source.
- Contents gives HTTP Git access with the installation token. Workflows is necessary only to edit files in `.github/workflows`. Source: [Choosing permissions for a GitHub App](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app).
- Do not add Workflows permission. Without it, an agent cannot change the CI of the repository.

### Rate limits

Source: [Rate limits for the REST API](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api).

| Token | Primary limit (REST) |
|---|---|
| PAT (user) | 5,000 requests per hour |
| App installation | 5,000 per hour, plus 50 per repository above 20, maximum 12,500 |
| App installation on Enterprise Cloud | 15,000 per hour |
| `GITHUB_TOKEN` in Actions | 1,000 per hour per repository |

- Secondary limits: 100 concurrent requests, 900 points per minute, 80 content-creating requests per minute, 500 per hour. Same source.
- The 500 content-creating requests per hour apply to comments, reviews, labels, and PRs. This is the real limit for a busy Mobius server.
- GraphQL: 5,000 points per hour per user or per installation. Source: [GraphQL rate limits](https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api).
- A PAT shares its 5,000 per hour with the Owner's other tools, for example `gh`. An App has its own budget.

### Setup friction and the manifest flow

The manifest flow registers a preconfigured App in one browser step. Source: [Registering a GitHub App from a manifest](https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest).

```
Owner browser            GitHub                     Mobius server (localhost)
     |  POST form: manifest  |                              |
     |---------------------->|                              |
     |  "Create GitHub App"  |                              |
     |  redirect_url?code=.. |                              |
     |------------------------------------------------------>|
     |                       | POST /app-manifests/{code}/conversions
     |                       |<-----------------------------|
     |                       | id, pem, webhook_secret ---->|  store in config
     |  Install App on repos |                              |
     |---------------------->|                              |
```

- The manifest holds `name`, `url`, `default_permissions`, `default_events`, `redirect_url`, and `hook_attributes`. Same source.
- "You must complete all three steps in the GitHub App Manifest flow within one hour." Same source.
- The response of the conversion step includes "`id` (GitHub App ID), `pem` (private key), and `webhook_secret`". Same source.
- The browser does the redirect, so `redirect_url` can point to the Mobius server on `localhost`. GitHub does not call that URL itself. The Probot tutorial runs the flow on a local server. Same source.
- The Owner must still install the App on the repositories after the registration. Source: [Installing your own GitHub App](https://docs.github.com/en/apps/using-github-apps/installing-your-own-github-app).
- A fine-grained PAT needs one form and one copy step. It is simpler, but it has the identity problems above.

## 2. Webhooks or polling

### Webhooks

- A webhook needs a URL that GitHub can reach. A home server behind NAT does not have one.
- GitHub records a delivery as a failure "if your server is down or takes longer than 10 seconds to respond". "GitHub does not automatically redeliver failed deliveries." Source: [Handling failed webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries). A laptop that sleeps loses events, so Mobius needs a catch-up poll anyway.
- smee.io: GitHub docs use it for local development. They also say: "You should not use Smee.io to forward your webhooks in production." Source: [Building a GitHub App that responds to webhook events](https://docs.github.com/en/apps/creating-github-apps/writing-code-for-a-github-app/building-a-github-app-that-responds-to-webhook-events).
- `gh webhook forward`: "It is not supported for use in production environments." It works only for repository and organization webhooks, not App webhooks. "Only one person can use webhook forwarding at a time for each repository." Source: [Using the GitHub CLI to forward webhooks for testing](https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/using-the-github-cli-to-forward-webhooks-for-testing).
- Tunnels give a public URL without an open port. Cloudflare Tunnel connects resources "without a publicly routable IP address". Source: [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/). Tailscale Funnel routes "traffic from the broader internet to a local service" on ports 443, 8443, or 10000. Source: [Tailscale Funnel](https://tailscale.com/kb/1223/funnel). Each tunnel is one more account and one more process for the Owner.

### Polling

- "Making a conditional request does not count against your primary rate limit if a `304` response is returned." Source: [Best practices for using the REST API](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api).
- Live check: `GET /repos/Mobius-Toolkit/Mobius/issues?per_page=1` with `If-None-Match` returned `304`. `X-RateLimit-Used` stayed at 31 for three `304` responses. A `200` response added 1.
- Keep the same query parameters on each poll, so that the ETag stays stable. Same source.

Candidate endpoints:

| Endpoint | What it gives | Fit |
|---|---|---|
| `GET /repos/{o}/{r}/issues?since=&sort=updated` | Issues and PRs with a new `updated_at`, with labels | Good: finds `mobius:ready` and state changes |
| `GET /repos/{o}/{r}/issues/comments?since=` | All new issue and PR conversation comments | Good: Owner comments |
| `GET /repos/{o}/{r}/pulls/comments?since=` | All new PR review comments (diff lines) | Good |
| `GET /repos/{o}/{r}/pulls/{n}/reviews` | Reviews of one PR | Per PR; no repository-wide list |
| `GET /repos/{o}/{r}/issues/events` | Label, close, and other issue events | Optional |
| `GET /repos/{o}/{r}/events` | Activity stream | Bad: latency "from 30s to 6h" |
| `GET /notifications` | Notification threads | Bad: classic PAT only |

Sources:
- `since` on issues, issue comments, and review comments: "Only show results that were last updated after the given time." [Issues](https://docs.github.com/en/rest/issues/issues), [Issue comments](https://docs.github.com/en/rest/issues/comments), [Pull request review comments](https://docs.github.com/en/rest/pulls/comments).
- Events API: "This API is not built to serve real-time use cases. Depending on the time of day, event latency can be anywhere from 30s to 6h." It keeps a maximum of 300 events from the last 30 days. Source: [Events](https://docs.github.com/en/rest/activity/events).
- Notifications: "These endpoints only support authentication using a personal access token (classic)." Source: [Notifications](https://docs.github.com/en/rest/activity/notifications).
- Both the events and the notifications APIs send `X-Poll-Interval`, and GitHub asks clients to obey it. Same sources.

Cost: one poll cycle for one repository is three conditional requests. At one cycle each 30 seconds, the maximum is 360 requests per hour. Most responses are `304` and cost nothing. Latency is the poll interval.

Open point: the docs do not say if a submitted review changes the PR `updated_at`. Confirm this in the first implementation. If it does not, poll `GET .../pulls/{n}/reviews` for each open Mobius PR with an ETag.

## 3. APIs that Mobius needs

### Sub-issues

- "You can add up to 100 sub-issues per parent issue" and "create up to eight levels of nested sub-issues". Source: [Adding sub-issues](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/adding-sub-issues).
- Endpoints: `GET .../issues/{n}/sub_issues`, `POST .../issues/{n}/sub_issues`, `DELETE .../issues/{n}/sub_issue`, `PATCH .../issues/{n}/sub_issues/priority`, `GET .../issues/{n}/parent`. Source: [Sub-issues](https://docs.github.com/en/rest/issues/sub-issues).
- The body field `sub_issue_id` is the issue `id`, not the issue number. Same source.
- A sub-issue can belong to another organization than its parent. Source: [GitHub changelog 2025-09-11](https://github.blog/changelog/2025-09-11-a-rest-api-for-github-projects-sub-issues-improvements-and-more/).
- Live check: `GET /repos/Mobius-Toolkit/Mobius/issues/3` returns `sub_issues_summary` (`total: 27`) and `issue_dependencies_summary`.

### Issue dependencies (blocked by)

- "You can link up to 50 issues for each relationship type." Source: [GitHub changelog 2025-08-21](https://github.blog/changelog/2025-08-21-dependencies-on-issues/).
- Endpoints: `GET` and `POST .../issues/{n}/dependencies/blocked_by`, `DELETE .../dependencies/blocked_by/{issue_id}`, `GET .../dependencies/blocking`. The body field `issue_id` is the issue `id`. Source: [Issue dependencies](https://docs.github.com/en/rest/issues/issue-dependencies).
- Live check: `GET /repos/Mobius-Toolkit/Mobius/issues/16/dependencies/blocking` returns issues 19, 21, 23, and 30.

### Labels

- `POST .../issues/{n}/labels`, `DELETE .../issues/{n}/labels/{name}`, `POST .../labels` to create `mobius:ready`. Source: [Labels](https://docs.github.com/en/rest/issues/labels). Permission: Issues write.

### Draft PRs

- `POST /repos/{o}/{r}/pulls` takes `draft: true`. Source: [Pull requests](https://docs.github.com/en/rest/pulls/pulls).
- Plan limit: "Draft pull requests are available in public repositories with GitHub Free and GitHub Free for organizations, GitHub Pro, and legacy per-repository billing plans, and in public and private repositories with GitHub Team and GitHub Enterprise Cloud." Same source. A private repository on a Free organization cannot have draft PRs.
- `PATCH /repos/{o}/{r}/pulls/{n}` has no `draft` field. Same source.
- Live check: `Mobius-Toolkit/Mobius` is public and owned by an organization.

### Ready for review

- GraphQL mutation `markPullRequestReadyForReview(input: {pullRequestId: ID!})`. The reverse is `convertPullRequestToDraft`. Source: [GraphQL pulls reference](https://docs.github.com/en/graphql/reference/pulls#markpullrequestreadyforreview).
- `pullRequestId` is the PR `node_id` from the REST response.
- "Marking a pull request as ready for review will request reviews from any code owners." Source: [About pull requests](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/proposing-changes-to-your-work-with-pull-requests/about-pull-requests).

### PR reviews and review comments

- `POST .../pulls/{n}/reviews` with `event` = `APPROVE`, `REQUEST_CHANGES`, or `COMMENT`, and a `comments` array with `path`, `line`, `side`, `start_line`, `body`. Without `event`, the review stays `PENDING`. Source: [Pull request reviews](https://docs.github.com/en/rest/pulls/reviews).
- One call posts the review body and all line comments together.
- `POST .../pulls/{n}/comments/{comment_id}/replies` answers a review comment thread. Source: [Pull request review comments](https://docs.github.com/en/rest/pulls/comments).

### GitHub Actions and drafts

- The `pull_request` event runs by default only for `opened`, `synchronize`, and `reopened`. Other types, for example `ready_for_review` and `converted_to_draft`, need the `types` key. Source: [Events that trigger workflows](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows).
- The pull request object has a `draft` boolean. Source: [Pull requests](https://docs.github.com/en/rest/pulls/pulls).
- `jobs.<job_id>.if` stops a job when the condition is false, and GitHub marks the job as skipped. Source: [Workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#jobsjob_idif).
- Pattern for the repositories that Mobius works on:

```yaml
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review]
jobs:
  ci:
    if: github.event.pull_request.draft == false
```

- Hazard: "If a workflow is skipped due to path filtering, branch filtering, or a commit message, then checks associated with that workflow will remain in a 'Pending' state." Same source. Use the job `if`, not a workflow filter, for drafts.

### Do PRs from Mobius trigger workflows

- "Events triggered by the `GITHUB_TOKEN` will not create a new workflow run." The exceptions are `workflow_dispatch`, `repository_dispatch`, and `pull_request` `opened`, `synchronize`, `reopened` in an approval-required state. Source: [Triggering a workflow](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow).
- "You can use a GitHub App installation access token or a personal access token instead of `GITHUB_TOKEN`." This "also lets `pull_request` workflows run automatically (without the approval prompt)". Same source.
- Mobius does not run inside Actions and does not use `GITHUB_TOKEN`. PRs, pushes, and "ready for review" from the App token trigger workflows normally.

### Rust client (octocrab)

Source: [octocrab 0.54.2 on docs.rs](https://docs.rs/octocrab/latest/octocrab/).

- App auth: `Octocrab::builder().app(...)`, then `installation(id)`. The client refreshes the installation token. [Octocrab](https://docs.rs/octocrab/latest/octocrab/struct.Octocrab.html).
- Sub-issues: `IssueHandler` has `add_sub_issue`, `list_sub_issues`, `remove_sub_issue`, `reprioritize_sub_issue`, `get_parent_issue`. [IssueHandler](https://docs.rs/octocrab/latest/octocrab/issues/struct.IssueHandler.html).
- Dependencies: no typed method. Use `_get` and `_post`.
- Ready for review: no typed method. Use `Octocrab::graphql`.
- Conditional requests: `etag::Etagged` exists, and `_get_with_headers` can send `If-None-Match`. [Etagged](https://docs.rs/octocrab/latest/octocrab/etag/struct.Etagged.html).

## Implications for Mobius

1. Identity: the App bot login separates agent activity from Owner activity. Mobius ignores events whose author is its own bot. This rule stops loops where the agent reacts to its own comment.
2. Approval: the Owner can approve agent PRs, because the App is the author. The Reviewer posts `COMMENT` reviews with the same App. If the Reviewer must `APPROVE` or `REQUEST_CHANGES`, a second App is necessary.
3. Transport: poll first. No public URL, no relay, no tunnel. Mobius stores the ETag and the `since` cursor per endpoint in SQLite. After a laptop sleep, the next poll catches up.
4. Webhooks later are an option, not a need. The App registration can set `hook_attributes.active` to `false`.
5. Budget: the limit that matters is 500 content-creating requests per hour, not 5,000 reads. Mobius must batch line comments into one review call.
6. Draft PRs need a public repository or a paid plan. Mobius must show a clear error if `draft: true` fails.
7. Setup: the Mobius web UI hosts the manifest form and the `redirect_url` handler on `localhost`. The Owner clicks twice: "Create GitHub App" and "Install".
8. Sub-issue and dependency calls take the issue `id`, not the number. Mobius must read the `id` first.

## Recommendation

Use one GitHub App per Mobius server. Register it with the manifest flow from the Mobius web UI. Request Issues write, Pull requests write, Contents write, and Metadata read. Do not request Workflows.

Poll the REST API every 30 seconds per repository with ETags and `since` cursors. Do not use webhooks in the first version.

Use octocrab with App auth. Use `_get` and `_post` for dependencies, and GraphQL for "ready for review".

Tell the Owner to use `if: github.event.pull_request.draft == false` with the `ready_for_review` type in CI workflows.
