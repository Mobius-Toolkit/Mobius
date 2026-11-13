import { afterEach, describe, expect, it, vi } from "vitest";
import { convertingDraftKeys, emptyDraft, loadDraft } from "./draft";

describe("the read of a draft", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("gives the empty draft when the browser store fails", async () => {
    vi.stubGlobal("indexedDB", {
      open: () => {
        throw new Error("The store is not open.");
      },
    });

    expect(await loadDraft("chatDraft:owner/shop/1")).toEqual(emptyDraft);
  });

  it("gives no key of a converting draft when the browser store fails", async () => {
    expect(await convertingDraftKeys()).toEqual([]);
  });
});
