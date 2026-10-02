# One Lead session gets the events and the Owner messages

Each Workstream has one Lead session. It starts on demand, when the Owner writes or when an event arrives. Mobius closes it after `lead_idle_timeout` with no turn. The session gets the Owner messages and the events in one queue, in the order that they occurred, and it gets one item in each turn. An event that arrives during a turn waits for the end of that turn. The chat shows each event to the Owner as a muted entry. In a turn for an event, the reply text goes only to the Transcript. The Lead writes to the Owner only through `tell_owner`, and only when the Owner must know about the event. So the Lead has the same context for an event and for a question about it.

The Lead can hold the event of the current turn with `hold_event`. A held event also holds each later event of the same task issue. The events of other tasks and the Owner messages continue. Mobius sends the held event again after the end of the next turn for an Owner message, in its task order. A held event stays held after a Mobius restart. It never becomes a `LeadFailed` item.

## Considered Options

- **Two sessions for each Workstream, one for the chat and one for the events:** rejected. Each session knew only a part of the context. The chat session did not know the events, and the event session did not know the chat. The Owner asked about an event that the chat session never saw.
- **One session for each Workstream, always on:** rejected. The Harness compacts the context again and again, and its quality degrades over months. Each open Workstream holds a process also when nothing occurs, and each restart needs `session/load` with a replay of all history.
- **A batch of events in one turn:** rejected. The Lead can miss one event of the batch.
- **A timer that frees a held event:** rejected. The Lead cannot know when the Owner answers, and a timer sends the event again before the Owner replies.
- **A rule in the Role prompt and a note in the Workstream memory, with no `hold_event`:** rejected. The Lead can forget the note, and the state of Mobius is durable.

## Consequences

- A decision reaches later sessions only through a note in the Workstream memory. Before Mobius closes a Lead session, it tells the Lead to save what the next session needs.
- Each Workstream runs a maximum of one Lead session at a time. An Owner message that arrives in a turn waits in the queue, and a stop cancels only a turn for an Owner message.
- A message from `tell_owner` is a chat message and an Inbox item.
- After a Mobius restart or a Lead crash, Mobius starts a new session and sends the same prompt again. Mobius does not use `session/load`. An event counts as delivered only when its turn ends, so the Lead can see an event two times after a crash.
- A drain leaves each event undelivered until the drain ends. A session that crashes again and again on an event gives the Owner a `LeadFailed` item.
- The Lead has no code in its working directory. A Researcher Worker reads the code and reports facts to the session that started it.
- Mobius does not treat a comment of the Lead session as an event (ADR 0009).
