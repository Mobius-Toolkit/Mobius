import { describe, expect, it } from "vitest";
import type { AgentWait } from "@/api/api.gen";
import { displayState, pausedText, waitDot } from "./state";
import { dayClock } from "./time";

const until = "2026-09-28T12:00:00Z";

describe("pausedText", () => {
  it("gives the end of the pause", () => {
    expect(pausedText(until)).toBe(`paused until ${dayClock(until)}`);
  });
});

describe("displayState", () => {
  it.each<[AgentWait, string]>([
    ["start_off", "start paused"],
    ["drain", "waits for upgrade"],
    ["no_access", "no access"],
    ["slot", "queued"],
    ["check_slot", "waits for check"],
    ["low_load", "waits for load"],
    ["local_check", "checks"],
    ["init", "setup"],
    ["harness_start", "starting"],
    ["ci", "waits for CI"],
    ["lead", "waits for Lead"],
    ["owner", "waits for Owner"],
    ["start_implementer", "waits for start_implementer"],
    ["owner_action", "waits for Owner action"],
  ])("gives the text of the wait %s and the variant outline", (wait, text) => {
    expect(displayState({ state: "working", wait, pausedUntil: null })).toEqual({
      text,
      variant: "outline",
    });
  });

  it("gives the end of the pause for the wait paused", () => {
    expect(displayState({ state: "working", wait: "paused", pausedUntil: until })).toEqual({
      text: `paused until ${dayClock(until)}`,
      variant: "outline",
    });
  });

  it("gives paused with no time when the end of the pause is unknown", () => {
    expect(displayState({ state: "working", wait: "paused", pausedUntil: null })).toEqual({
      text: "paused",
      variant: "outline",
    });
  });

  it("gives the state and the variant secondary for no wait", () => {
    expect(displayState({ state: "reviewed", wait: "", pausedUntil: null })).toEqual({
      text: "reviewed",
      variant: "secondary",
    });
  });

  it("gives approved for a task that merges and has no wait", () => {
    expect(
      displayState({ state: "ready_for_review", merging: true, wait: "", pausedUntil: null }),
    ).toEqual({ text: "approved", variant: "secondary" });
  });

  it("gives a wait before approved", () => {
    expect(
      displayState({ state: "ready_for_review", merging: true, wait: "ci", pausedUntil: null })
        .text,
    ).toBe("waits for CI");
  });

  it("keeps the state of an agent that is kept or stopped", () => {
    expect(displayState({ state: "kept", wait: "", pausedUntil: null }).text).toBe("kept");
    expect(displayState({ state: "stopped", wait: "", pausedUntil: null }).text).toBe("stopped");
  });
});

describe("waitDot", () => {
  it("is green for no wait, a local check and the setup", () => {
    expect(waitDot("")).toBe("bg-green-600");
    expect(waitDot("local_check")).toBe("bg-green-600");
    expect(waitDot("init")).toBe("bg-green-600");
  });

  it("is amber for each other wait", () => {
    expect(waitDot("paused")).toBe("bg-amber-500");
    expect(waitDot("slot")).toBe("bg-amber-500");
    expect(waitDot("ci")).toBe("bg-amber-500");
  });
});
