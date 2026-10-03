# Mobius keeps a local copy of the Workstream and task data

This ADR replaces ADR 0008 in part. It replaces only the rule that Mobius keeps no copy of the Workstream and task data for the two screens below. The other decisions of ADR 0008 stay valid.

The Workstreams screen and the Tasks tab read all data from GitHub on each load. The calls run one after the other. `workstreams::list` makes one call for each repository, one `sub_issues` call for each Workstream, and one `issue_events` call for each Workstream with `mobius:autopilot`. `tasks::list` makes one `sub_issues` call for each issue in the tree, and more calls for each blocked issue. The UI reads the list again after each `Live::Workstreams` event. So the two screens are slow.

The SQLite store keeps a copy of this data for each repository:

- The open Workstreams: the title, the body, the Autopilot state, and "all tasks closed".
- The task tree: the sub-issues, the state, the labels, the author, and the blockers.

The GitHub poll updates the copy. A full sync runs at startup. The two screens read only the store.

## Limits

- GitHub stays the source of truth. Mobius never writes to GitHub from the copy.
- The copy holds only the data for the two screens. The store does not mirror other GitHub state.
- Other engine code continues to read GitHub. This includes dispatch, the start of Autopilot, and recovery.
- The local-only state of ADR 0008 stays local-only: the tasks, the sessions, the chat history, the Lead event queue, the activity feed, the Inbox, the Harness pauses, and the poll cursors.
- Each poll still compares GitHub with the task rows. A label removal still stops a live task.
- Mobius still does not rebuild the task rows after a loss of the store. Mobius marks each orphan task `mobius:needs-human`.

## Considered Options

- **No copy, with faster reads from GitHub (ADR 0008):** rejected. The calls need the data of each Workstream and each issue in the tree. Parallel calls reduce the time, but each load still costs many calls and rate limit.

## Consequences

- The data on the two screens can be up to one poll interval old.
- A new sub-issue link, a new blocker, or a blocker that closes possibly does not change the `updated_at` of an issue. The `since` poll does not see these changes. A separate sync finds them and updates the copy.
- A loss of the store loses the copy and the other local-only state, but no work state. The full sync at startup builds the copy again.
- A missed change shows old data on the two screens only. It does not affect dispatch or Autopilot, because these read GitHub.
- The engine needs code that updates the copy from the poll. This is the cost that ADR 0008 rejected.
