# 07 — GitHub workflow

GitHub is the human collaboration surface: issues are task intake, PRs are the
review surface.

## Issues → tasks

```mermaid
sequenceDiagram
    participant GH as GitHub
    participant GS as GithubPollingSource (gh)
    participant RG as SourceRegistry
    participant DP as Dispatcher
    participant DB as SQLite

    GH->>GS: issue/comment/label events
    GS->>RG: Signal (dedupe_key)
    RG->>DB: insert (skip duplicates)
    RG->>DP: SignalIngested
    DP->>DP: route: scope keywords/labels ⊆ title+body
    DP->>DB: Task (Proposed, origin.signal_id, github_issue)
    Note over DP: Project Agent owns the task;<br/>human approves → Queued → Run
```

Routing is deliberately simple in M0: a signal lands on the first active
project whose `scope.keywords`/`labels` contain-match the title/body (repo
match preferred). Unmatched signals stay in the DB with no task — visible in
`GET /api/v1/signals` and the Signals UI.

## PRs → review

`PullRequestOpened` / `PullRequestReviewRequested` signals route the same way
but spawn `review`-kind tasks owned by the Reviewer agent (M2). Review results
are posted back as PR comments via `gh`.

## Human gates

- `ask_human` permission policy parks every ACP permission request until a
  human resolves it in chat or via `POST /api/v1/permissions/{id}`.
- Task approval (`Proposed → Approved`) is a human step in the UI; agent runs
  never bypass it.
