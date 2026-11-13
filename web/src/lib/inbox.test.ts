import { Effect } from "effect";
import { afterEach, describe, expect, it, vi } from "vitest";
import { currentInbox, dropMarks, hideInboxItem, loadInbox, restoreInboxItem } from "./inbox";

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

describe("the read of the Inbox", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("gives the items that the server answers", async () => {
    vi.stubGlobal("fetch", async () => new Response(JSON.stringify({ data: [{ id: 4 }] })));

    await Effect.runPromise(loadInbox());

    expect(currentInbox().items.map((item) => item.id)).toEqual([4]);
  });

  it("keeps the items when the request fails", async () => {
    vi.stubGlobal("fetch", async () => new Response(JSON.stringify({ data: [{ id: 4 }] })));
    await Effect.runPromise(loadInbox());
    const before = currentInbox();
    vi.stubGlobal("fetch", () => Promise.reject(new TypeError("offline")));

    await Effect.runPromise(loadInbox());

    expect(currentInbox()).toBe(before);
  });

  it("keeps the items when the server answers with an error", async () => {
    const before = currentInbox();
    vi.stubGlobal(
      "fetch",
      async () => new Response(JSON.stringify({ error: "boom" }), { status: 500 }),
    );

    await Effect.runPromise(loadInbox());

    expect(currentInbox()).toBe(before);
  });
});
