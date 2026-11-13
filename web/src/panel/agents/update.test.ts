import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import { FetchLog } from "@/transcript/commands";
import * as TranscriptFixture from "@/transcript/fixtures";
import { minutesUntil } from "@/lib/time";
import type { Agent } from "@/transcript/schemas";
import { Message } from "./message";
import { initialModel, shownAgents } from "./model";
import { update } from "./update";

const agent: Agent = {
  effort: "high",
  endReason: "",
  endedAt: null,
  harness: "devin",
  id: 7,
  issue: 39,
  keptUntil: null,
  issueTitle: null,
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

describe("the Transcript of the Agents tab", () => {
  it("reads the log with no known line when the Owner opens an agent, before the stream opens", () => {
    story(
      update,
      given(initialModel({ owner: "owner", name: "shop", number: 12, instance: "side" })),
      message(Message.OpenedAgent({ agent })),
      model((m) => {
        expect(Option.getOrThrow(m.transcript).agent.id).toBe(7);
      }),
      StoryCommand.expectExact(FetchLog({ id: 7, known: Option.none() })),
      StoryCommand.resolve(FetchLog, TranscriptFixture.loaded),
    );
  });
});

describe("the kept sessions of the Agents tab", () => {
  const stopped: Agent = { ...agent, id: 8, endedAt: "2026-05-01T10:30:00Z", working: false };
  const kept: Agent = { ...stopped, id: 6, keptUntil: "2026-05-01T11:30:00Z" };

  it("shows a kept session with the switch off, in the order of the ids", () => {
    expect(shownAgents([stopped, kept, agent], false).map((shown) => shown.id)).toEqual([7, 6]);
    expect(shownAgents([stopped, kept, agent], true).map((shown) => shown.id)).toEqual([8, 7, 6]);
  });

  it("keeps the time of the last tick", () => {
    story(
      update,
      given(initialModel({ owner: "owner", name: "shop", number: 12, instance: "side" })),
      message(Message.Ticked({ now: 1_000 })),
      model((m) => {
        expect(m.now).toBe(1_000);
      }),
    );
  });

  it("counts the minutes to the close up, and shows at least 1", () => {
    const now = Date.parse("2026-05-01T10:48:00Z");
    expect(minutesUntil("2026-05-01T11:30:00Z", now)).toBe(42);
    expect(minutesUntil("2026-05-01T11:30:00Z", now - 1_000)).toBe(43);
    expect(minutesUntil("2026-05-01T10:48:30Z", now)).toBe(1);
    expect(minutesUntil("2026-05-01T10:00:00Z", now)).toBe(1);
  });
});
