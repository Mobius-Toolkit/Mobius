import { beforeEach, describe, expect, it, vi } from "vitest";
import { emptyDraft, updateDraft } from "./draft";
import { registerVoiceInput, settle } from "./voice";

describe("voice input field targeting", () => {
  const key = "chatDraft:test-org/test-repo/1428";

  beforeEach(async () => {
    await updateDraft(key, () => ({
      ...emptyDraft,
      voice: { state: "converting", audio: null, conversion: "", error: "" },
    }));
  });

  it("with two registered fields of one draft key, the conversion of a recording goes to the field that started it", async () => {
    const field1Texts: string[] = [];
    const field2Texts: string[] = [];

    const field1El = { id: "chat-input" };
    const field2El = { id: "task-input" };

    vi.stubGlobal("document", {
      getElementById: (id: string) =>
        id === "chat-input" ? field1El : id === "task-input" ? field2El : null,
    });

    const unregister1 = registerVoiceInput(key, "chat-input", (text) => field1Texts.push(text));
    const unregister2 = registerVoiceInput(key, "task-input", (text) => field2Texts.push(text));

    await settle(key, { text: "Hello from field 1", error: "" }, "chat-input");

    expect(field1Texts).toEqual(["Hello from field 1"]);
    expect(field2Texts).toEqual([]);

    await updateDraft(key, (draft) => ({
      ...draft,
      voice: { state: "converting", audio: null, conversion: "", error: "" },
    }));

    vi.stubGlobal("document", {
      getElementById: (id: string) => (id === "task-input" ? field2El : null),
    });

    await settle(key, { text: "Hello fallback", error: "" }, "chat-input");

    expect(field1Texts).toEqual(["Hello from field 1"]);
    expect(field2Texts).toEqual(["Hello fallback"]);

    unregister1();
    unregister2();
    vi.unstubAllGlobals();
  });
});
