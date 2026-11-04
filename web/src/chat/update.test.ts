import { describe, expect, it } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import type { ChatMessage } from "@/api/api.gen";
import type { Queued } from "@/lib/outbox";
import {
  ApproveAction,
  FetchActionBrief,
  FetchPage,
  DoneInboxItem,
  LoadAnswerDraft,
  PostQueued,
  ProbeVoice,
  RemoveQueued,
  RunTrayAction,
  StopVoice,
  WaitToRetry,
} from "./commands";
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
            known: null,
            sent: "q1",
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

    const page = {
      messages: [createChatMessage(101, "Hello")],
      older: false,
      writing: false,
      stoppable: false,
      pausedUntil: null,
    };

    it("posts the next item when the server stored the item of a failed attempt", () => {
      const a = createQueuedItem("a", "First") as Waiting;
      const b = createQueuedItem("b", "Second") as Waiting;
      const base = createTestModel({
        queue: [a, b],
        attempt: { id: "a", error: "lost", retryAt: 1 },
        failureCount: 3,
      });

      story(
        update,
        given(base),
        message(
          Message.PageLoaded({
            page: { ...page, messages: [createChatMessage(101, "First", "a")] },
            isInitial: false,
          }),
        ),
        model((m) => {
          expect(m.attempt).toBeNull();
          expect(m.failureCount).toBe(0);
          expect((m.queue as Waiting[]).map((item) => item.id)).toEqual(["b"]);
        }),
        StoryCommand.expectExact(
          RemoveQueued({ id: "a" }),
          PostQueued({ item: b, delay: retryDelays[0] }),
        ),
        StoryCommand.resolve(RemoveQueued, Message.CompletedRemoveQueued({ id: "a" })),
        StoryCommand.resolve(PostQueued, Message.SendAttemptSucceeded({ id: "b" })),
        StoryCommand.resolve(FetchPage, Message.PageFailed({ error: "x", sent: "b" })),
        StoryCommand.expectNone(),
      );
    });

    it("ignores the wait of a stored item, and posts the next item after a live message", () => {
      const a = createQueuedItem("a", "First") as Waiting;
      const b = createQueuedItem("b", "Second") as Waiting;
      const base = createTestModel({
        queue: [a, b],
        attempt: { id: "a", error: "lost", retryAt: 1 },
        failureCount: 1,
        retrySequence: 2,
      });

      story(
        update,
        given(base),
        message(Message.ReceivedLiveMessage({ message: createChatMessage(101, "First", "a") })),
        model((m) => {
          expect(m.attempt).toBeNull();
          expect(m.retrySequence).toBe(3);
        }),
        StoryCommand.expectExact(
          RemoveQueued({ id: "a" }),
          PostQueued({ item: b, delay: retryDelays[0] }),
        ),
        StoryCommand.resolve(RemoveQueued, Message.CompletedRemoveQueued({ id: "a" })),
        StoryCommand.resolve(PostQueued, Message.SendAttemptSucceeded({ id: "b" })),
        StoryCommand.resolve(FetchPage, Message.PageFailed({ error: "x", sent: "b" })),
        message(Message.RetryDue({ id: "a", sequence: 2 })),
        StoryCommand.expectNone(),
      );
    });

    it("removes a sent item at the end of its own load, also when the server gives no browserId", () => {
      const sent = { ...createQueuedItem("browser-1", "Hello"), sent: true } as Waiting;
      const waiting = createQueuedItem("browser-2", "World") as Waiting;
      const base = createTestModel({ queue: [sent, waiting] });

      story(
        update,
        given(base),
        message(Message.PageLoaded({ page, isInitial: false, sent: "browser-1" })),
        model((m) => {
          expect((m.queue as Waiting[]).map((item) => item.id)).toEqual(["browser-2"]);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("keeps a sent item while the load of another send ends", () => {
      const first = { ...createQueuedItem("browser-1", "Hello"), sent: true } as Waiting;
      const second = { ...createQueuedItem("browser-2", "World"), sent: true } as Waiting;
      const base = createTestModel({ queue: [first, second] });

      story(
        update,
        given(base),
        message(Message.PageLoaded({ page, isInitial: false, sent: "browser-1" })),
        model((m) => {
          expect((m.queue as Waiting[]).map((item) => item.id)).toEqual(["browser-2"]);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("keeps a sent item when a live message comes before the end of its load", () => {
      const sent = { ...createQueuedItem("browser-1", "Hello"), sent: true } as Waiting;
      const base = createTestModel({ queue: [sent] });

      story(
        update,
        given(base),
        message(Message.ReceivedLiveMessage({ message: createChatMessage(102, "Reply") })),
        model((m) => {
          expect((m.queue as Waiting[]).map((item) => item.id)).toEqual(["browser-1"]);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("removes a sent item when its own load fails", () => {
      const sent = { ...createQueuedItem("browser-1", "Hello"), sent: true } as Waiting;
      const base = createTestModel({ queue: [sent] });

      story(
        update,
        given(base),
        message(Message.PageFailed({ error: "down", sent: "browser-1" })),
        model((m) => {
          expect(m.queue).toEqual([]);
        }),
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
    it("sets failure count and attempt on SendAttemptFailed, and waits for the first delay", () => {
      const q1 = createQueuedItem("q1", "Retry message");
      const base = createTestModel({ queue: [q1 as Waiting], failureCount: 0 });
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
        StoryCommand.expectExact(WaitToRetry({ id: "q1", delay: retryDelays[0], sequence: 0 })),
        StoryCommand.resolve(WaitToRetry, Message.RetryDue({ id: "q1", sequence: 0 })),
        model((m) => {
          expect(m.attempt).toBeNull();
        }),
        StoryCommand.expectExact(PostQueued({ item: q1, delay: retryDelays[1] })),
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

    it("grows the wait to the last delay and keeps it", () => {
      const q1 = createQueuedItem("q1", "Retry message");
      const base = createTestModel({
        queue: [q1 as Waiting],
        failureCount: retryDelays.length + 2,
      });

      story(
        update,
        given(base),
        message(Message.SendAttemptFailed({ id: "q1", error: "down", retryAt: 1 })),
        StoryCommand.expectExact(
          WaitToRetry({ id: "q1", delay: retryDelays[retryDelays.length - 1], sequence: 0 }),
        ),
        StoryCommand.resolve(WaitToRetry, Message.RetryDue({ id: "q1", sequence: 0 })),
        StoryCommand.expectExact(
          PostQueued({ item: q1, delay: retryDelays[retryDelays.length - 1] }),
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

    it("ignores a wait that ended after Retry now", () => {
      const q1 = createQueuedItem("q1", "Retry message");
      const base = createTestModel({
        queue: [q1 as Waiting],
        failureCount: 1,
        attempt: { id: "q1", error: "down", retryAt: 1 },
      });

      story(
        update,
        given(base),
        message(Message.RetryNowClicked()),
        StoryCommand.expectExact(PostQueued({ item: q1, delay: retryDelays[1] })),
        StoryCommand.resolve(
          PostQueued,
          Message.SendAttemptFailed({ id: "q1", error: "down", retryAt: 2 }),
        ),
        StoryCommand.expectExact(WaitToRetry({ id: "q1", delay: retryDelays[1], sequence: 1 })),
        StoryCommand.resolve(WaitToRetry, Message.RetryDue({ id: "q1", sequence: 0 })),
        model((m) => {
          expect(m.attempt).toEqual({ id: "q1", error: "down", retryAt: 2 });
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
        StoryCommand.expectExact(FetchActionBrief({ actionId: 42 }), LoadAnswerDraft({ id: 101 })),
        StoryCommand.resolve(
          FetchActionBrief,
          Message.ActionBriefFetched({ brief: "Old brief content" }),
        ),
        StoryCommand.resolve(LoadAnswerDraft, Message.AnswerDraftLoaded({ text: "" })),
        model((m) => {
          expect(m.answerLoaded).toBe(true);
        }),
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

  describe("tray and panel", () => {
    const question = { id: 7, kind: "question", questions: [], action: null, todo: null } as any;
    const approval = {
      id: 8,
      kind: "approval",
      title: "Approve it",
      reason: "",
      issue: 3,
      repository: "test-repo",
      questions: [],
      todo: null,
      action: {
        id: 42,
        tool: "message_lead",
        state: "pending",
        role: "Coordinator",
        arguments: [],
      },
    } as any;
    const todo = {
      id: 9,
      kind: "todo",
      questions: [],
      action: null,
      issue: 5,
      repository: "test-repo",
      todo: { name: "plan", mandatory: true },
    } as any;

    it("keeps the list and opens the panel again with the error after a failed decision", () => {
      const messages = [createChatMessage(1, "Hello")];
      const base = createTestModel({
        messages,
        trayItems: [question],
        activePanel: null,
        closedPanel: { type: "answer", item: approval },
        decisionSubmitting: true,
      });

      story(
        update,
        given(base),
        message(Message.ActionFailed({ id: 8, error: "The server failed." })),
        model((m) => {
          expect(m.messages).toBe(messages);
          expect(m.trayItems).toEqual([question]);
          expect(m.activePanel?.item).toBe(approval);
          expect(m.closedPanel).toBeNull();
          expect(m.answerError).toBe("The server failed.");
          expect(m.decisionSubmitting).toBe(false);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("sends a todo with an empty note", () => {
      const base = createTestModel({
        activePanel: { type: "answer", item: todo },
        answerLoaded: true,
      });

      story(
        update,
        given(base),
        message(Message.AnswerSubmitted()),
        model((m) => {
          expect(m.probingSend).toBe(true);
        }),
        StoryCommand.expectExact(ProbeVoice({ region: null, draftText: "", target: "answer" })),
        StoryCommand.resolve(ProbeVoice, Message.VoiceProbed({ live: false, target: "answer" })),
        StoryCommand.expectExact(
          StopVoice(),
          DoneInboxItem({ id: 9, repository: "test-repo", issue: 5, name: "plan", note: "" }),
        ),
        StoryCommand.resolve(StopVoice, Message.CompletedStopVoice()),
        StoryCommand.resolve(DoneInboxItem, Message.AnswerCompleted({ id: 9, result: {} })),
        StoryCommand.expectNone(),
      );
    });

    it("keeps the pending Stop button after a successful request", () => {
      const base = createTestModel({ stoppable: true, stopping: true });

      story(
        update,
        given(base),
        message(Message.StopChatCompleted({ error: null })),
        model((m) => {
          expect(m.stopping).toBe(true);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("ends the pending Stop button after a failed request", () => {
      const base = createTestModel({ stoppable: true, stopping: true });

      story(
        update,
        given(base),
        message(Message.StopChatCompleted({ error: "Stop failed" })),
        model((m) => {
          expect(m.stopping).toBe(false);
          expect(m.sendError).toBe("Stop failed");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("keeps the Details panel of a card when the host sends the tray again", () => {
      const answered = { ...question, dismissedAt: "2026-11-04T08:00:00Z" };
      const base = createTestModel({
        activePanel: { type: "details", item: answered },
        trayItems: [],
      });

      story(
        update,
        given(base),
        message(Message.ReceivedTrayItems({ items: [] })),
        model((m) => {
          expect(m.activePanel?.item).toBe(answered);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("closes the Details panel of a tray row with the row", () => {
      const base = createTestModel({
        activePanel: { type: "details", item: question, fromTray: true },
        trayItems: [question],
      });

      story(
        update,
        given(base),
        message(Message.ReceivedTrayItems({ items: [] })),
        model((m) => {
          expect(m.activePanel).toBeNull();
        }),
        StoryCommand.expectNone(),
      );
    });

    it("runs Merge, shows the pending state, and shows a refusal", () => {
      const base = createTestModel({ trayItems: [question] });

      story(
        update,
        given(base),
        message(Message.TrayActionClicked({ item: question, kind: "merge" })),
        model((m) => {
          expect(m.trayRunning).toEqual({ 7: "merge" });
        }),
        StoryCommand.expectExact(RunTrayAction({ item: question, kind: "merge" })),
        StoryCommand.resolve(
          RunTrayAction,
          Message.TrayActionDone({ id: 7, kind: "merge", error: "A check fails.", merging: false }),
        ),
        model((m) => {
          expect(m.trayRunning).toEqual({});
          expect(m.trayRefusals).toEqual({ 7: "A check fails." });
        }),
        StoryCommand.expectNone(),
      );
    });

    it("keeps Send again pending after a success, and ends it after a failure", () => {
      const base = createTestModel({ trayItems: [question] });

      story(
        update,
        given(base),
        message(Message.TrayActionClicked({ item: question, kind: "send_again" })),
        StoryCommand.expectExact(RunTrayAction({ item: question, kind: "send_again" })),
        StoryCommand.resolve(
          RunTrayAction,
          Message.TrayActionDone({ id: 7, kind: "action", error: "", merging: false }),
        ),
        model((m) => {
          expect(m.trayRunning).toEqual({ 7: "action" });
        }),
        message(Message.TrayActionDone({ id: 7, kind: "action", error: "No.", merging: false })),
        model((m) => {
          expect(m.trayRunning).toEqual({});
          expect(m.trayError).toBe("No.");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("keeps the panel of another item when a request ends with a success", () => {
      const other = { id: 11, kind: "question", questions: [], action: null, todo: null } as any;
      const base = createTestModel({
        activePanel: { type: "answer", item: other },
        closedPanel: null,
      });

      story(
        update,
        given(base),
        message(Message.AnswerCompleted({ id: 7, result: {} })),
        model((m) => {
          expect(m.activePanel?.item).toBe(other);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("keeps the panel of another item when a request ends with an error, and shows the error only for its item", () => {
      const other = { id: 11, kind: "question", questions: [], action: null, todo: null } as any;
      const base = createTestModel({
        activePanel: { type: "answer", item: other },
        closedPanel: { type: "answer", item: question },
      });

      story(
        update,
        given(base),
        message(Message.AnswerCompleted({ id: 7, result: { error: "No." } })),
        model((m) => {
          expect(m.activePanel?.item).toBe(other);
          expect(m.closedPanel?.item).toBe(question);
          expect(m.answerError).toBe("No.");
          expect(m.answerErrorId).toBe(7);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("clears the error at a new decision", () => {
      const base = createTestModel({
        activePanel: { type: "answer", item: approval },
        answerError: "Old error.",
        answerErrorId: 7,
      });

      story(
        update,
        given(base),
        message(Message.ActionDecided({ decision: "approve", text: "" })),
        model((m) => {
          expect(m.answerError).toBe("");
          expect(m.answerErrorId).toBe(0);
        }),
        StoryCommand.expectExact(
          StopVoice(),
          ApproveAction({
            actionId: 42,
            inboxItemId: 8,
            repository: "test-repo",
            issue: 3,
            title: "Approve it",
            text: "",
          }),
        ),
        StoryCommand.resolve(StopVoice, Message.CompletedStopVoice()),
        StoryCommand.resolve(ApproveAction, Message.ActionFailed({ id: 8, error: "x" })),
        StoryCommand.expectNone(),
      );
    });

    it("drops the local merge answer when the row comes again with other data", () => {
      const merging = { ...question, merging: true };
      const base = createTestModel({
        trayItems: [question],
        trayMerging: { 7: true },
        trayRefusals: {},
      });

      story(
        update,
        given(base),
        message(Message.ReceivedTrayItems({ items: [{ ...question }] })),
        model((m) => {
          expect(m.trayMerging).toEqual({ 7: true });
        }),
        message(
          Message.ReceivedTrayItems({
            items: [{ ...merging, merging: false, mergeRefusal: "A check fails." }],
          }),
        ),
        model((m) => {
          expect(m.trayMerging).toEqual({});
        }),
        StoryCommand.expectNone(),
      );
    });
  });
});
