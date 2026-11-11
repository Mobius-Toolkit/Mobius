import { describe, expect, it } from "vitest";
import { currentInbox, dropMarks, hideInboxItem, restoreInboxItem } from "./inbox";

const row = {
  number: 5,
  pullRequest: null,
  pullRequestUrl: null,
  question: true,
  reason: "",
  repository: "owner/shop",
  stopped: false,
  title: "",
  url: "",
  workstream: 12,
};

describe("the mark of an answered issue", () => {
  it("ends when a new list of needs-human issues has no row of the issue", () => {
    hideInboxItem({ id: 301, repository: "owner/shop", issue: 5 });

    dropMarks([row]);
    expect(currentInbox().answered.map((other) => other.mark)).toEqual([true]);

    dropMarks([]);
    expect(currentInbox().answered.map((other) => other.mark)).toEqual([false]);
    expect(currentInbox().answered.map((other) => other.item)).toEqual([true]);

    restoreInboxItem(301);
    expect(currentInbox().answered).toEqual([]);
  });
});
