import { describe, expect, it } from "vitest";
import {
  groupByProject,
  progressOfProjects,
  rowKey,
  stops,
  taskItem,
  withoutClosed,
  withoutFolded,
  type TaskLine,
} from "./model";

const line = (overrides: Partial<TaskLine>): TaskLine => ({
  blockedBy: [],
  startWait: "",
  depth: 0,
  implementer: null,
  kind: "task",
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

const project = (number: number, overrides: Partial<TaskLine> = {}) =>
  line({
    number,
    kind: "project",
    url: `https://github.com/owner/shop/issues/${number}`,
    ...overrides,
  });

const task = (number: number, overrides: Partial<TaskLine> = {}) =>
  line({ number, url: `https://github.com/owner/shop/issues/${number}`, ...overrides });

describe("the Projects of a Workstream", () => {
  it("puts the lines with no Project first and each Project with its lines after them", () => {
    const lines = [
      project(1),
      task(2, { depth: 1 }),
      task(3),
      project(4),
      task(5, { depth: 1 }),
      project(6, { depth: 1 }),
      task(7, { depth: 2 }),
      task(8),
    ];

    expect(groupByProject(lines).map(({ number }) => number)).toEqual([3, 8, 1, 2, 4, 5, 6, 7]);
  });

  it("counts the closed lines below a Project at each depth, also when the closed lines are hidden", () => {
    const lines = [
      project(1),
      task(2, { depth: 1 }),
      task(3, { depth: 1, state: "closed" }),
      task(4, { depth: 2, state: "closed" }),
      task(5),
    ];

    const shown = withoutClosed(lines);

    expect(shown.map(({ number }) => number)).toEqual([1, 2, 5]);
    expect(progressOfProjects(lines).get(lines[0].url)).toEqual({ done: 2, all: 3 });
  });

  it("removes the lines below a folded Project", () => {
    const lines = [project(1), task(2, { depth: 1 }), task(3, { depth: 2 }), task(4)];

    expect(withoutFolded(lines, [lines[0].url]).map(({ number }) => number)).toEqual([1, 4]);
    expect(withoutFolded(lines, []).map(({ number }) => number)).toEqual([1, 2, 3, 4]);
  });
});
