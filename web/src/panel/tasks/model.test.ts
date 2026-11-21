import { describe, expect, it } from "vitest";
import { rowKey, stops, taskItem, type TaskLine } from "./model";

const line = (overrides: Partial<TaskLine>): TaskLine => ({
  blockedBy: [],
  startWait: "",
  depth: 0,
  implementer: null,
  number: 41,
  otherRepository: false,
  repository: "owner/shop",
  state: "open",
  title: "Add plan model",
  url: "https://github.com/owner/shop/issues/41",
  pausedUntil: null,
  wait: "",
  ...overrides,
});

describe("a task line with a wait", () => {
  it("has a stop button, also when its state is open", () => {
    expect(stops(line({ state: "open", wait: "owner_action" }))).toBe(true);
    expect(stops(line({ state: "working", wait: "paused" }))).toBe(true);
  });

  it("has no stop button when it has no wait and its state is open", () => {
    expect(stops(line({ state: "open" }))).toBe(false);
  });

  it("gets a new key when the wait changes", () => {
    const working = line({ state: "working" });
    expect(rowKey(line({ state: "working", wait: "slot" }))).not.toBe(rowKey(working));
  });

  it("gives the text of its wait as the reason of its Inbox item", () => {
    expect(taskItem(line({ state: "working", wait: "ci" })).reason).toBe("waits for CI");
  });
});
