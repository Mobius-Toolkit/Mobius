## Agent skills

### Issue tracker

Issues live in GitHub Issues on `Mobius-Toolkit/Mobius`. See `docs/agents/issue-tracker.md`.

### Triage labels

The repository uses the five default triage labels. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one root `CONTEXT.md` and `docs/adr/`. See `docs/agents/domain.md`.

### Screenshots

The CI job `screenshots` (`.github/workflows/screenshots.yml`) makes the files in `crates/mobius-ui/screenshots/`. It commits them to the pull request branch. Do not add, change, or delete these files in a commit.
