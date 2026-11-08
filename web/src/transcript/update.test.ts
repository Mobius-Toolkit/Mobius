import { Option } from "effect";
import { describe, expect, it } from "vitest";
import {
  Command as StoryCommand,
  expectNoOutMessage,
  expectOutMessage,
  given,
  message,
  model,
  story,
} from "foldkit/story";
import { lowLoadReason } from "@/lib/agents";
import { FetchLog, FetchOlder, StartCheck } from "./commands";
import { Message, OutMessage } from "./message";
import { initialModel, type Model } from "./model";
import type { Agent, Line } from "./schemas";
import { merge, reflectStartable, update } from "./update";

const agent: Agent = {
  effort: "high",
  endReason: "",
  endedAt: null,
  harness: "devin",
  id: 7,
  issue: 39,
  model: "swe-1.5",
  name: "Implementer",
  organization: "owner",
  parent: null,
  pausedUntil: null,
  queueReason: "",
  repository: "owner/shop",
  role: "implementer",
  startedAt: "2026-05-01T10:00:00Z",
  title: "Task 39",
  working: true,
  workstream: 12,
  workstreamRepository: "owner/shop",
};

const line = (id: number, overrides: Partial<Line> = {}): Line => ({
  body: "",
  error: false,
  folded: false,
  harnessToolName: "",
  id,
  kind: "update",
  raw: `{"id":${id}}`,
  session: 7,
  text: `Line ${id}`,
  time: "2026-05-01T10:00:00Z",
  ...overrides,
});

const createModel = (overrides: Partial<Model> = {}): Model => ({
  ...initialModel(agent),
  ...overrides,
});

const idsOf = (m: Model) => Option.getOrElse(m.lines, () => []).map(({ id }) => id);

describe("the lines of the log", () => {
  it("keeps the longer raw text of a line that the agent extended, and sorts by id", () => {
    const longer = line(2, { raw: '{"id":2,"more":true}' });

    expect(merge([line(3), line(2)], [longer, line(1), line(2, { raw: "{}" })])).toEqual([
      line(1),
      longer,
      line(3),
    ]);
  });

  it("shows the lines of the first load and tells if the session has older rows", () => {
    story(
      update,
      given(createModel()),
      message(Message.Loaded({ lines: [line(5), line(6)], older: true, initial: true })),
      model((m) => {
        expect(idsOf(m)).toEqual([5, 6]);
        expect(m.older).toBe(true);
        expect(m.newest).toEqual(Option.some(6));
      }),
    );
  });

  it("keeps the older flag after a load that follows a reconnect", () => {
    story(
      update,
      given(createModel({ lines: Option.some([line(5)]), older: true, newest: Option.some(5) })),
      message(Message.Loaded({ lines: [line(5), line(6)], older: false, initial: false })),
      model((m) => {
        expect(idsOf(m)).toEqual([5, 6]);
        expect(m.older).toBe(true);
      }),
    );
  });

  it("adds a line from the stream at the end, and counts a new version of the lines", () => {
    story(
      update,
      given(createModel({ lines: Option.some([line(5)]), newest: Option.some(5) })),
      message(Message.LineReceived({ line: line(6) })),
      model((m) => {
        expect(idsOf(m)).toEqual([5, 6]);
        expect(m.newest).toEqual(Option.some(6));
        expect(m.version).toBe(1);
      }),
    );
  });

  it("does not add a line from the stream that comes before the loaded lines", () => {
    story(
      update,
      given(createModel({ lines: Option.some([line(5)]), newest: Option.some(5) })),
      message(Message.LineReceived({ line: line(3) })),
      model((m) => {
        expect(idsOf(m)).toEqual([5]);
        expect(m.version).toBe(0);
      }),
    );
  });

  it("shows a failed load, and ends the wait for older lines", () => {
    story(
      update,
      given(createModel({ loadingOlder: true })),
      message(Message.LoadFailed({ error: "no log" })),
      model((m) => {
        expect(m.error).toBe("no log");
        expect(m.loadingOlder).toBe(false);
      }),
    );
  });
});

describe("the connection of the stream", () => {
  it("reads the log when the connection opens, with no known line at the first open", () => {
    story(
      update,
      given(createModel()),
      message(Message.StreamOpened()),
      model((m) => {
        expect(m.opened).toBe(true);
      }),
      StoryCommand.expectExact(FetchLog({ id: 7, known: Option.none() })),
      StoryCommand.resolve(
        FetchLog,
        Message.Loaded({ lines: [line(1)], older: false, initial: true }),
      ),
    );
  });

  it("reads the log from the last known line after a new connection", () => {
    story(
      update,
      given(createModel({ lines: Option.some([line(5)]), newest: Option.some(5) })),
      message(Message.StreamClosed()),
      model((m) => {
        expect(m.opened).toBe(false);
      }),
      message(Message.StreamRetried()),
      StoryCommand.expectExact(FetchLog({ id: 7, known: Option.some(5) })),
      StoryCommand.resolve(
        FetchLog,
        Message.Loaded({ lines: [line(5), line(6)], older: false, initial: false }),
      ),
      model((m) => {
        expect(idsOf(m)).toEqual([5, 6]);
      }),
    );
  });

  it("asks the host for the login on a 401, and ends the waits", () => {
    story(
      update,
      given(createModel({ loadingOlder: true, starting: true })),
      message(Message.LoginRequired()),
      model((m) => {
        expect(m.loadingOlder).toBe(false);
        expect(m.starting).toBe(false);
      }),
      expectOutMessage(OutMessage.LoginRequired()),
    );
  });
});

describe("the older lines", () => {
  const loaded = { lines: Option.some([line(5), line(6)]), older: true };

  it("reads no second page during the wait for an older page", () => {
    story(
      update,
      given(createModel({ ...loaded, loadingOlder: true })),
      message(Message.NearTop()),
      StoryCommand.expectNone(),
    );
  });

  it("reads one older page near the top", () => {
    story(
      update,
      given(createModel(loaded)),
      message(Message.NearTop()),
      model((m) => {
        expect(m.loadingOlder).toBe(true);
      }),
      StoryCommand.expectExact(FetchOlder({ id: 7, before: 5 })),
      StoryCommand.resolve(
        FetchOlder,
        Message.OlderLoaded({ lines: [line(3), line(4)], older: true }),
      ),
      model((m) => {
        expect(m.loadingOlder).toBe(false);
        expect(idsOf(m)).toEqual([3, 4, 5, 6]);
      }),
    );
  });

  it("adds the older page, and stops when the page is the first one", () => {
    story(
      update,
      given(createModel(loaded)),
      message(Message.NearTop()),
      StoryCommand.resolve(
        FetchOlder,
        Message.OlderLoaded({ lines: [line(3), line(4)], older: false }),
      ),
      model((m) => {
        expect(idsOf(m)).toEqual([3, 4, 5, 6]);
        expect(m.older).toBe(false);
        expect(m.loadingOlder).toBe(false);
      }),
      message(Message.NearTop()),
      StoryCommand.expectNone(),
    );
  });

  it("reads no older page when the session has none", () => {
    story(
      update,
      given(createModel({ lines: Option.some([line(5)]), older: false })),
      message(Message.NearTop()),
      StoryCommand.expectNone(),
    );
  });
});

describe("the view of the log", () => {
  it("shows the jump control for a line that arrives while the Owner reads older lines", () => {
    story(
      update,
      given(createModel()),
      message(Message.ArrivedWhileAway()),
      model((m) => {
        expect(m.behind).toBe(true);
      }),
      message(Message.JumpedToEnd()),
      model((m) => {
        expect(m.behind).toBe(false);
        expect(m.jumps).toBe(1);
      }),
      message(Message.ArrivedWhileAway()),
      message(Message.ReachedEnd()),
      model((m) => {
        expect(m.behind).toBe(false);
        expect(m.jumps).toBe(1);
      }),
    );
  });

  it("opens and closes a line, and shows and hides its raw text", () => {
    story(
      update,
      given(createModel()),
      message(Message.ToggledOpen({ id: 5 })),
      message(Message.ToggledOpen({ id: 6 })),
      message(Message.ToggledOpen({ id: 5 })),
      message(Message.ToggledRaw({ id: 6 })),
      model((m) => {
        expect(m.toggledOpen).toEqual([6]);
        expect(m.rawShown).toEqual([6]);
      }),
      message(Message.ToggledRaw({ id: 6 })),
      model((m) => {
        expect(m.rawShown).toEqual([]);
      }),
    );
  });

  it("gives the Back control to the parent", () => {
    story(
      update,
      given(createModel()),
      message(Message.ClickedBack()),
      expectOutMessage(OutMessage.Closed()),
    );
  });
});

describe("the check of an agent that waits for a low load", () => {
  it("is startable only for the low load reason", () => {
    expect(createModel().startable).toBe(false);
    expect(initialModel({ ...agent, queueReason: lowLoadReason }).startable).toBe(true);
  });

  it("starts the check, and ends the wait after the 204", () => {
    story(
      update,
      given(createModel({ startable: true, startError: "old" })),
      message(Message.ClickedStartCheck()),
      model((m) => {
        expect(m.starting).toBe(true);
      }),
      StoryCommand.expectExact(StartCheck({ id: 7 })),
      StoryCommand.resolve(StartCheck, Message.StartedCheck()),
      model((m) => {
        expect(m.starting).toBe(false);
        expect(m.startError).toBe("");
      }),
      expectNoOutMessage(),
    );
  });

  it("shows the reason when the check does not start", () => {
    story(
      update,
      given(createModel({ startable: true })),
      message(Message.ClickedStartCheck()),
      StoryCommand.resolve(
        StartCheck,
        Message.StartCheckFailed({ error: "The agent does not wait for a low load." }),
      ),
      model((m) => {
        expect(m.starting).toBe(false);
        expect(m.startError).toBe("The agent does not wait for a low load.");
      }),
    );
  });

  it("clears the wait and the reason when the agent stops to be startable", () => {
    const { model: next } = reflectStartable(
      createModel({ startable: true, starting: true, startError: "failed" }),
      false,
    );

    expect(next).toMatchObject({ startable: false, starting: false, startError: "" });
    expect(
      reflectStartable(createModel({ startable: false, startError: "failed" }), true).model,
    ).toMatchObject({ startable: true, startError: "failed" });
  });
});
