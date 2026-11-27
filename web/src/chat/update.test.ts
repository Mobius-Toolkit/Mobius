import { describe, expect, it, vi } from "vitest";
import {
  Command as StoryCommand,
  expectOutMessage,
  given,
  message,
  model,
  story,
} from "foldkit/story";
import {
  AnswerInboxItem,
  ApproveAction,
  FetchActionBrief,
  FetchPage,
  DoneInboxItem,
  ListQueued,
  ResumeStoppedItem,
  PostQueued,
  readQueued,
  RemoveQueued,
  RunTrayAction,
  SaveTrayState,
  StopChat,
  WaitToRetry,
} from "./commands";
import { StopVoice } from "@/composer/commands";
import * as Selection from "@/ui/selection";
import * as SelectionFixture from "@/ui/selection-fixtures";
import type * as Composer from "@/composer";
import * as ComposerFixture from "@/composer/fixtures";
import { newAnswerComposer } from "./composer";
import { Message, OutMessage, toGotAnswerComposerMessage, toGotSelectionMessage } from "./message";
import {
  cardOwner,
  dialogOwner,
  initialModel,
  quoteLabel,
  retryDelays,
  type Model,
  type Waiting,
} from "./model";
import type { ChatMessage, Draft, InboxItem, PendingAction, Queued } from "./schemas";
import { enqueue, stopChat, update } from "./update";

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
  selections: [],
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

// A model whose answer panel of the item is open and whose field has read its draft.
function createPanelModel(
  item: InboxItem,
  field: Partial<Composer.Model> = {},
  overrides: Partial<Model> = {},
): Model {
  const base = createTestModel();
  return createTestModel({
    activePanel: { type: "answer", item },
    answerComposer: { ...newAnswerComposer(base, item), draftLoaded: true, ...field },
    ...overrides,
  });
}

const submitted = toGotAnswerComposerMessage(ComposerFixture.sendSubmitted);

const noParts = { images: [], inboxItems: [], selections: [], voice: false };

function createQueuedItem(id: string, text: string): Queued {
  return {
    id,
    organization: "test-org",
    repository: "test-repo",
    workstream: 1428,
    text,
    images: [],
    inboxItems: [],
    selections: [],
    voice: false,
    voiceSeconds: 0,
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
    displayOnly: false,
    failedAt: null,
    images: 0,
    inboxItems: [],
    selections: [],
    question: null,
    stoppedAt: null,
    voice: false,
    voiceSeconds: 0,
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
    mergeQueuePlace: null,
    mergeQueueSize: 0,
    ...overrides,
  };
}

describe("chat update story tests", () => {
  describe("send queue logic", () => {
    it("queues an outgoing message and dispatches PostQueued if idle", () => {
      const item = createQueuedItem("q1", "First message");

      const result = enqueue(createTestModel(), item);
      expect(result.model.queue.map((other) => other.id)).toEqual(["q1"]);
      expect(described(result.commands)).toEqual(
        described([PostQueued({ item, delay: retryDelays[0] })]),
      );
    });

    it("does not dispatch PostQueued immediately if another message is in progress", () => {
      const first = createQueuedItem("q1", "First");
      const second = createQueuedItem("q2", "Second");
      const base = createTestModel({
        queue: [first],
        attempt: { id: "q1", error: "network error", retryAt: 1700000005000 },
      });

      const result = enqueue(base, second);
      expect(result.model.queue.map((other) => other.id)).toEqual(["q1", "q2"]);
      expect(result.commands ?? []).toEqual([]);
    });

    it("keeps one queue entry for a message that the outbox read already added", () => {
      const item = createQueuedItem("q1", "First message");
      const base = createTestModel({ queue: [item] });

      const result = enqueue(base, item);
      expect(result.model).toBe(base);
      expect(result.commands ?? []).toEqual([]);
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
        message(toGotAnswerComposerMessage(ComposerFixture.draftReceived(createDraft("")))),
        model((m) => {
          expect(m.answerComposer?.draftLoaded).toBe(true);
          expect(m.answerComposer?.draftKey).toBe("answerDraft:101");
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
          expect(m.answerComposer?.draftKey).toBe("answerDraft:8");
          expect(m.answerComposer?.draftLoaded).toBe(false);
          expect(m.answerComposer?.sendError).toBe("The server failed.");
          expect(m.requesting).toEqual([]);
        }),
        StoryCommand.expectNone(),
      );
    });

    it("keeps the panel with the error when the tray without its row comes after the failed request", () => {
      const base = createPanelModel(approval, {}, { trayItems: [approval], requesting: [8] });

      story(
        update,
        given(base),
        message(Message.ActionFailed({ id: 8, error: "The server failed." })),
        message(Message.ReceivedTrayItems({ items: [] })),
        model((m) => {
          expect(m.activePanel?.item).toBe(approval);
          expect(m.closedPanel).toBeNull();
          expect(m.answerComposer?.sendError).toBe("The server failed.");
        }),
        message(Message.ReceivedTrayItems({ items: [approval] })),
        model((m) => {
          expect(m.activePanel?.item).toBe(approval);
          expect(m.answerComposer?.sendError).toBe("The server failed.");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("opens the panel again with the error when the tray without its row comes before the failed request", () => {
      const base = createPanelModel(approval, {}, { trayItems: [approval], requesting: [8] });

      story(
        update,
        given(base),
        message(Message.ReceivedTrayItems({ items: [] })),
        model((m) => {
          expect(m.activePanel).toBeNull();
          expect(m.answerComposer).toBeNull();
          expect(m.closedPanel?.item).toBe(approval);
        }),
        message(Message.ActionFailed({ id: 8, error: "The server failed." })),
        model((m) => {
          expect(m.activePanel?.item).toBe(approval);
          expect(m.closedPanel).toBeNull();
          expect(m.answerComposer?.sendError).toBe("The server failed.");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("closes the panel with the error when a list without its row comes after a read that showed the row", () => {
      const base = createPanelModel(approval, {}, { trayItems: [approval], requesting: [8] });

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
      const base = createPanelModel(approval, {}, { trayItems: [approval], requesting: [8] });

      story(
        update,
        given(base),
        message(Message.ActionFailed({ id: 8, error: "The server failed." })),
        message(Message.ClosePanel()),
        model((m) => {
          expect(m.answerComposer).toBeNull();
        }),
        message(Message.OpenAnswerPanel({ item: approval })),
        model((m) => {
          expect(m.answerComposer?.sendError).toBe("");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("shows the error of a request in no panel of another item", () => {
      const other = createInboxItem({ id: 11 });
      const base = createPanelModel(
        other,
        {},
        { trayItems: [approval], closedPanel: { type: "answer", item: approval }, requesting: [8] },
      );

      story(
        update,
        given(base),
        message(Message.ActionFailed({ id: 8, error: "The server failed." })),
        model((m) => {
          expect(m.activePanel?.item.id).toBe(11);
          expect(m.answerComposer?.draftKey).toBe("answerDraft:11");
          expect(m.answerComposer?.sendError).toBe("");
        }),
        message(Message.OpenAnswerPanel({ item: approval })),
        model((m) => {
          expect(m.activePanel?.item.id).toBe(8);
          expect(m.answerComposer?.draftKey).toBe("answerDraft:8");
          expect(m.answerComposer?.sendError).toBe("");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("sends a todo with an empty note", () => {
      const base = createPanelModel(todo);

      story(
        update,
        given(base),
        message(submitted),
        model((m) => {
          expect(m.requesting).toEqual([9]);
        }),
        StoryCommand.expectExact(
          DoneInboxItem({
            id: 9,
            repository: "test-repo",
            issue: 5,
            notice: "Marked the todo plan as done",
            note: "",
            ...noParts,
          }),
        ),
        StoryCommand.resolve(DoneInboxItem, Message.AnswerCompleted({ id: 9, error: "" })),
        StoryCommand.expectNone(),
      );
    });

    it("sends an owner action with a note", () => {
      const ownerAction = createInboxItem({
        id: 12,
        kind: "owner action",
        issue: 5,
        pullRequest: 8,
        pullRequestUrl: "https://github.com/test-repo/pull/8",
      });
      const base = createPanelModel(ownerAction, { draftText: "Logged in." });

      story(
        update,
        given(base),
        message(submitted),
        StoryCommand.expectExact(
          DoneInboxItem({
            id: 12,
            repository: "test-repo",
            issue: 5,
            notice: "Marked the action as done",
            note: "Logged in.",
            ...noParts,
          }),
        ),
        StoryCommand.resolve(DoneInboxItem, Message.AnswerCompleted({ id: 12, error: "" })),
        model((m) => {
          expect(m.activePanel).toBeNull();
        }),
        StoryCommand.expectNone(),
      );
    });

    it("resumes a stopped item with an empty note and does not answer", () => {
      const stopped = createInboxItem({ id: 13, kind: "stopped", issue: 6 });
      const base = createPanelModel(stopped);

      story(
        update,
        given(base),
        message(submitted),
        StoryCommand.expectExact(
          ResumeStoppedItem({
            id: 13,
            repository: "test-repo",
            issue: 6,
            notice: "Resumed #6",
          }),
        ),
        StoryCommand.resolve(ResumeStoppedItem, Message.AnswerCompleted({ id: 13, error: "" })),
        model((m) => {
          expect(m.activePanel).toBeNull();
        }),
        StoryCommand.expectNone(),
      );
    });

    it("answers a stopped item with a note and does not resume", () => {
      const stopped = createInboxItem({ id: 13, kind: "stopped", issue: 6 });
      const base = createPanelModel(stopped, { draftText: "Try again." });

      story(
        update,
        given(base),
        message(submitted),
        StoryCommand.expectExact(
          AnswerInboxItem({
            id: 13,
            repository: "test-repo",
            issue: 6,
            answer: "Try again.",
            notice: "Resumed #6",
            ...noParts,
          }),
        ),
        StoryCommand.resolve(AnswerInboxItem, Message.AnswerCompleted({ id: 13, error: "" })),
        StoryCommand.expectNone(),
      );
    });

    it("ignores a second tap on Stop while the first request runs", () => {
      const first = stopChat(createTestModel({ stoppable: true }));
      expect(described(first.commands)).toEqual(
        described([
          StopChat({ organization: "test-org", repository: "test-repo", workstream: 1428 }),
        ]),
      );
      expect(first.model.stopping).toBe(true);

      const second = stopChat(first.model);
      expect(second.model).toBe(first.model);
      expect(second.commands ?? []).toEqual([]);
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
          expect(m.composer.sendError).toBe("Stop failed");
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
      const base = createPanelModel(other, {}, { closedPanel: { type: "answer", item: question } });

      story(
        update,
        given(base),
        message(Message.AnswerCompleted({ id: 7, error: "No." })),
        model((m) => {
          expect(m.activePanel?.item).toBe(other);
          expect(m.closedPanel?.item).toBe(question);
          expect(m.answerComposer?.draftKey).toBe("answerDraft:11");
          expect(m.answerComposer?.sendError).toBe("");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("clears the error at a new decision", () => {
      const base = createPanelModel(approval, { sendError: "Old error." });

      story(
        update,
        given(base),
        message(Message.ActionDecided({ decision: "approve" })),
        model((m) => {
          expect(m.answerComposer?.sendError).toBe("");
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
            ...noParts,
          }),
        ),
        StoryCommand.resolve(StopVoice, ComposerFixture.voiceStopped),
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
      const base = createPanelModel(approval, { draftText: "Go ahead." });

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
        ...noParts,
      });
      const second = update(first.model, Message.ActionDecided({ decision: "reject" }));
      expect(second.model).toBe(first.model);
      expect(second.commands ?? []).toEqual([]);
    });

    it("sends an answer one time when the Owner taps Send again before the row leaves", () => {
      const base = createPanelModel(question, { draftText: "Ten seats." });

      const sent = update(base, submitted);
      expect(sent.model.requesting).toEqual([7]);
      const second = update(sent.model, submitted);
      expect(second.model.requesting).toEqual([7]);
      expect(second.commands ?? []).toEqual([]);
      const done = update(sent.model, Message.AnswerCompleted({ id: 7, error: "" }));
      expect(done.model.requesting).toEqual([]);
    });

    it("reads the draft of its own item when a panel opens again", () => {
      const base = createTestModel({
        activePanel: null,
        closedPanel: { type: "answer", item: question },
      });

      story(
        update,
        given(base),
        message(Message.AnswerCompleted({ id: 7, error: "No." })),
        model((m) => {
          expect(m.activePanel?.item).toBe(question);
          expect(m.answerComposer?.draftText).toBe("");
          expect(m.answerComposer?.draftLoaded).toBe(false);
        }),
        message(
          toGotAnswerComposerMessage(ComposerFixture.draftReceived(createDraft("My own note."))),
        ),
        model((m) => {
          expect(m.answerComposer?.draftText).toBe("My own note.");
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
      const base = createPanelModel(other, { draftText: "Eleven." }, { requesting: [7] });

      story(
        update,
        given(base),
        message(submitted),
        model((m) => {
          expect(m.requesting).toEqual([7, 11]);
        }),
        StoryCommand.expectExact(
          AnswerInboxItem({
            id: 11,
            repository: "test-repo",
            issue: 0,
            answer: "Eleven.",
            notice: "Sent the answer to the Lead of #1428",
            ...noParts,
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

    it("adds one queue entry and sends PostQueued on outbox change with a new queued message, and adds no second entry on second change with the same id", () => {
      const item = createQueuedItem("q1", "Queued from elsewhere.");
      const base = createTestModel();

      story(
        update,
        given(base),
        message(Message.OutboxChanged()),
        StoryCommand.expectExact(
          ListQueued({
            organization: "test-org",
            repository: "test-repo",
            workstream: 1428,
          }),
        ),
        StoryCommand.resolve(ListQueued, Message.QueueLoaded({ items: [item] })),
        model((m) => {
          expect(m.queue.length).toBe(1);
          expect(m.queue[0].id).toBe("q1");
        }),
        StoryCommand.expectExact(PostQueued({ item, delay: retryDelays[0] })),
        StoryCommand.resolve(
          PostQueued,
          Message.SendAttemptFailed({ id: "q1", error: "Network error", retryAt: 9999999 }),
        ),
        StoryCommand.expectExact(WaitToRetry({ id: "q1", delay: retryDelays[0], sequence: 0 })),
        StoryCommand.resolve(WaitToRetry, Message.RetryDue({ id: "q1", sequence: 999 })),
        message(Message.OutboxChanged()),
        StoryCommand.expectExact(
          ListQueued({
            organization: "test-org",
            repository: "test-repo",
            workstream: 1428,
          }),
        ),
        StoryCommand.resolve(ListQueued, Message.QueueLoaded({ items: [item] })),
        model((m) => {
          expect(m.queue.length).toBe(1);
          expect(m.queue[0].id).toBe("q1");
        }),
        StoryCommand.expectNone(),
      );
    });

    it("stops the recording and sends nothing when the answer panel is submitted during a recording", () => {
      const voice = {
        recording: true,
        key: "x",
        startedAt: 1,
        live: "",
        notice: { key: "", text: "" },
      };
      const answer = createPanelModel(question, { draftText: "Ten seats.", voice });
      const answerResult = update(answer, submitted);
      expect(answerResult.model.requesting).toEqual([]);
      expect(answerResult.commands?.map(({ name }) => name)).toEqual(["StopVoice"]);
    });

    it("sends the answer with the voice mark when the card of the panel is ready", () => {
      const base = createPanelModel(question, {
        draftText: "Ten seats.",
        draftVoice: {
          state: "ready",
          audio: null,
          conversion: "",
          error: "",
          seconds: 0,
          text: "",
        },
      });

      const result = update(base, submitted);
      expect(described(result.commands)).toEqual(
        described([
          AnswerInboxItem({
            id: 7,
            repository: "test-repo",
            issue: 0,
            answer: "Ten seats.",
            notice: "Sent the answer to the Lead of #1428",
            ...noParts,
            voice: true,
          }),
        ]),
      );
    });

    it("sends nothing from the panel while its voice card converts or has failed", () => {
      for (const state of ["converting", "failed"] as const) {
        const draftVoice = { state, audio: null, conversion: "", error: "", seconds: 0, text: "" };
        const send = update(
          createPanelModel(question, { draftText: "Ten seats.", draftVoice }),
          submitted,
        );
        expect(send.model.requesting).toEqual([]);
        expect(send.commands ?? []).toEqual([]);
        const decide = update(
          createPanelModel(approval, { draftText: "Ten seats.", draftVoice }),
          Message.ActionDecided({ decision: "approve" }),
        );
        expect(decide.commands ?? []).toEqual([]);
      }
    });

    it("sends the images, the Inbox items and the selections of the field with the answer", () => {
      const image = new File(["x"], "a.png", { type: "image/png" });
      const inboxItem = { id: 3, reference: "#3", title: "Plan", reason: "", link: "" };
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
      const base = createPanelModel(question, {
        draftImages: [image],
        draftInboxItems: [inboxItem],
        draftSelections: [selection],
      });

      story(
        update,
        given(base),
        message(submitted),
        model((m) => {
          expect(m.requesting).toEqual([7]);
        }),
        StoryCommand.expectExact(
          AnswerInboxItem({
            id: 7,
            repository: "test-repo",
            issue: 0,
            answer: "",
            notice: "Sent the answer to the Lead of #1428",
            images: [image],
            inboxItems: [inboxItem],
            selections: [selection],
            voice: false,
          }),
        ),
        StoryCommand.resolve(AnswerInboxItem, Message.AnswerCompleted({ id: 7, error: "" })),
        StoryCommand.expectNone(),
      );
    });

    it("sends the decision with the images, the Inbox items and the selections of the field", () => {
      const image = new File(["x"], "a.png", { type: "image/png" });
      const base = createPanelModel(approval, {
        draftText: "No.",
        draftImages: [image],
        draftInboxItems: [{ id: 3, reference: "#3", title: "Plan", reason: "", link: "" }],
      });

      const rejected = update(base, Message.ActionDecided({ decision: "reject" }));
      expect(rejected.commands?.[1].name).toBe("RejectAction");
      expect(rejected.commands?.[1].args).toEqual({
        actionId: 42,
        inboxItemId: 8,
        repository: "test-repo",
        issue: 3,
        title: "Approve it",
        text: "No.",
        images: [image],
        inboxItems: [{ id: 3, reference: "#3", title: "Plan", reason: "", link: "" }],
        selections: [],
        voice: false,
      });
    });

    it("sends a stopped item with only an attachment as an answer, and does not resume", () => {
      const stopped = createInboxItem({ id: 13, kind: "stopped", issue: 6 });
      const image = new File(["x"], "a.png", { type: "image/png" });
      const base = createPanelModel(stopped, { draftImages: [image] });

      const result = update(base, submitted);
      expect(result.commands?.map(({ name }) => name)).toEqual(["AnswerInboxItem"]);
    });

    it("sends a todo with only an attachment, and a question with only a selected option", () => {
      const image = new File(["x"], "a.png", { type: "image/png" });
      const attached = update(createPanelModel(todo, { draftImages: [image] }), submitted);
      expect(attached.commands?.[0].args).toMatchObject({ note: "", images: [image] });

      const asked = createInboxItem({
        id: 14,
        questions: [
          { text: "How many?", multiple: false, options: [{ title: "Ten", details: "" }] },
        ],
      });
      const selected = update(
        createPanelModel(asked, {}, { selectedOptions: { 0: [0] } }),
        submitted,
      );
      expect(selected.commands?.[0].args).toMatchObject({ answer: "How many?\n- Ten" });

      const nothing = update(createPanelModel(asked), submitted);
      expect(nothing.commands ?? []).toEqual([]);
    });

    it("gives the panel the field of its own item", () => {
      const base = createPanelModel(question, { draftText: "The note of another item." });

      const opened = update(base, Message.OpenAnswerPanel({ item: approval }));
      expect(opened.model.answerComposer?.draftKey).toBe("answerDraft:8");
      expect(opened.model.answerComposer?.draftText).toBe("");
      expect(opened.model.answerComposer?.draftLoaded).toBe(false);
    });

    it("keeps the field of an open panel when the Owner opens the same panel again", () => {
      const base = createPanelModel(question, { draftText: "Typed.", sendError: "Old error." });

      const result = update(base, Message.OpenAnswerPanel({ item: question }));
      expect(result.model.answerComposer?.draftText).toBe("Typed.");
      expect(result.model.answerComposer?.draftLoaded).toBe(true);
      expect(result.model.answerComposer?.sendError).toBe("");
    });

    it("removes the field with its panel", () => {
      const base = createPanelModel(question);

      expect(update(base, Message.ClosePanel()).model.answerComposer).toBeNull();
      expect(
        update(base, Message.OpenDetailsPanel({ item: question })).model.answerComposer,
      ).toBeNull();
      expect(update(base, Message.NavigateTo({ path: "/" })).model.answerComposer).toBeNull();
    });

    it("opens the todo list, moves to a todo panel, returns to the list, and closes when no todo remains", () => {
      const todo1 = createInboxItem({
        id: 101,
        kind: "todo",
        repository: "owner/shop",
        issue: 10,
        pullRequest: 44,
        title: "Rotate webhook secret",
        todo: {
          name: "Rotate webhook secret",
          kind: "action",
          reason: "Webhook secret changed.",
          path: "config.toml",
          line: 12,
          mandatory: true,
          afterMerge: false,
          dismissable: false,
          note: "",
        },
      });
      const todo2 = createInboxItem({
        id: 102,
        kind: "todo",
        repository: "owner/shop",
        issue: 10,
        pullRequest: 44,
        title: "Verify queue metric",
        todo: {
          name: "Verify queue metric",
          kind: "check",
          reason: "Optional check.",
          path: "metrics.go",
          line: 8,
          mandatory: false,
          afterMerge: true,
          dismissable: true,
          note: "",
        },
      });

      story(
        update,
        given(createTestModel({ trayItems: [todo1, todo2] })),
        message(Message.OpenTodoList({ repository: "owner/shop", pullRequest: 99 })),
        model((m) => {
          expect(m.todoList).toBeNull();
        }),
        message(Message.OpenTodoList({ repository: "owner/shop", pullRequest: 44 })),
        model((m) => {
          expect(m.todoList).toEqual({ repository: "owner/shop", pullRequest: 44 });
        }),
        message(Message.OpenAnswerPanel({ item: todo1 })),
        model((m) => {
          expect(m.activePanel?.item.id).toBe(101);
          expect(m.todoList).toEqual({ repository: "owner/shop", pullRequest: 44 });
        }),
        message(Message.ClosePanel()),
        model((m) => {
          expect(m.activePanel).toBeNull();
          expect(m.todoList).toEqual({ repository: "owner/shop", pullRequest: 44 });
        }),
        message(Message.OpenAnswerPanel({ item: todo1 })),
        message(submitted),
        model((m) => {
          expect(m.requesting).toEqual([101]);
        }),
        StoryCommand.expectExact(
          DoneInboxItem({
            id: 101,
            repository: "owner/shop",
            issue: 10,
            notice: "Marked the todo Rotate webhook secret as done",
            note: "",
            ...noParts,
          }),
        ),
        StoryCommand.resolve(DoneInboxItem, Message.AnswerCompleted({ id: 101, error: "" })),
        model((m) => {
          expect(m.activePanel).toBeNull();
          expect(m.todoList).toEqual({ repository: "owner/shop", pullRequest: 44 });
        }),
        message(Message.ReceivedTrayItems({ items: [todo2] })),
        model((m) => {
          expect(m.todoList).toEqual({ repository: "owner/shop", pullRequest: 44 });
        }),
        message(Message.ReceivedTrayItems({ items: [] })),
        model((m) => {
          expect(m.todoList).toBeNull();
        }),
      );
    });

    it("shows nothing after the back tap of a todo panel when the row has no todo left", () => {
      const openTodo = createInboxItem({
        id: 101,
        kind: "todo",
        repository: "owner/shop",
        issue: 10,
        pullRequest: 44,
        title: "Rotate webhook secret",
        todo: {
          name: "Rotate webhook secret",
          kind: "action",
          reason: "Webhook secret changed.",
          path: "config.toml",
          line: 12,
          mandatory: true,
          afterMerge: false,
          dismissable: false,
          note: "",
        },
      });

      story(
        update,
        given(
          createTestModel({
            trayItems: [],
            todoList: { repository: "owner/shop", pullRequest: 44 },
            activePanel: { type: "answer", item: openTodo },
          }),
        ),
        message(Message.ClosePanel()),
        model((m) => {
          expect(m.activePanel).toBeNull();
          expect(m.todoList).toBeNull();
        }),
      );
    });

    it("dismisses an optional todo from its panel, shows refusal errors in the panel, and returns to the list on success", () => {
      const todo1 = createInboxItem({
        id: 201,
        kind: "todo",
        repository: "owner/shop",
        issue: 10,
        pullRequest: 44,
        title: "Rotate webhook secret",
        todo: {
          name: "Rotate webhook secret",
          kind: "action",
          reason: "Webhook secret changed.",
          path: "config.toml",
          line: 12,
          mandatory: true,
          afterMerge: false,
          dismissable: false,
          note: "",
        },
      });
      const todo2 = createInboxItem({
        id: 202,
        kind: "todo",
        repository: "owner/shop",
        issue: 10,
        pullRequest: 44,
        title: "Verify queue metric",
        todo: {
          name: "Verify queue metric",
          kind: "check",
          reason: "Optional check.",
          path: "metrics.go",
          line: 8,
          mandatory: false,
          afterMerge: true,
          dismissable: true,
          note: "",
        },
      });

      story(
        update,
        given(
          createPanelModel(
            todo2,
            {},
            {
              trayItems: [todo1, todo2],
              todoList: { repository: "owner/shop", pullRequest: 44 },
            },
          ),
        ),
        message(Message.TrayActionClicked({ item: todo2, kind: "dismiss" })),
        model((m) => {
          expect(m.trayRunning).toEqual({ 202: "dismiss" });
        }),
        StoryCommand.expectExact(RunTrayAction({ item: todo2, kind: "dismiss" })),
        StoryCommand.resolve(
          RunTrayAction,
          Message.TrayActionDone({
            id: 202,
            kind: "dismiss",
            error: "Cannot dismiss",
            merging: false,
          }),
        ),
        model((m) => {
          expect(m.trayRunning).toEqual({});
          expect(m.activePanel?.item.id).toBe(202);
          expect(m.answerComposer?.sendError).toBe("Cannot dismiss");
          expect(m.trayError).toBe("");
        }),
        message(Message.TrayActionClicked({ item: todo2, kind: "dismiss" })),
        model((m) => {
          expect(m.answerComposer?.sendError).toBe("");
        }),
        StoryCommand.expectExact(RunTrayAction({ item: todo2, kind: "dismiss" })),
        StoryCommand.resolve(
          RunTrayAction,
          Message.TrayActionDone({ id: 202, kind: "dismiss", error: "", merging: false }),
        ),
        model((m) => {
          expect(m.trayRunning).toEqual({});
          expect(m.activePanel).toBeNull();
          expect(m.todoList).toEqual({ repository: "owner/shop", pullRequest: 44 });
        }),
        message(Message.ReceivedTrayItems({ items: [todo1] })),
        model((m) => {
          expect(m.todoList).toEqual({ repository: "owner/shop", pullRequest: 44 });
        }),
        message(Message.CloseTodoList()),
        model((m) => {
          expect(m.todoList).toBeNull();
        }),
      );

      // hideInboxItem runs before RunTrayAction returns TrayActionDone, so ReceivedTrayItems can run first.
      const clicked = update(
        createTestModel({
          trayItems: [todo1, todo2],
          todoList: { repository: "owner/shop", pullRequest: 44 },
          activePanel: { type: "answer", item: todo2 },
        }),
        Message.TrayActionClicked({ item: todo2, kind: "dismiss" }),
      );
      const hidden = update(clicked.model, Message.ReceivedTrayItems({ items: [todo1] }));
      expect(hidden.model.activePanel).toBeNull();
      expect(hidden.model.closedPanel?.item.id).toBe(202);
      const done = update(
        hidden.model,
        Message.TrayActionDone({ id: 202, kind: "dismiss", error: "", merging: false }),
      );
      expect(done.model.trayRunning).toEqual({});
      expect(done.model.closedPanel).toBeNull();
      expect(done.model.todoList).toEqual({ repository: "owner/shop", pullRequest: 44 });
    });
  });

  describe("a task card", () => {
    it("gives the repository and the number of the task to the parent", () => {
      story(
        update,
        given(createTestModel()),
        message(Message.ClickedTaskCard({ repository: "owner/shop", number: 71 })),
        expectOutMessage(OutMessage.TaskOpened({ repository: "owner/shop", number: 71 })),
      );
    });
  });
});

const quoted = (owner: string, overrides: Partial<Model> = {}) =>
  createTestModel({
    selection: {
      owner,
      text: "free plan",
      context: "One free plan for staff",
      part: "Scope",
      rect: SelectionFixture.rect,
    },
    ...overrides,
  });

const selectionOf = (part: string) => ({
  source: "inbox" as const,
  text: "free plan",
  context: "One free plan for staff",
  reference: "#4",
  link: "https://github.com/test-org/test-repo/issues/4",
  part,
  messageId: 0,
  messageAuthor: "",
});

describe("a quote of an Inbox item", () => {
  const addClicked = message(toGotSelectionMessage(SelectionFixture.addClicked));
  const question = createInboxItem({
    id: 7,
    issue: 4,
    link: "https://github.com/test-org/test-repo/issues/4",
  });
  it("goes from the dialog body into the draft of the field of the dialog", () => {
    const base = createTestModel();
    story(
      update,
      given(
        quoted(dialogOwner("brief"), {
          activePanel: { type: "answer", item: question },
          answerComposer: { ...newAnswerComposer(base, question), draftLoaded: true },
        }),
      ),
      addClicked,
      StoryCommand.expectExact(
        Selection.AddSelection({ key: "answerDraft:7", selection: selectionOf("brief") }),
        Selection.ClearSelection(),
      ),
      StoryCommand.resolve(Selection.ClearSelection, SelectionFixture.clearedSelection),
      StoryCommand.resolve(Selection.AddSelection, SelectionFixture.addedSelection),
    );
  });

  it("goes from the dialog body of an item with no field into the chat draft", () => {
    story(
      update,
      given(quoted(dialogOwner("question"), { activePanel: { type: "details", item: question } })),
      addClicked,
      StoryCommand.expectExact(
        Selection.AddSelection({
          key: "chatDraft:test-org/test-repo/1428",
          selection: selectionOf("question"),
        }),
        Selection.ClearSelection(),
      ),
      StoryCommand.resolve(Selection.ClearSelection, SelectionFixture.clearedSelection),
      StoryCommand.resolve(Selection.AddSelection, SelectionFixture.addedSelection),
    );
  });

  it("goes from a question card into the chat draft", () => {
    story(
      update,
      given(
        quoted(cardOwner("question", 31), {
          messages: [{ ...createChatMessage(31, "How many seats?"), question }],
        }),
      ),
      addClicked,
      StoryCommand.expectExact(
        Selection.AddSelection({
          key: "chatDraft:test-org/test-repo/1428",
          selection: selectionOf("question"),
        }),
        Selection.ClearSelection(),
      ),
      StoryCommand.resolve(Selection.ClearSelection, SelectionFixture.clearedSelection),
      StoryCommand.resolve(Selection.AddSelection, SelectionFixture.addedSelection),
    );
  });

  it("goes from an action card into the chat draft with the reference of its pending item", () => {
    const action = createPendingAction();
    const item = createInboxItem({ ...question, id: 8, kind: "approval", action });
    story(
      update,
      given(
        quoted(cardOwner("action", 32), {
          messages: [{ ...createChatMessage(32, "Edit the Brief"), action }],
          trayItems: [item],
        }),
      ),
      addClicked,
      StoryCommand.expectExact(
        Selection.AddSelection({
          key: "chatDraft:test-org/test-repo/1428",
          selection: selectionOf("action"),
        }),
        Selection.ClearSelection(),
      ),
      StoryCommand.resolve(Selection.ClearSelection, SelectionFixture.clearedSelection),
      StoryCommand.resolve(Selection.AddSelection, SelectionFixture.addedSelection),
    );
  });

  it("goes from a chat message into the chat draft as before", () => {
    story(
      update,
      given(quoted("31", { messages: [createChatMessage(31, "How many seats?")] })),
      addClicked,
      StoryCommand.expectExact(
        Selection.AddSelection({
          key: "chatDraft:test-org/test-repo/1428",
          selection: {
            source: "message",
            text: "free plan",
            context: "One free plan for staff",
            reference: "",
            link: "",
            part: "",
            messageId: 31,
            messageAuthor: "Lead",
          },
        }),
        Selection.ClearSelection(),
      ),
      StoryCommand.resolve(Selection.ClearSelection, SelectionFixture.clearedSelection),
      StoryCommand.resolve(Selection.AddSelection, SelectionFixture.addedSelection),
    );
  });

  it("names the field in the label of the popover", () => {
    const approval = createInboxItem({ id: 8, kind: "approval", action: createPendingAction() });
    const owner = dialogOwner("brief");
    expect(quoteLabel(quoted(owner))).toBe("Add to chat");
    expect(quoteLabel(quoted("31", { activePanel: { type: "answer", item: question } }))).toBe(
      "Add to chat",
    );
    expect(quoteLabel(quoted(owner, { activePanel: { type: "details", item: question } }))).toBe(
      "Add to chat",
    );
    expect(quoteLabel(quoted(owner, { activePanel: { type: "answer", item: question } }))).toBe(
      "Add to the answer",
    );
    expect(quoteLabel(quoted(owner, { activePanel: { type: "answer", item: approval } }))).toBe(
      "Add to the note",
    );
  });
});

describe("the rows of a missing Workstream", () => {
  const missingSkills = createInboxItem({
    id: 21,
    kind: "missing skills",
    repository: "",
    workstreamRepository: "",
    workstream: 0,
    title: "Skills Workstream is missing",
  });
  const missingMaintenance = createInboxItem({
    ...missingSkills,
    id: 22,
    kind: "missing maintenance",
    title: "Maintenance Workstream is missing",
  });

  it("dismisses the item with the pending state until the server sends the new list", () => {
    story(
      update,
      given(createTestModel({ trayItems: [missingSkills] })),
      message(Message.TrayActionClicked({ item: missingSkills, kind: "dismiss" })),
      model((m) => {
        expect(m.trayRunning).toEqual({ 21: "dismiss" });
      }),
      StoryCommand.expectExact(RunTrayAction({ item: missingSkills, kind: "dismiss" })),
      StoryCommand.resolve(
        RunTrayAction,
        Message.TrayActionDone({ id: 21, kind: "dismiss", error: "", merging: false }),
      ),
      model((m) => {
        expect(m.trayRunning).toEqual({ 21: "dismiss" });
      }),
      message(Message.ReceivedTrayItems({ items: [] })),
      model((m) => {
        expect(m.trayItems).toEqual([]);
      }),
    );
  });

  it("reads the checkup when the first missing Workstream item comes", () => {
    const first = update(createTestModel(), Message.ReceivedTrayItems({ items: [missingSkills] }));
    expect(described(first.commands)).toEqual([
      { name: "FetchCheckup", args: { organization: "test-org" } },
    ]);

    const second = update(
      first.model,
      Message.ReceivedTrayItems({ items: [missingSkills, missingMaintenance] }),
    );
    expect(described(second.commands)).toEqual([]);

    const other = update(
      createTestModel(),
      Message.ReceivedTrayItems({ items: [createInboxItem({ id: 5 })] }),
    );
    expect(described(other.commands)).toEqual([]);
  });
});

describe("the read of a stored outbox item", () => {
  it("reads an item with no voiceSeconds as 0 and keeps the seconds of another", () => {
    const { voiceSeconds: _unused, ...older } = createQueuedItem("q1", "Plant roses");

    expect(readQueued(older).voiceSeconds).toBe(0);
    expect(readQueued({ ...older, voice: true, voiceSeconds: 12 })).toMatchObject({
      voice: true,
      voiceSeconds: 12,
    });
  });
});
