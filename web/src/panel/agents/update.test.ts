import { Option } from "effect";
import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import { FetchLog } from "@/transcript/commands";
import * as TranscriptFixture from "@/transcript/fixtures";
import type { Agent } from "@/transcript/schemas";
import { Message } from "./message";
import { initialModel } from "./model";
import { update } from "./update";

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
