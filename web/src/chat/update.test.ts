import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import type { ChatMessage } from "@/api/api.gen";
import type { Queued } from "@/lib/outbox";
import { FetchActionBrief, FetchPage, PostQueued, RemoveQueued } from "./commands";
import { Message } from "./message";
import { initialModel, retryDelays, type Model, type Waiting } from "./model";
import { update } from "./update";

function createTestModel(overrides: Partial<Model> = {}): Model {
  const base = initialModel({
    organization: "test-org",
    repository: "test-repo",
    workstream: 1428,
    agent: "implementer",
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
    time: "2026-11-04T08:00:00Z",
  };
}

function createChatMessage(id: number, text: string, browserId = ""): ChatMessage {
  return {
    id,
    organization: "test-org",
    repository: "test-repo",
    workstream: 1428,
    author: "Lead",
    text,
    time: "2026-11-04T08:00:00Z",
    browserId,
    action: null,
    cancelledAt: null,
    deliveredAt: null,
    failedAt: null,
    images: 0,
    inboxItems: [],
    question: null,
    stoppedAt: null,
  };
}

describe("chat update story tests", () => {
  describe("send queue logic", () => {
    it("queues an outgoing message and dispatches PostQueued if idle", () => {
      const item = createQueuedItem("q1", "First message");
      const base = createTestModel();

      story(
        update,
        given(base),
        message(Message.MessageQueued({ item })),
        model((m) => {
          expect((m.queue as Waiting[]).length).toBe(1);
          expect((m.queue as Waiting[])[0].id).toBe("q1");
        }),
        StoryCommand.expectExact(
          PostQueued({
            item,
            delay: retryDelays[0],
          }),
        ),
        StoryCommand.resolve(PostQueued, Message.SendAttemptSucceeded({ id: "q1" })),
        StoryCommand.resolve(
          FetchPage,
          Message.PageLoaded({
            page: {
              messages: [],
              older: false,
              writing: false,
              stoppable: false,
              pausedUntil: null,
            },
            isInitial: false,
          }),
        ),
        StoryCommand.expectNone(),
      );
    });

    it("does not dispatch PostQueued immediately if another message is in progress", () => {
      const first = createQueuedItem("q1", "First");
      const second = createQueuedItem("q2", "Second");
      const base = createTestModel({
        queue: [first as Waiting],
        attempt: { id: "q1", error: "network error", retryAt: 1700000005000 },
      });

      story(
        update,
        given(base),
        message(Message.MessageQueued({ item: second })),
        model((m) => {
          expect((m.queue as Waiting[]).length).toBe(2);
          expect((m.queue as Waiting[])[1].id).toBe("q2");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("marks sent item on SendAttemptSucceeded and triggers PostQueued for next item", () => {
      const first: Waiting = { ...createQueuedItem("q1", "First"), sent: false };
      const second: Waiting = { ...createQueuedItem("q2", "Second"), sent: false };
      const base = createTestModel({
        queue: [first, second],
      });

      story(
        update,
        given(base),
        message(Message.SendAttemptSucceeded({ id: "q1" })),
        model((m) => {
          const q = m.queue as Waiting[];
          expect(q[0].sent).toBe(true);
          expect(q[1].sent).toBeFalsy();
          expect(m.attempt).toBeNull();
          expect(m.failureCount).toBe(0);
        }),
        StoryCommand.expectExact(
          FetchPage({
            organization: "test-org",
            repository: "test-repo",
            workstream: 1428,
            isInitial: false,
          }),
          PostQueued({
            item: second,
            delay: retryDelays[0],
          }),
        ),
        StoryCommand.resolve(
          FetchPage,
          Message.PageLoaded({
            page: {
              messages: [],
              older: false,
              writing: false,
              stoppable: false,
              pausedUntil: null,
            },
            isInitial: false,
          }),
        ),
        StoryCommand.resolve(PostQueued, Message.SendAttemptSucceeded({ id: "q2" })),
        StoryCommand.resolve(
          FetchPage,
          Message.PageLoaded({
            page: {
              messages: [],
              older: false,
              writing: false,
              stoppable: false,
              pausedUntil: null,
            },
            isInitial: false,
          }),
        ),
        StoryCommand.expectNone(),
      );
    });

    it("removes queued items on PageLoaded when server confirms browserId", () => {
      const q1 = createQueuedItem("browser-1", "Hello");
      const q2 = createQueuedItem("browser-2", "World");
      const base = createTestModel({
        queue: [q1 as Waiting, q2 as Waiting],
      });

      const serverMsg = createChatMessage(101, "Hello", "browser-1");

      story(
        update,
        given(base),
        message(
          Message.PageLoaded({
            page: {
              messages: [serverMsg],
              older: false,
              writing: false,
              stoppable: false,
              pausedUntil: null,
            },
            isInitial: false,
          }),
        ),
        model((m) => {
          const q = m.queue as Waiting[];
          expect(q.length).toBe(1);
          expect(q[0].id).toBe("browser-2");
        }),
        StoryCommand.expectExact(RemoveQueued({ id: "browser-1" })),
        StoryCommand.resolve(RemoveQueued, Message.CompletedRemoveQueued({ id: "browser-1" })),
        StoryCommand.expectNone(),
      );
    });

    it("discards a queued message and removes it from storage", () => {
      const q1 = createQueuedItem("q1", "Delete me");
      const base = createTestModel({
        queue: [q1 as Waiting],
        attempt: { id: "q1", error: "failed", retryAt: 12345 },
      });

      story(
        update,
        given(base),
        message(Message.DiscardQueuedClicked({ id: "q1" })),
        model((m) => {
          expect((m.queue as Waiting[]).length).toBe(0);
          expect(m.attempt).toBeNull();
        }),
        StoryCommand.expectExact(RemoveQueued({ id: "q1" })),
        StoryCommand.resolve(RemoveQueued, Message.CompletedRemoveQueued({ id: "q1" })),
        StoryCommand.expectNone(),
      );
    });
  });

  describe("retry waits logic", () => {
    it("sets failure count and attempt on SendAttemptFailed", () => {
      const base = createTestModel({
        failureCount: 0,
      });

      const retryTime = 1700000000000 + retryDelays[0];

      story(
        update,
        given(base),
        message(
          Message.SendAttemptFailed({
            id: "q1",
            error: "Temporary network failure",
            retryAt: retryTime,
          }),
        ),
        model((m) => {
          expect(m.failureCount).toBe(1);
          expect(m.attempt).toEqual({
            id: "q1",
            error: "Temporary network failure",
            retryAt: retryTime,
          });
        }),
        StoryCommand.expectNone(),
      );
    });

    it("resets attempt on RetryNowClicked and immediately posts next unsent item with current delay", () => {
      const q1 = createQueuedItem("q1", "Retry message");
      const base = createTestModel({
        queue: [q1 as Waiting],
        failureCount: 2,
        attempt: { id: "q1", error: "error", retryAt: 1700000010000 },
      });

      story(
        update,
        given(base),
        message(Message.RetryNowClicked()),
        model((m) => {
          expect(m.attempt).toBeNull();
        }),
        StoryCommand.expectExact(
          PostQueued({
            item: q1,
            delay: retryDelays[2],
          }),
        ),
        StoryCommand.resolve(PostQueued, Message.SendAttemptSucceeded({ id: "q1" })),
        StoryCommand.resolve(
          FetchPage,
          Message.PageLoaded({
            page: {
              messages: [],
              older: false,
              writing: false,
              stoppable: false,
              pausedUntil: null,
            },
            isInitial: false,
          }),
        ),
        StoryCommand.expectNone(),
      );
    });
  });

  describe("merge of live message into list", () => {
    it("adds a new live message and sorts list by ID", () => {
      const m1 = createChatMessage(10, "First");
      const m3 = createChatMessage(30, "Third");
      const m2 = createChatMessage(20, "Second");

      const base = createTestModel({
        messages: [m1, m3],
      });

      story(
        update,
        given(base),
        message(Message.ReceivedLiveMessage({ message: m2 })),
        model((m) => {
          const list = m.messages as ChatMessage[];
          expect(list.length).toBe(3);
          expect(list.map((x) => x.id)).toEqual([10, 20, 30]);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("updates existing message if new text is longer (live streaming progress)", () => {
      const initial = createChatMessage(10, "Thinking");
      const longer = createChatMessage(10, "Thinking about the task...");

      const base = createTestModel({
        messages: [initial],
      });

      story(
        update,
        given(base),
        message(Message.ReceivedLiveMessage({ message: longer })),
        model((m) => {
          const list = m.messages as ChatMessage[];
          expect(list.length).toBe(1);
          expect(list[0].text).toBe("Thinking about the task...");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("ignores live message if text is shorter than existing", () => {
      const longer = createChatMessage(10, "Complete response text");
      const shorter = createChatMessage(10, "Partial");

      const base = createTestModel({
        messages: [longer],
      });

      story(
        update,
        given(base),
        message(Message.ReceivedLiveMessage({ message: shorter })),
        model((m) => {
          const list = m.messages as ChatMessage[];
          expect(list.length).toBe(1);
          expect(list[0].text).toBe("Complete response text");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("drops live message if older than oldest loaded message to avoid gaps", () => {
      const m20 = createChatMessage(20, "Page top message");
      const m5 = createChatMessage(5, "Old message before first loaded");

      const base = createTestModel({
        messages: [m20],
      });

      story(
        update,
        given(base),
        message(Message.ReceivedLiveMessage({ message: m5 })),
        model((m) => {
          const list = m.messages as ChatMessage[];
          expect(list.length).toBe(1);
          expect(list[0].id).toBe(20);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("removes queued item when live message arrives with matching browserId", () => {
      const queued = createQueuedItem("client-123", "My message");
      const liveMsg = createChatMessage(50, "My message", "client-123");

      const base = createTestModel({
        messages: [],
        queue: [queued as Waiting],
      });

      story(
        update,
        given(base),
        message(Message.ReceivedLiveMessage({ message: liveMsg })),
        model((m) => {
          expect((m.queue as Waiting[]).length).toBe(0);
          expect((m.messages as ChatMessage[]).length).toBe(1);
        }),
        StoryCommand.expectExact(RemoveQueued({ id: "client-123" })),
        StoryCommand.resolve(RemoveQueued, Message.CompletedRemoveQueued({ id: "client-123" })),
        StoryCommand.expectNone(),
      );
    });
  });

  describe("brief diff and answer panel logic", () => {
    it("fetches action brief when opening answer panel for pending edit_brief", () => {
      const base = createTestModel();
      const item = {
        id: 101,
        kind: "approval" as const,
        action: {
          id: 42,
          tool: "edit_brief",
          state: "pending",
          role: "Lead",
          sender: null,
          time: "2026-11-04T08:00:00Z",
          arguments: [{ name: "brief", value: "Updated brief text", labels: [] }],
        },
      } as any;

      story(
        update,
        given(base),
        message(Message.OpenAnswerPanel({ item })),
        model((m) => {
          expect(m.activePanel?.type).toBe("answer");
          expect(m.showRemovedText).toBe(false);
          expect(m.briefActionBrief).toBe("");
        }),
        StoryCommand.expectExact(FetchActionBrief({ actionId: 42 })),
        StoryCommand.resolve(
          FetchActionBrief,
          Message.ActionBriefFetched({ brief: "Old brief content" }),
        ),
        StoryCommand.expectNone(),
      );
    });

    it("toggles showRemovedText on ToggleRemovedText message", () => {
      const base = createTestModel({ showRemovedText: false });

      story(
        update,
        given(base),
        message(Message.ToggleRemovedText()),
        model((m) => {
          expect(m.showRemovedText).toBe(true);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("stores fetched brief on ActionBriefFetched message", () => {
      const base = createTestModel({ briefActionBrief: "" });

      story(
        update,
        given(base),
        message(Message.ActionBriefFetched({ brief: "Fetched brief from server" })),
        model((m) => {
          expect(m.briefActionBrief).toBe("Fetched brief from server");
        }),
        StoryCommand.expectNone(),
      );
    });
  });
});
