import { Effect } from "effect";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { emptyDraft, type Draft } from "@/lib/draft";
import { AnswerInboxItem, ApproveAction, DoneInboxItem, RejectAction } from "./commands";

const mocks = vi.hoisted(() => ({
  answerInboxItem: vi.fn(),
  doneInboxItem: vi.fn(),
  approveAction: vi.fn(),
  rejectAction: vi.fn(),
  hideInboxItem: vi.fn(),
  restoreInboxItem: vi.fn(),
  showNotice: vi.fn(),
  updates: [] as Array<(draft: Draft) => Draft>,
}));

vi.mock("@/api/api.gen", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/api.gen")>()),
  answerInboxItem: mocks.answerInboxItem,
  doneInboxItem: mocks.doneInboxItem,
  approveAction: mocks.approveAction,
  rejectAction: mocks.rejectAction,
}));
vi.mock("@/lib/draft", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/draft")>()),
  updateDraft: (_key: string, update: (draft: Draft) => Draft) => {
    mocks.updates.push(update);
    return Promise.resolve();
  },
}));
vi.mock("@/lib/inbox", () => ({
  hideInboxItem: mocks.hideInboxItem,
  restoreInboxItem: mocks.restoreInboxItem,
}));
vi.mock("@/lib/notice", () => ({
  issueRef: () => "",
  showNotice: mocks.showNotice,
}));
// The voice library reads the document of the browser when it loads.
vi.mock("@/lib/voice", () => ({ resend: vi.fn(), stopVoice: vi.fn(), toggleVoice: vi.fn() }));

const image = new File(["x"], "a.png", { type: "image/png" });
const inboxItem = { id: 3, reference: "#3", title: "Plan", reason: "Why", link: "https://x/3" };
const selection = {
  id: "s1",
  source: "message" as const,
  text: "Part",
  context: "A part of the message.",
  reference: "",
  link: "",
  part: "",
  messageId: 5,
  messageAuthor: "Lead",
};
const parts = {
  images: [image],
  inboxItems: [inboxItem],
  selections: [selection],
  voice: true,
  voiceSeconds: 12,
};
const body = {
  images: [image],
  inboxItems: [{ reference: "#3", title: "Plan", reason: "Why", link: "https://x/3" }],
  selections: [
    {
      source: "message",
      text: "Part",
      context: "A part of the message.",
      reference: "",
      link: "",
      part: "",
      messageId: 5,
      messageAuthor: "Lead",
    },
  ],
  voice: true,
  voiceSeconds: 12,
};
const target = { id: 7, repository: "owner/shop", issue: 45, notice: "Done" };
const refused = { status: 500, data: { error: "The server failed." } };

const run = (command: { effect: Effect.Effect<unknown> }) => Effect.runPromise(command.effect);

beforeEach(() => {
  vi.clearAllMocks();
  mocks.updates.length = 0;
});

describe("the requests of the answer panel", () => {
  it("gives the images, the Inbox items, the selections and the voice mark to the answer", async () => {
    mocks.answerInboxItem.mockResolvedValue({ status: 200, data: {} });

    await run(AnswerInboxItem({ ...target, answer: "Yes", ...parts }));

    expect(mocks.answerInboxItem).toHaveBeenCalledWith(7, { text: "Yes", ...body });
  });

  it("gives the same parts to the note of a done item", async () => {
    mocks.doneInboxItem.mockResolvedValue({ status: 200, data: {} });

    await run(DoneInboxItem({ ...target, note: "Done it", ...parts }));

    expect(mocks.doneInboxItem).toHaveBeenCalledWith(7, { note: "Done it", ...body });
  });

  it("gives the same parts to the message of an approval and of a rejection", async () => {
    const decision = {
      actionId: 42,
      inboxItemId: 7,
      repository: "owner/shop",
      issue: 45,
      title: "Approve it",
      text: "Go",
      ...parts,
    };
    mocks.approveAction.mockResolvedValue(refused);
    mocks.rejectAction.mockResolvedValue(refused);

    await run(ApproveAction(decision));
    await run(RejectAction(decision));

    expect(mocks.approveAction).toHaveBeenCalledWith(42, { message: "Go", ...body });
    expect(mocks.rejectAction).toHaveBeenCalledWith(42, { message: "Go", ...body });
  });

  it("puts the text and all parts back in the draft when the answer fails", async () => {
    mocks.answerInboxItem.mockResolvedValue(refused);

    await run(AnswerInboxItem({ ...target, answer: "Yes", ...parts }));

    const [clear, restore] = mocks.updates;
    const typedMeanwhile = { ...emptyDraft, text: "More." };
    expect(clear(typedMeanwhile)).toMatchObject({ text: "", images: [], inboxItems: [] });
    const restored = restore({ ...emptyDraft });
    expect(restored.text).toBe("Yes");
    expect(restored.images).toEqual([image]);
    expect(restored.inboxItems).toEqual([inboxItem]);
    expect(restored.selections).toEqual([selection]);
    expect(restored.voice?.state).toBe("ready");
    expect(restored.voice?.seconds).toBe(12);
    expect(mocks.restoreInboxItem).toHaveBeenCalledWith(7);
  });

  it("puts all parts of a done item back in the draft when the request fails", async () => {
    mocks.doneInboxItem.mockResolvedValue(refused);

    await run(DoneInboxItem({ ...target, note: "Done it", ...parts }));

    const restored = mocks.updates[1]({ ...emptyDraft });
    expect(restored.text).toBe("Done it");
    expect(restored.images).toEqual([image]);
    expect(restored.inboxItems).toEqual([inboxItem]);
    expect(restored.selections).toEqual([selection]);
    expect(restored.voice?.seconds).toBe(12);
  });

  it("keeps the draft of a decision that the server refuses", async () => {
    mocks.approveAction.mockResolvedValue(refused);

    await run(
      ApproveAction({
        actionId: 42,
        inboxItemId: 7,
        repository: "owner/shop",
        issue: 45,
        title: "Approve it",
        text: "Go",
        ...parts,
      }),
    );

    expect(mocks.updates).toEqual([]);
  });
});
