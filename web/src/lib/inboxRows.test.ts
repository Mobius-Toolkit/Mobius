import { describe, expect, it } from "vitest";
import type { InboxItem } from "@/chat/schemas";
import { inboxRows } from "./inboxRows";

function item(overrides: Partial<InboxItem>): InboxItem {
  return {
    id: 1,
    kind: "question",
    action: null,
    answer: "",
    blockingTodos: [],
    dismissedAt: null,
    issue: 0,
    link: "",
    mergeRefusal: "",
    merging: false,
    organization: "test-org",
    pullRequest: 0,
    pullRequestUrl: "",
    questions: [],
    reason: "",
    reportUrl: "",
    repository: "owner/shop",
    text: "",
    time: "2026-11-04T08:00:00Z",
    title: "",
    todo: null,
    withdrawn: false,
    workstream: 1,
    workstreamRepository: "owner/shop",
    mergeQueuePlace: null,
    mergeQueueSize: 0,
    ...overrides,
  };
}

const todo = (id: number, overrides: Partial<InboxItem> = {}) =>
  item({ id, kind: "todo", pullRequest: 22, issue: 5, ...overrides });

describe("inboxRows", () => {
  it("gives a pull request with its three todos one row, oldest todo first", () => {
    const pullRequest = item({ id: 10, kind: "ready for review", pullRequest: 22, issue: 5 });

    const rows = inboxRows([todo(13), pullRequest, todo(11), todo(12)]);

    expect(rows.map((row) => row.item.id)).toEqual([10]);
    expect(rows[0].todos.map((other) => other.id)).toEqual([11, 12, 13]);
  });

  it("gives the todos of a pull request with no item ready for review one row with the oldest todo", () => {
    const rows = inboxRows([todo(12), todo(11)]);

    expect(rows.map((row) => row.item.id)).toEqual([11]);
    expect(rows[0].todos.map((other) => other.id)).toEqual([11, 12]);
  });

  it("gives the todos of another repository with the same pull request number another row", () => {
    const pullRequest = item({ id: 10, kind: "ready for review", pullRequest: 22, issue: 5 });

    const rows = inboxRows([pullRequest, todo(11), todo(12, { repository: "owner/blog" })]);

    expect(rows.map((row) => [row.item.id, row.todos.map((other) => other.id)])).toEqual([
      [10, [11]],
      [12, [12]],
    ]);
  });

  it("gives an item of another kind a row with no todos, sorted by the id of the item", () => {
    const rows = inboxRows([item({ id: 9, kind: "question" }), todo(7)]);

    expect(rows.map((row) => [row.item.id, row.todos.length])).toEqual([
      [7, 1],
      [9, 0],
    ]);
  });
});
