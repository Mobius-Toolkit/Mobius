# Mobius keeps no copy of GitHub state

GitHub holds all work state: the Workstream issues, the Briefs, the task tree, the blockers, the labels, the PRs, the review threads, and the comments. Mobius reads this state from GitHub when it needs it and keeps no copy. The local store holds only the state that GitHub does not have: the tasks and their counters, the sessions and their transcripts, the chat history, the Lead event queue, the activity feed, the Inbox, the Harness pauses, and the poll cursors. So GitHub and Mobius never disagree about work state, and a human can change the work on GitHub at any time.

## Considered Options

- **A local mirror of issues, labels, and threads:** rejected. Each change on GitHub needs code that updates the mirror, and a missed change makes Mobius act on old state.
- **A rebuild of the local store from GitHub after a loss:** rejected. It needs much code for a rare case. Mobius marks each orphan task `mobius:needs-human`, and a trusted user starts it again.

## Consequences

- Each poll compares GitHub with the task rows only. When an issue of a live task loses the label that the task needs, Mobius stops the task. A human can stop stuck work with a label removal.
- Mobius does more GitHub reads. ETags and `since` cursors keep most polls free of rate limit cost.
- The Brief, the labels, and the threads are always current, because each session reads them at start.
- A loss of the store loses the chat history, the feed, and the transcripts, but no work state.
