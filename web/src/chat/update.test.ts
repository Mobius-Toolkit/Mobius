import { describe, expect, it, vi } from "vitest";
import { Command as StoryCommand, given, message, model, story } from "foldkit/story";
import {
  AnswerInboxItem,
  ApproveAction,
  FetchActionBrief,
  FetchPage,
  DoneInboxItem,
  EnqueueMessage,
  PostQueued,
  RemoveQueued,
  RemoveVoice,
  ResendVoice,
  RunTrayAction,
  SaveDraftImages,
  SaveDraftInboxItems,
  SaveDraftText,
  SaveTrayState,
  ShowBlockingTodo,
  StopChat,
  StopVoice,
  ToggleVoice,
  WaitToRetry,
} from "./commands";
import { Message } from "./message";
import { initialModel, retryDelays, type Model, type Waiting } from "./model";
import type { ChatMessage, Draft, InboxItem, PendingAction, Queued } from "./schemas";
import { update } from "./update";

// The voice library reads the document of the browser when it loads.
vi.mock("@/lib/voice", () => ({ resend: vi.fn(), stopVoice: vi.fn(), toggleVoice: vi.fn() }));

type Commands = ReturnType<typeof update>["commands"];

// A Command holds a function, so a test compares the name and the arguments.
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
    voice: false,
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
    voice: false,
  };
}

function createPendingAction(overrides: Partial<PendingAction> = {}): PendingAction {
  return {
    id: 42,
    tool: "message_lead",
    state: "pending",
    role: "coordinator",
    sender: null,
    time: "2026-11-04T08:00:00Z",
    arguments: [],
    decidedAt: null,
    error: "",
    message: "",
    oldBrief: "",
    result: "",
    ...overrides,
  };
}

function createInboxItem(overrides: Partial<InboxItem> = {}): InboxItem {
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
    repository: "test-repo",
    text: "",
    time: "2026-11-04T08:00:00Z",
    title: "",
    todo: null,
    withdrawn: false,
    workstream: 1428,
    workstreamRepository: "test-repo",
    ...overrides,
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
          expect(m.queue.length).toBe(1);
          expect(m.queue[0].id).toBe("q1");
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
              harness: "claude-code",
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
        queue: [first],
        attempt: { id: "q1", error: "network error", retryAt: 1700000005000 },
      });

      story(
        update,
        given(base),
        message(Message.MessageQueued({ item: second })),
        model((m) => {
          expect(m.queue.length).toBe(2);
          expect(m.queue[1].id).toBe("q2");
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
          const q = m.queue;
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
              harness: "claude-code",
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
              harness: "claude-code",
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
        queue: [q1, q2],
      });

      const serverMsg = createChatMessage(101, "Hello", "browser-1");

      story(
        update,
        given(base),
        message(
          Message.PageLoaded({
            page: {
              harness: "claude-code",
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
          const q = m.queue;
          expect(q.length).toBe(1);
          expect(q[0].id).toBe("browser-2");
        }),
        StoryCommand.expectExact(RemoveQueued({ id: "browser-1" })),
        StoryCommand.resolve(RemoveQueued, Message.CompletedRemoveQueued({ id: "browser-1" })),
        StoryCommand.expectNone(),
      );
    });

    const page = {
      harness: "claude-code",
      messages: [createChatMessage(101, "Hello")],
      older: false,
      writing: false,
      stoppable: false,
      pausedUntil: null,
    };

    it("posts the next item when the server stored the item of a failed attempt", () => {
      const a = createQueuedItem("a", "First");
      const b = createQueuedItem("b", "Second");
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
          expect(m.queue.map((item) => item.id)).toEqual(["b"]);
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
      const a = createQueuedItem("a", "First");
      const b = createQueuedItem("b", "Second");
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

    it("posts the next item when the request of a stored item fails after the item left the queue", () => {
      const b = createQueuedItem("b", "Second");
      const base = createTestModel({ queue: [b] });

      story(
        update,
        given(base),
        message(Message.SendAttemptFailed({ id: "a", error: "lost", retryAt: 1 })),
        model((m) => {
          expect(m.attempt).toBeNull();
          expect(m.failureCount).toBe(0);
        }),
        StoryCommand.expectExact(PostQueued({ item: b, delay: retryDelays[0] })),
        StoryCommand.resolve(PostQueued, Message.SendAttemptSucceeded({ id: "b" })),
        StoryCommand.resolve(FetchPage, Message.PageFailed({ error: "x", sent: "b" })),
        StoryCommand.expectNone(),
      );
    });

    it("removes a sent item at the end of its own load, also when the server gives no browserId", () => {
      const sent = { ...createQueuedItem("browser-1", "Hello"), sent: true };
      const waiting = createQueuedItem("browser-2", "World");
      const base = createTestModel({ queue: [sent, waiting] });

      story(
        update,
        given(base),
        message(Message.PageLoaded({ page, isInitial: false, sent: "browser-1" })),
        model((m) => {
          expect(m.queue.map((item) => item.id)).toEqual(["browser-2"]);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("keeps a sent item while the load of another send ends", () => {
      const first = { ...createQueuedItem("browser-1", "Hello"), sent: true };
      const second = { ...createQueuedItem("browser-2", "World"), sent: true };
      const base = createTestModel({ queue: [first, second] });

      story(
        update,
        given(base),
        message(Message.PageLoaded({ page, isInitial: false, sent: "browser-1" })),
        model((m) => {
          expect(m.queue.map((item) => item.id)).toEqual(["browser-2"]);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("keeps a sent item when a live message comes before the end of its load", () => {
      const sent = { ...createQueuedItem("browser-1", "Hello"), sent: true };
      const base = createTestModel({ queue: [sent] });

      story(
        update,
        given(base),
        message(Message.ReceivedLiveMessage({ message: createChatMessage(102, "Reply") })),
        model((m) => {
          expect(m.queue.map((item) => item.id)).toEqual(["browser-1"]);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("removes a sent item when its own load fails", () => {
      const sent = { ...createQueuedItem("browser-1", "Hello"), sent: true };
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
        queue: [q1],
        attempt: { id: "q1", error: "failed", retryAt: 12345 },
      });

      story(
        update,
        given(base),
        message(Message.DiscardQueuedClicked({ id: "q1" })),
        model((m) => {
          expect(m.queue.length).toBe(0);
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
      const base = createTestModel({ queue: [q1], failureCount: 0 });
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
              harness: "claude-code",
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
        queue: [q1],
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
              harness: "claude-code",
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
        queue: [q1],
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
        queue: [q1],
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
              harness: "claude-code",
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
          const list = m.messages;
          expect(list.length).toBe(3);
          expect(list.map((x) => x.id)).toEqual([10, 20, 30]);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("keeps the question of a message when an older read of the page has none", () => {
      const asked = { ...createChatMessage(10, "Which store?"), author: "Question" as const };
      const withQuestion = { ...asked, question: createInboxItem({ id: 52 }) };
      const base = createTestModel({ messages: [withQuestion] });

      story(
        update,
        given(base),
        message(
          Message.PageLoaded({
            page: {
              harness: "claude-code",
              messages: [asked],
              older: false,
              writing: false,
              stoppable: false,
              pausedUntil: null,
            },
            isInitial: false,
          }),
        ),
        model((m) => {
          expect(m.messages.map((x) => x.question?.id)).toEqual([52]);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("keeps the action of a message when an older read of the page has none", () => {
      const sent = { ...createChatMessage(10, "Message to the Lead"), author: "Action" as const };
      const withAction = { ...sent, action: createPendingAction({ id: 42 }) };
      const base = createTestModel({ messages: [withAction] });

      story(
        update,
        given(base),
        message(
          Message.PageLoaded({
            page: {
              harness: "claude-code",
              messages: [sent],
              older: false,
              writing: false,
              stoppable: false,
              pausedUntil: null,
            },
            isInitial: false,
          }),
        ),
        model((m) => {
          expect(m.messages.map((x) => x.action?.id)).toEqual([42]);
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
          const list = m.messages;
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
          const list = m.messages;
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
          const list = m.messages;
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
        queue: [queued],
      });

      story(
        update,
        given(base),
        message(Message.ReceivedLiveMessage({ message: liveMsg })),
        model((m) => {
          expect(m.queue.length).toBe(0);
          expect(m.messages.length).toBe(1);
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
      const item = createInboxItem({
        id: 101,
        kind: "approval",
        action: createPendingAction({
          tool: "edit_brief",
          role: "lead",
          arguments: [{ name: "brief", value: "Updated brief text", labels: [] }],
        }),
      });

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
        message(Message.ReceivedAnswerDraft({ id: 101, draft: createDraft("") })),
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
    const question = createInboxItem({ id: 7 });
    const approval = createInboxItem({
      id: 8,
      kind: "approval",
      title: "Approve it",
      issue: 3,
      action: createPendingAction(),
    });
    const todo = createInboxItem({
      id: 9,
      kind: "todo",
      issue: 5,
      todo: {
        afterMerge: false,
        dismissable: false,
        kind: "env_var",
        line: 1,
        mandatory: true,
        name: "plan",
        note: "",
        path: "app.yaml",
        reason: "",
      },
    });

    it("keeps the list and opens the panel again with the error after a failed decision", () => {
      const messages = [createChatMessage(1, "Hello")];
      const base = createTestModel({
        messages,
        trayItems: [question],
        activePanel: null,
        closedPanel: { type: "answer", item: approval },
        requesting: [8],
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
          expect(m.requesting).toEqual([]);
        }),
        message(Message.ReceivedAnswerDraft({ id: 8, draft: createDraft("") })),
        StoryCommand.expectNone(),
      );
    });

    it("keeps the panel with the error when the tray without its row comes after the failed request", () => {
      const base = createTestModel({
        trayItems: [approval],
        activePanel: { type: "answer", item: approval },
        requesting: [8],
      });

      story(
        update,
        given(base),
        message(Message.ActionFailed({ id: 8, error: "The server failed." })),
        message(Message.ReceivedTrayItems({ items: [] })),
        model((m) => {
          expect(m.activePanel?.item).toBe(approval);
          expect(m.closedPanel).toBeNull();
          expect(m.answerError).toBe("The server failed.");
        }),
        message(Message.ReceivedTrayItems({ items: [approval] })),
        model((m) => {
          expect(m.activePanel?.item).toBe(approval);
          expect(m.answerError).toBe("The server failed.");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("opens the panel again with the error when the tray without its row comes before the failed request", () => {
      const base = createTestModel({
        trayItems: [approval],
        activePanel: { type: "answer", item: approval },
        requesting: [8],
      });

      story(
        update,
        given(base),
        message(Message.ReceivedTrayItems({ items: [] })),
        model((m) => {
          expect(m.activePanel).toBeNull();
          expect(m.closedPanel?.item).toBe(approval);
        }),
        message(Message.ActionFailed({ id: 8, error: "The server failed." })),
        model((m) => {
          expect(m.activePanel?.item).toBe(approval);
          expect(m.closedPanel).toBeNull();
          expect(m.answerError).toBe("The server failed.");
        }),
        message(Message.ReceivedAnswerDraft({ id: 8, draft: createDraft("") })),
        StoryCommand.expectNone(),
      );
    });

    it("closes the panel with the error when a list without its row comes after a read that showed the row", () => {
      const base = createTestModel({
        trayItems: [approval],
        activePanel: { type: "answer", item: approval },
        requesting: [8],
      });

      story(
        update,
        given(base),
        message(Message.ActionFailed({ id: 8, error: "The server failed." })),
        message(Message.ReceivedTrayItems({ items: [approval] })),
        message(Message.ReceivedTrayItems({ items: [] })),
        model((m) => {
          expect(m.activePanel).toBeNull();
          expect(m.closedPanel?.item).toBe(approval);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("shows no old error in a panel that the Owner opens again", () => {
      const base = createTestModel({
        trayItems: [approval],
        activePanel: { type: "answer", item: approval },
        requesting: [8],
      });

      story(
        update,
        given(base),
        message(Message.ActionFailed({ id: 8, error: "The server failed." })),
        message(Message.ClosePanel()),
        model((m) => {
          expect(m.answerError).toBe("");
          expect(m.answerErrorId).toBe(0);
        }),
        message(Message.OpenAnswerPanel({ item: approval })),
        model((m) => {
          expect(m.answerError).toBe("");
        }),
        message(Message.ReceivedAnswerDraft({ id: 8, draft: createDraft("") })),
        StoryCommand.expectNone(),
      );
    });

    it("clears the error of an item when its panel opens from the tray with no close before", () => {
      const other = createInboxItem({ id: 11 });
      const base = createTestModel({
        trayItems: [approval],
        activePanel: { type: "answer", item: other },
        closedPanel: { type: "answer", item: approval },
        requesting: [8],
      });

      story(
        update,
        given(base),
        message(Message.ActionFailed({ id: 8, error: "The server failed." })),
        model((m) => {
          expect(m.answerError).toBe("The server failed.");
          expect(m.answerErrorId).toBe(8);
        }),
        message(Message.OpenAnswerPanel({ item: approval })),
        model((m) => {
          expect(m.activePanel?.item.id).toBe(8);
          expect(m.answerError).toBe("");
          expect(m.answerErrorId).toBe(0);
        }),
        message(Message.ReceivedAnswerDraft({ id: 8, draft: createDraft("") })),
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
          expect(m.requesting).toEqual([9]);
        }),
        StoryCommand.expectExact(
          DoneInboxItem({
            id: 9,
            repository: "test-repo",
            issue: 5,
            name: "plan",
            note: "",
            voice: false,
          }),
        ),
        StoryCommand.resolve(DoneInboxItem, Message.AnswerCompleted({ id: 9, error: "" })),
        StoryCommand.expectNone(),
      );
    });

    it("ignores a second tap on Stop while the first request runs", () => {
      const base = createTestModel({ stoppable: true });

      story(
        update,
        given(base),
        message(Message.StopChatClicked()),
        StoryCommand.expectExact(
          StopChat({ organization: "test-org", repository: "test-repo", workstream: 1428 }),
        ),
        StoryCommand.resolve(StopChat, Message.StopChatCompleted({ error: null })),
        message(Message.StopChatClicked()),
        model((m) => {
          expect(m.stopping).toBe(true);
        }),
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
      const other = createInboxItem({ id: 11 });
      const base = createTestModel({
        activePanel: { type: "answer", item: other },
        closedPanel: null,
      });

      story(
        update,
        given(base),
        message(Message.AnswerCompleted({ id: 7, error: "" })),
        model((m) => {
          expect(m.activePanel?.item).toBe(other);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("keeps the panel of another item when a request ends with an error, and shows the error only for its item", () => {
      const other = createInboxItem({ id: 11 });
      const base = createTestModel({
        activePanel: { type: "answer", item: other },
        closedPanel: { type: "answer", item: question },
      });

      story(
        update,
        given(base),
        message(Message.AnswerCompleted({ id: 7, error: "No." })),
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
        message(Message.ActionDecided({ decision: "approve" })),
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

    it("ignores a second tap on Approve while the first request runs, and sends the note of the field", () => {
      const base = createTestModel({
        activePanel: { type: "answer", item: approval },
        answerText: "Go ahead.",
      });

      const first = update(base, Message.ActionDecided({ decision: "approve" }));
      expect(first.model.requesting).toEqual([8]);
      expect((first.commands ?? []).map((command) => command.name)).toEqual([
        "StopVoice",
        "ApproveAction",
      ]);
      expect(first.commands?.[1].args).toEqual({
        actionId: 42,
        inboxItemId: 8,
        repository: "test-repo",
        issue: 3,
        title: "Approve it",
        text: "Go ahead.",
      });
      const second = update(first.model, Message.ActionDecided({ decision: "reject" }));
      expect(second.model).toBe(first.model);
      expect(second.commands ?? []).toEqual([]);
    });

    it("sends an answer one time when the Owner taps Send again before the row leaves", () => {
      const base = createTestModel({
        activePanel: { type: "answer", item: question },
        answerText: "Ten seats.",
      });

      const sent = update(base, Message.AnswerSubmitted());
      expect(sent.model.requesting).toEqual([7]);
      const second = update(sent.model, Message.AnswerSubmitted());
      expect(second.model).toBe(sent.model);
      expect(second.commands ?? []).toEqual([]);
      const done = update(sent.model, Message.AnswerCompleted({ id: 7, error: "" }));
      expect(done.model.requesting).toEqual([]);
    });

    it("reads the draft of its own item when a panel opens again", () => {
      const base = createTestModel({
        activePanel: null,
        closedPanel: { type: "answer", item: question },
        answerText: "The note of another item.",
        answerLoaded: true,
      });

      story(
        update,
        given(base),
        message(Message.AnswerCompleted({ id: 7, error: "No." })),
        model((m) => {
          expect(m.activePanel?.item).toBe(question);
          expect(m.answerText).toBe("");
          expect(m.answerLoaded).toBe(false);
        }),
        message(Message.ReceivedAnswerDraft({ id: 7, draft: createDraft("My own note.") })),
        model((m) => {
          expect(m.answerText).toBe("My own note.");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("ends the load of the queue and shows the error when the store fails", () => {
      story(
        update,
        given(createTestModel()),
        message(Message.QueueFailed({ error: "The store failed." })),
        model((m) => {
          expect(m.queueLoaded).toBe(true);
          expect(m.error).toBe("The store failed.");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("lets another item send while the request of the first item runs", () => {
      const other = createInboxItem({ id: 11 });
      const base = createTestModel({
        activePanel: { type: "answer", item: other },
        answerText: "Eleven.",
        requesting: [7],
      });

      story(
        update,
        given(base),
        message(Message.AnswerSubmitted()),
        model((m) => {
          expect(m.requesting).toEqual([7, 11]);
        }),
        StoryCommand.expectExact(
          AnswerInboxItem({
            id: 11,
            repository: "test-repo",
            issue: 0,
            workstream: 1428,
            answer: "Eleven.",
            voice: false,
          }),
        ),
        StoryCommand.resolve(AnswerInboxItem, Message.AnswerCompleted({ id: 11, error: "" })),
        model((m) => {
          expect(m.requesting).toEqual([7]);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("ignores a second tap on a tray button while the request of the row runs", () => {
      const base = createTestModel({ trayItems: [question] });

      const first = update(base, Message.TrayActionClicked({ item: question, kind: "dismiss" }));
      expect(first.commands?.map((command) => command.name)).toEqual(["RunTrayAction"]);
      const second = update(
        first.model,
        Message.TrayActionClicked({ item: question, kind: "merge" }),
      );
      expect(second.model).toBe(first.model);
      expect(second.commands ?? []).toEqual([]);
      const resume = update(first.model, Message.ResumeClicked({ item: question }));
      expect(resume.model).toBe(first.model);
      expect(resume.commands ?? []).toEqual([]);
    });

    it("saves the state of the tray with a Command", () => {
      story(
        update,
        given(createTestModel({ trayCollapsed: false })),
        message(Message.TrayToggled()),
        model((m) => {
          expect(m.trayCollapsed).toBe(true);
        }),
        StoryCommand.expectExact(SaveTrayState({ collapsed: true })),
        StoryCommand.resolve(SaveTrayState, Message.CompletedSaveTrayState()),
        StoryCommand.expectNone(),
      );
    });

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
        StoryCommand.expectExact(
          PostQueued({ item: createQueuedItem("q1", "Show the prices."), delay: retryDelays[0] }),
        ),
        StoryCommand.resolve(PostQueued, Message.SendAttemptSucceeded({ id: "q1" })),
        StoryCommand.resolve(
          FetchPage,
          Message.PageLoaded({
            page: {
              harness: "",
              messages: [],
              older: false,
              stoppable: false,
              writing: false,
              pausedUntil: null,
            },
            isInitial: false,
            sent: "q1",
          }),
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

      const answer = createTestModel({
        activePanel: { type: "answer", item: question },
        answerText: "Ten seats.",
        voice,
      });
      const answerResult = update(answer, Message.AnswerSubmitted());
      expect(answerResult.model).toBe(answer);
      expect(described(answerResult.commands)).toEqual(described([StopVoice()]));
    });

    it("sends the answer with the voice mark when the card of the panel is ready", () => {
      const base = createTestModel({
        activePanel: { type: "answer", item: question },
        answerText: "Ten seats.",
        answerVoice: { state: "ready", audio: null, conversion: "", error: "" },
      });

      const result = update(base, Message.AnswerSubmitted());
      expect(described(result.commands)).toEqual(
        described([
          AnswerInboxItem({
            id: 7,
            repository: "test-repo",
            issue: 0,
            workstream: 1428,
            answer: "Ten seats.",
            voice: true,
          }),
        ]),
      );
    });

    it("sends nothing from the panel while its voice card converts or has failed", () => {
      for (const state of ["converting", "failed"] as const) {
        const base = createTestModel({
          activePanel: { type: "answer", item: question },
          answerText: "Ten seats.",
          answerVoice: { state, audio: null, conversion: "", error: "" },
        });
        const send = update(base, Message.AnswerSubmitted());
        expect(send.model).toBe(base);
        expect(send.commands ?? []).toEqual([]);
        const decide = update(
          { ...base, activePanel: { type: "answer", item: approval } },
          Message.ActionDecided({ decision: "approve" }),
        );
        expect(decide.commands ?? []).toEqual([]);
      }
    });

    it("starts the voice of the field that the Message names", () => {
      const base = createTestModel({ activePanel: { type: "answer", item: question } });

      expect(
        described(update(base, Message.VoiceButtonClicked({ target: "chat" })).commands),
      ).toEqual(described([ToggleVoice({ key: "chatDraft:test-org/test-repo/1428" })]));
      expect(
        described(update(base, Message.VoiceButtonClicked({ target: "answer" })).commands),
      ).toEqual(described([ToggleVoice({ key: "answerDraft:7" })]));
      expect(
        update(createTestModel(), Message.VoiceButtonClicked({ target: "answer" })).commands ?? [],
      ).toEqual([]);
    });

    it("sends again and removes the voice card of the field that the Message names", () => {
      const base = createTestModel({ activePanel: { type: "answer", item: question } });

      expect(
        described(update(base, Message.VoiceResendClicked({ target: "answer" })).commands),
      ).toEqual(described([ResendVoice({ key: "answerDraft:7" })]));
      expect(
        described(update(base, Message.VoiceRemoveClicked({ target: "chat" })).commands),
      ).toEqual(described([RemoveVoice({ key: "chatDraft:test-org/test-repo/1428" })]));
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

    it("takes the draft of the panel only when the panel of that item is open", () => {
      const draft = createDraft("My note.", {
        voice: { state: "converting", audio: null, conversion: "c1", error: "" },
      });
      const base = createTestModel({ activePanel: { type: "answer", item: question } });

      const other = update(base, Message.ReceivedAnswerDraft({ id: 99, draft }));
      expect(other.model).toBe(base);
      const own = update(base, Message.ReceivedAnswerDraft({ id: 7, draft }));
      expect(own.model.answerText).toBe("My note.");
      expect(own.model.answerVoice?.state).toBe("converting");
      expect(own.model.answerLoaded).toBe(true);
    });

    it("keeps the answer of an open panel when the Owner opens the same panel again", () => {
      const base = createTestModel({
        activePanel: { type: "answer", item: question },
        answerText: "Typed.",
        answerLoaded: true,
      });

      const result = update(base, Message.OpenAnswerPanel({ item: question }));
      expect(result.model.answerText).toBe("Typed.");
      expect(result.model.answerLoaded).toBe(true);
    });

    it("shows the error of a refused message", () => {
      const result = update(createTestModel(), Message.MessageRefused({ error: "No room." }));
      expect(result.model.sendError).toBe("No room.");
    });

    it("scrolls to the first blocking todo of a pull request", () => {
      const result = update(createTestModel(), Message.BlockingTodoClicked({ id: 12 }));
      expect(described(result.commands)).toEqual(described([ShowBlockingTodo({ id: 12 })]));
    });
  });
});
