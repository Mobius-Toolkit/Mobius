# Mobius keeps a local copy of the Workstream and task data

The Workstreams screen and the Tasks tab read all data from GitHub on each load. The calls run one after the other. `workstreams::list` makes one call for each repository, one `sub_issues` call for each Workstream, and one `issue_events` call for each Workstream with `mobius:autopilot`. `tasks::list` makes one `sub_issues` call for each issue in the tree, and more calls for each blocked issue. The UI reads the list again after each `Live::Workstreams` event. So the two screens are slow.

## Decision

GitHub holds all work state: the Workstream issues, the Briefs, the task tree, the blockers, the labels, the PRs, the review threads, and the comments. GitHub stays the source of truth. A human can change the work on GitHub at any time.

The SQLite store holds the local-only state: the tasks and their counters, the sessions and their transcripts, the chat history, the Lead event queue, the activity feed, the Inbox, the Harness pauses, and the poll cursors.

The store also holds a copy of the data for the two screens, for each repository:

- The open Workstreams: the title, the body, the Autopilot state, and "all tasks closed".
- The task tree: the sub-issues, the state, the labels, the author, and the blockers.

The store holds no other GitHub state.

The GitHub poll updates the copy. A full sync runs at startup. The two screens read only the store.

## Limits

- Mobius never writes to GitHub from the copy.
- Other engine code continues to read GitHub. This includes dispatch, the start of Autopilot, and recovery.
- Each poll compares GitHub with the task rows. A label removal stops a live task.
- Each session reads the Brief, the labels, and the threads from GitHub at start.
- ETags and `since` cursors keep most polls free of rate limit cost.

## Considered Options

- **No copy, with reads from GitHub on each load:** rejected. The calls need the data of each Workstream and each issue in the tree. Parallel calls reduce the time, but each load still costs many calls and rate limit.
- **A full local mirror of issues, labels, and threads:** rejected. Each change on GitHub needs code that updates the mirror, and a missed change makes Mobius act on old state. The copy for the two screens avoids this risk, because dispatch and Autopilot read GitHub.
- **A rebuild of the task rows from GitHub after a loss of the store:** rejected. It needs much code for a rare case. Mobius marks each orphan task `mobius:needs-human`, and a trusted user starts it again.

## Consequences

- The data on the two screens can be up to one poll interval old.
- A new sub-issue link, a new blocker, or a blocker that closes possibly does not change the `updated_at` of an issue. The `since` poll does not see these changes. A separate sync finds them and updates the copy.
- A loss of the store loses the copy and the other local-only state, but no work state. The full sync at startup builds the copy again.
- A missed change shows old data on the two screens only. It does not affect dispatch or Autopilot, because these read GitHub.
- The engine needs code that updates the copy from the poll.
