# The Lead runs as short sessions, not one long session

A Lead is not one Harness session that lives as long as its Workstream. Each Workstream has one chat session and one event session. The chat session starts on demand when the Owner writes, and Mobius closes it after `lead_idle_timeout` with no turn. The event session starts when an event arrives. It gets one event in each turn, and Mobius closes it after `event_idle_timeout` with no event. Each new session gets the Brief, the index of the Workstream memory, the task list, and either the last chat messages or the first event. So the context of the Lead stays small, events never break a discussion with the Owner, and idle Workstreams use no process.

## Considered Options

- **One session for each Workstream, always on:** rejected. The Harness compacts the context again and again, and its quality degrades over months. Each open Workstream holds a process also when nothing occurs, and each restart needs `session/load` with a replay of all history.
- **One session on demand that gets both the chat and the events:** rejected. A batch of GitHub events arrives in the middle of a discussion with the Owner and fills the context with text that the Owner did not ask about.
- **A batch of events in one turn:** rejected. The Lead can miss one event of the batch.
- **One new session for each event:** rejected. The session does not know the event before it, for example an earlier comment on the same issue.

## Consequences

- A decision reaches later sessions only through a note in the Workstream memory. Before Mobius closes a chat session or an event session, it tells the Lead to save what the next session needs.
- The text of an event session never goes to the chat. An event session speaks to the Owner only through a tool, and each such message is also an Inbox item.
- Each Workstream runs a maximum of one event session at a time, with one event in each turn.
- After a Mobius restart or a Lead crash, Mobius starts a new session and sends the same prompt again. Mobius does not use `session/load`. An event counts as delivered only when its turn ends, so the Lead can see an event two times after a crash.
- The Lead has no code in its working directory. A Researcher Worker reads the code and reports facts to the session that started it.
