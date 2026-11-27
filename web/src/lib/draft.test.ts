import { afterEach, describe, expect, it, vi } from "vitest";
import { convertingDraftKeys, emptyDraft, loadDraft, readDraft } from "./draft";

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

  it("reads a stored voice with no seconds and no text as 0 seconds and an empty text", () => {
    const stored = {
      ...emptyDraft,
      voice: { state: "ready" as const, audio: null, conversion: "", error: "" },
    };

    expect(readDraft(stored).voice).toEqual({ ...stored.voice, seconds: 0, text: "" });
  });

  it("keeps the seconds and the text of a stored voice", () => {
    const voice = {
      state: "ready" as const,
      audio: null,
      conversion: "",
      error: "",
      seconds: 12,
      text: "Plant",
    };

    expect(readDraft({ ...emptyDraft, voice }).voice).toEqual(voice);
    expect(readDraft(undefined)).toEqual(emptyDraft);
  });
});
