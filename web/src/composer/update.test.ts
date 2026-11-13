import { describe, expect, it, vi } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import {
  EnqueueMessage,
  RemoveVoice,
  ResendVoice,
  SaveDraftImages,
  SaveDraftInboxItems,
  SaveDraftText,
  StopVoice,
  ToggleVoice,
} from "./commands";
import { Message } from "./message";
import { initialModel, type Model } from "./model";
import type { Draft, Queued } from "@/chat/schemas";
import { update } from "./update";

// The voice library reads the document of the browser when it loads.
vi.mock("@/lib/voice", () => ({ resend: vi.fn(), stopVoice: vi.fn(), toggleVoice: vi.fn() }));

type Commands = ReturnType<typeof update>["commands"];

const described = (commands: Commands) =>
  (commands ?? []).map(({ name, args }) => ({ name, args }));

const createDraft = (text = "", overrides: Partial<Draft> = {}): Draft => ({
  text,
  images: [],
  inboxItems: [],
  voice: null,
  ...overrides,
});

function createTestModel(overrides: Partial<Model> = {}): Model {
  const base = initialModel({
    draftKey: "chatDraft:test-org/test-repo/1428",
    instance: "chat",
    label: "Message to the Lead",
    placeholder: "Write to the Lead",
    organization: "test-org",
    repository: "test-repo",
    workstream: 1428,
  });
  return {
    ...base,
    ...overrides,
  };
}

function createQueuedItem(id: string, text: string): Queued {
  return {
    id,
    organization: "test-org",
    repository: "test-repo",
    workstream: 1428,
    text,
    images: [],
    inboxItems: [],
    voice: false,
    time: "2026-11-04T08:00:00Z",
  };
}

describe("composer update story tests", () => {
  it("takes the draft of the chat from the store", () => {
    const image = new File(["x"], "a.png", { type: "image/png" });
    const draft = createDraft("Hello", {
      images: [image],
      voice: { state: "ready", audio: null, conversion: "", error: "" },
    });

    const result = update(createTestModel(), Message.ReceivedDraft({ draft }));
    expect(result.model.draftLoaded).toBe(true);
    expect(result.model.draftText).toBe("Hello");
    expect(result.model.draftImages).toEqual([image]);
    expect(result.model.draftVoice?.state).toBe("ready");
  });

  it("writes each change of the draft of the chat to the store under the key of the chat", () => {
    const key = "chatDraft:test-org/test-repo/1428";
    const image = new File(["x"], "a.png", { type: "image/png" });
    const base = createTestModel({
      draftImages: [image],
      draftInboxItems: [{ id: 7, reference: "#7", title: "Question", reason: "", link: "" }],
    });

    const text = update(base, Message.DraftTextChanged({ text: "Hi" }));
    expect(text.model.draftText).toBe("Hi");
    expect(described(text.commands)).toEqual(described([SaveDraftText({ key, text: "Hi" })]));
    const images = update(base, Message.DraftImageRemoved({ position: 0 }));
    expect(described(images.commands)).toEqual(described([SaveDraftImages({ key, images: [] })]));
    const items = update(base, Message.DraftInboxItemRemoved({ id: 7 }));
    expect(described(items.commands)).toEqual(
      described([SaveDraftInboxItems({ key, inboxItems: [] })]),
    );
  });

  it("sends the draft of the chat with the voice mark when the card is ready, and clears the draft", () => {
    const base = createTestModel({
      draftText: "Show the prices.",
      draftVoice: { state: "ready", audio: null, conversion: "", error: "" },
      sendError: "Old error.",
    });

    story(
      update,
      given(base),
      message(Message.SendSubmitted()),
      model((m) => {
        expect(m.draftText).toBe("");
        expect(m.draftVoice).toBeNull();
        expect(m.sendError).toBe("");
      }),
      StoryCommand.expectExact(
        EnqueueMessage({
          organization: "test-org",
          repository: "test-repo",
          workstream: 1428,
          key: "chatDraft:test-org/test-repo/1428",
          text: "Show the prices.",
          images: [],
          inboxItems: [],
          voice: true,
        }),
      ),
      StoryCommand.resolve(
        EnqueueMessage,
        Message.MessageQueued({ item: createQueuedItem("q1", "Show the prices.") }),
      ),
      StoryCommand.expectNone(),
    );
  });

  it("sends a second tap on Send in the chat one time, because the first tap clears the draft", () => {
    const base = createTestModel({ draftText: "Once." });

    const first = update(base, Message.SendSubmitted());
    expect(first.commands?.map((command) => command.name)).toEqual(["EnqueueMessage"]);
    const second = update(first.model, Message.SendSubmitted());
    expect(second.commands ?? []).toEqual([]);
  });

  it("sends nothing from the chat while the voice card converts or has failed", () => {
    for (const state of ["converting", "failed"] as const) {
      const base = createTestModel({
        draftText: "Text.",
        draftVoice: { state, audio: null, conversion: "", error: "" },
      });
      const result = update(base, Message.SendSubmitted());
      expect(result.model).toBe(base);
      expect(result.commands ?? []).toEqual([]);
    }
  });

  it("stops the recording and sends nothing when Send is tapped during a recording", () => {
    const voice = {
      recording: true,
      key: "x",
      startedAt: 1,
      live: "",
      notice: { key: "", text: "" },
    };
    const chat = createTestModel({ draftText: "Text.", voice });
    const chatResult = update(chat, Message.SendSubmitted());
    expect(chatResult.model).toBe(chat);
    expect(described(chatResult.commands)).toEqual(described([StopVoice()]));
  });

  it("starts the voice of the composer field", () => {
    const base = createTestModel();

    expect(described(update(base, Message.VoiceButtonClicked()).commands)).toEqual(
      described([ToggleVoice({ key: "chatDraft:test-org/test-repo/1428", fieldId: "chat-input" })]),
    );
  });

  it("sends again and removes the voice card of the composer field", () => {
    const base = createTestModel();

    expect(described(update(base, Message.VoiceResendClicked()).commands)).toEqual(
      described([ResendVoice({ key: "chatDraft:test-org/test-repo/1428", fieldId: "chat-input" })]),
    );
    expect(described(update(base, Message.VoiceRemoveClicked()).commands)).toEqual(
      described([RemoveVoice({ key: "chatDraft:test-org/test-repo/1428" })]),
    );
  });

  it("shows the error of a refused message", () => {
    const result = update(createTestModel(), Message.MessageRefused({ error: "No room." }));
    expect(result.model.sendError).toBe("No room.");
  });

  it("takes the voice state and the time from the Subscriptions", () => {
    const voice = {
      recording: true,
      key: "chatDraft:test-org/test-repo/1428",
      startedAt: 1000,
      live: "Show",
      notice: { key: "", text: "" },
    };

    const changed = update(createTestModel(), Message.VoiceChanged({ voice }));
    expect(changed.model.voice).toEqual(voice);
    expect(update(changed.model, Message.VoiceTicked({ now: 4000 })).model.now).toBe(4000);
  });
});
