import type { Return as UpdateReturn } from "foldkit/update";
import type {
  ChatInboxItem,
  ChatMessage,
  InboxItem,
  PendingAction,
  Workstream,
} from "@/api/api.gen";
import type { DraftInboxItem } from "@/lib/draft";
import type { Queued } from "@/lib/outbox";
import {
  AnswerInboxItem,
  ApproveAction,
  ClickAttach,
  DiscardVoice,
  DoneInboxItem,
  EnqueueMessage,
  FetchActionBrief,
  FetchOlder,
  FetchPage,
  FinishVoiceForSend,
  FitImages,
  LoadAnswerDraft,
  Navigate,
  PostQueued,
  ProbeVoice,
  RejectAction,
  RemoveQueued,
  RequestLogin,
  ResendVoice,
  RunTrayAction,
  ResumeIssue,
  SaveAnswerDraft,
  SaveDraft,
  SaveTrayState,
  SeeChat,
  StopChat,
  StopVoice,
  ToggleVoice,
  WaitToRetry,
} from "./commands";
import { maxImages, tooManyImages } from "@/lib/images";
import { Message } from "./message";
type Running = "action" | "dismiss" | "merge";
import { countsAsUnread, retryDelays, upsert, type Model, type Waiting } from "./model";

function isEmptyDraft(model: Model) {
  return (
    !model.draftText.trim() &&
    (model.draftImages as File[]).length === 0 &&
    (model.draftInboxItems as DraftInboxItem[]).length === 0
  );
}

function hasSelection(model: Model) {
  return Object.values(model.selectedOptions as Record<number, number[]>).some(
    (options) => options.length > 0,
  );
}

// The answer has one block for each question with a selected option, and then the note.
function selectionText(selected: Record<number, number[]>, item: InboxItem, note: string) {
  return [
    ...item.questions.flatMap((question, index) => {
      const lines = question.options
        .filter((_, option) => selected[index]?.includes(option))
        .map((option) => `- ${option.title}`);
      return lines.length > 0 ? [[question.text, ...lines].join("\n")] : [];
    }),
    ...(note.trim() ? [note] : []),
  ].join("\n\n");
}

function answerCommands(
  model: Model,
  item: InboxItem,
  text: string,
  selected: Record<number, number[]>,
) {
  const target = {
    id: item.id,
    repository: item.repository || model.repository,
    issue: item.issue,
  };
  return item.todo
    ? [
        DoneInboxItem({
          ...target,
          name: item.todo.name || item.title,
          note: text,
        }),
      ]
    : [
        AnswerInboxItem({
          ...target,
          workstream: item.workstream || model.workstream,
          answer: selectionText(selected, item, text),
        }),
      ];
}

function clearedDraft(model: Model): Model {
  return {
    ...model,
    draftText: "",
    draftImages: [],
    draftInboxItems: [],
    voiceRegion: null,
  };
}

function sendCommands(model: Model, text: string) {
  const inboxItems: ChatInboxItem[] = (model.draftInboxItems as DraftInboxItem[]).map(
    ({ reference, title, reason, link }) => ({ reference, title, reason, link }),
  );
  return [
    EnqueueMessage({
      organization: model.organization,
      repository: model.repository,
      workstream: model.workstream,
      text,
      images: model.draftImages as File[],
      inboxItems,
    }),
    SaveDraft({
      organization: model.organization,
      repository: model.repository,
      workstream: model.workstream,
      draft: { text: "", images: [], inboxItems: [] },
    }),
  ];
}

function withoutKey<T>(record: Record<number, T>, key: number): Record<number, T> {
  return Object.fromEntries(Object.entries(record).filter(([name]) => Number(name) !== key));
}

const noCommands: Array<ReturnType<typeof PostQueued>> = [];

// When the server stored the item of the last failed attempt, the wait for the retry ends, and the next item goes.
function settleAttempt(model: Model, queue: Waiting[]) {
  const attempt = model.attempt;
  if (!attempt || queue.some((item) => item.id === attempt.id)) {
    return { fields: {}, commands: noCommands };
  }
  const next = queue.find((item) => !item.sent);
  return {
    fields: { attempt: null, failureCount: 0, retrySequence: model.retrySequence + 1 },
    commands: next ? [PostQueued({ item: next, delay: retryDelays[0] })] : noCommands,
  };
}

// The request of the item ends. Only the panel of that item changes: a success closes it, and a failure opens it again
// with the error. The panel of another item that the Owner opened in the meantime stays as it is.
function finishRequest(model: Model, id: number, error: string): Model {
  const ownActive = model.activePanel?.item.id === id;
  const ownClosed = model.closedPanel?.item.id === id;
  if (!error) {
    return {
      ...model,
      activePanel: ownActive ? null : model.activePanel,
      closedPanel: ownClosed ? null : model.closedPanel,
      answerError: model.answerErrorId === id ? "" : model.answerError,
      answerErrorId: model.answerErrorId === id ? 0 : model.answerErrorId,
    };
  }
  const reopen = !model.activePanel && ownClosed;
  return {
    ...model,
    activePanel: reopen ? model.closedPanel : model.activePanel,
    closedPanel: reopen ? null : model.closedPanel,
    answerError: error,
    answerErrorId: id,
    answerErrorAwaitsRow: true,
  };
}

function requestingOf(model: Model) {
  return model.requesting as number[];
}

function without(model: Model, id: number) {
  return requestingOf(model).filter((other) => other !== id);
}

// A panel that opens again reads the draft of its own item. The note of another panel that the Owner used in the
// meantime is not its note.
function reopened(before: Model, after: Model, id: number) {
  if (before.activePanel !== null || after.activePanel === null) {
    return { model: after, commands: noCommands };
  }
  return {
    model: { ...after, answerText: "", answerLoaded: false, selectedOptions: {} },
    commands: [LoadAnswerDraft({ id })],
  };
}

// The region of the voice text belongs to one field.
function regionOf(model: Model, target: "chat" | "answer") {
  const region = model.voiceRegion;
  return region && region.target === target
    ? { lead: region.lead, trail: region.trail, value: region.value }
    : null;
}

function newestMessage(model: Model) {
  const messages = model.messages as ChatMessage[];
  return model.loaded && messages.length > 0 ? Math.max(...messages.map((m) => m.id)) : null;
}

function lastAgentMessage(model: Model) {
  return (model.messages as ChatMessage[]).findLast(
    (m) => m.author !== "Owner" && m.author !== "Event" && m.author !== "Record",
  )?.id;
}

// The page marks the messages as seen when the list has the first unread message. A failed call keeps the count, and
// the next message of the agent calls again.
function seesChat(model: Model) {
  const waiting =
    model.older &&
    model.openUnread !== null &&
    (model.messages as ChatMessage[]).filter(countsAsUnread).length < model.openUnread;
  return (
    model.queueLoaded && !waiting && Boolean(model.unread) && lastAgentMessage(model) !== undefined
  );
}

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  const result = step(model, message);
  const next = result.model;
  const seen = lastAgentMessage(next);
  if (
    !seesChat(next) ||
    (seesChat(model) && lastAgentMessage(model) === seen && model.unread === next.unread)
  ) {
    return result;
  }
  return {
    model: next,
    commands: [
      ...(result.commands ?? []),
      SeeChat({
        organization: next.organization,
        repository: next.repository,
        workstream: next.workstream,
        message: seen ?? 0,
      }),
    ],
  };
}

function step(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    ReceivedUnread: ({ unread }) => {
      const nextOpen = model.openUnread === null ? unread : model.openUnread;
      const messages = model.messages as ChatMessage[];
      const commands = [];
      const waiting =
        model.older &&
        nextOpen !== null &&
        nextOpen > 0 &&
        messages.filter(countsAsUnread).length < nextOpen;
      if (waiting && !model.loadingOlder && messages.length > 0) {
        commands.push(
          FetchOlder({
            organization: model.organization,
            repository: model.repository,
            workstream: model.workstream,
            before: messages[0].id,
          }),
        );
      }
      return {
        model: {
          ...model,
          unread,
          openUnread: nextOpen,
          loadingOlder: waiting ? true : model.loadingOlder,
        },
        commands,
      };
    },

    ReceivedBrief: ({ brief }) => ({
      model: {
        ...model,
        brief: brief as Workstream | null,
      },
    }),

    // The panel of a row goes away with the row, unless the request of the panel failed. A failed request brings the
    // row back, and a panel that closed meanwhile opens again at the end of the request.
    ReceivedTrayItems: ({ items }) => {
      const panel = model.activePanel;
      const rows = (items as InboxItem[]).filter(
        (item) => !item.action || item.action.state === "pending",
      );
      // Only the panel of a tray row goes away with the row. The Details panel of a card stays.
      // A panel that shows the error of its request stays until a read shows its row again: the Port can come before
      // the row is back. After that read, a list without the row closes the panel.
      const awaitsRow =
        panel !== null &&
        model.answerErrorAwaitsRow &&
        model.answerErrorId === panel.item.id &&
        model.answerError !== "";
      const gone =
        panel !== null &&
        panel.item.id !== 0 &&
        (panel.type === "answer" || panel.fromTray === true) &&
        !awaitsRow &&
        !rows.some((row) => row.id === panel.item.id);
      // The local answer of a merge or a refusal belongs to the row that the Owner saw. A new read of the row, with other
      // data or without the row, replaces it.
      const before = model.trayItems as InboxItem[];
      const kept = (id: number) => {
        const old = before.find((item) => item.id === id);
        const next = (items as InboxItem[]).find((item) => item.id === id);
        return (
          old !== undefined && next !== undefined && JSON.stringify(old) === JSON.stringify(next)
        );
      };
      const keepKnown = <T>(record: Record<number, T>) =>
        Object.fromEntries(Object.entries(record).filter(([id]) => kept(Number(id))));
      return {
        model: {
          ...model,
          trayItems: items as InboxItem[],
          trayMerging: keepKnown(model.trayMerging as Record<number, boolean>),
          trayRefusals: keepKnown(model.trayRefusals as Record<number, string>),
          answerErrorAwaitsRow:
            model.answerErrorAwaitsRow && !rows.some((row) => row.id === model.answerErrorId),
          activePanel: gone ? null : model.activePanel,
          closedPanel: gone ? panel : model.closedPanel,
        },
      };
    },

    ReceivedWorkstreams: ({ workstreams }) => ({
      model: {
        ...model,
        workstreams: workstreams as Workstream[] | null,
      },
    }),

    ConnectionOpened: () => ({
      model,
      commands: [
        FetchPage({
          organization: model.organization,
          repository: model.repository,
          workstream: model.workstream,
          isInitial: !model.loaded,
          known: newestMessage(model),
          sent: null,
        }),
      ],
    }),

    ReceivedLiveMessage: ({ message: rawMsg }) => {
      const liveMsg = rawMsg as ChatMessage;
      const messages = model.messages as ChatMessage[];
      if (messages.length > 0 && liveMsg.id < messages[0].id) {
        return { model };
      }
      const nextMessages = upsert(messages, liveMsg);
      const storedIds = new Set(nextMessages.map((m) => m.browserId));
      const nextQueue = (model.queue as Waiting[]).filter((item) => !storedIds.has(item.id));
      const commands = [];
      const storedInQueue = (model.queue as Waiting[]).filter((item) => storedIds.has(item.id));
      for (const item of storedInQueue) {
        commands.push(RemoveQueued({ id: item.id }));
      }
      const settled = settleAttempt(model, nextQueue);
      return {
        model: {
          ...model,
          messages: nextMessages,
          queue: nextQueue,
          ...settled.fields,
        },
        commands: [...commands, ...settled.commands],
      };
    },

    ReceivedLiveChatState: ({ state: rawState }) => {
      const state = rawState as {
        writing: boolean;
        stoppable: boolean;
        pausedUntil: string | null;
        error: string;
      };
      return {
        model: {
          ...model,
          writing: state.writing,
          stoppable: state.stoppable,
          pausedUntil: state.pausedUntil,
          stopping: state.stoppable ? model.stopping : false,
          failure: state.error,
        },
      };
    },

    ReceivedLiveAction: ({ action: rawAction }) => {
      const action = rawAction as PendingAction;
      const nextMessages = (model.messages as ChatMessage[]).map((m) =>
        m.action?.id === action.id ? { ...m, action } : m,
      );
      return {
        model: {
          ...model,
          messages: nextMessages,
        },
      };
    },

    ReceivedLiveQuestion: ({ question: rawQuestion }) => {
      const question = rawQuestion as InboxItem;
      const nextMessages = (model.messages as ChatMessage[]).map((m) =>
        m.question?.id === question.id ? { ...m, question } : m,
      );
      return {
        model: {
          ...model,
          messages: nextMessages,
        },
      };
    },

    PageLoaded: ({ page: rawPage, isInitial, sent }) => {
      const page = rawPage as {
        messages: ChatMessage[];
        older: boolean;
        writing: boolean;
        stoppable: boolean;
        pausedUntil: string | null;
      };
      const messages = model.messages as ChatMessage[];
      const nextMessages = page.messages.reduce(upsert, messages);
      const storedIds = new Set(nextMessages.map((m) => m.browserId));
      // A sent message stays on the screen until the end of the load that its own send started.
      const nextQueue = (model.queue as Waiting[]).filter(
        (item) => !storedIds.has(item.id) && item.id !== sent,
      );
      const commands = [];
      for (const item of (model.queue as Waiting[]).filter((i) => storedIds.has(i.id))) {
        commands.push(RemoveQueued({ id: item.id }));
      }
      const settled = settleAttempt(model, nextQueue);
      const nextOlder = isInitial ? page.older : model.older;
      const waiting =
        nextOlder &&
        model.openUnread !== null &&
        model.openUnread > 0 &&
        nextMessages.filter(countsAsUnread).length < model.openUnread;
      if (waiting && nextMessages.length > 0) {
        commands.push(
          FetchOlder({
            organization: model.organization,
            repository: model.repository,
            workstream: model.workstream,
            before: nextMessages[0].id,
          }),
        );
      }
      return {
        model: {
          ...model,
          loaded: true,
          older: nextOlder,
          loadingOlder: waiting,
          writing: page.writing,
          stoppable: page.stoppable,
          pausedUntil: page.pausedUntil,
          stopping: page.stoppable ? model.stopping : false,
          messages: nextMessages,
          queue: nextQueue,
          ...settled.fields,
        },
        commands: [...commands, ...settled.commands],
      };
    },

    OlderPageLoaded: ({ page: rawPage }) => {
      const page = rawPage as { messages: ChatMessage[]; older: boolean };
      const messages = model.messages as ChatMessage[];
      const nextMessages = page.messages.reduce(upsert, messages);
      const waiting =
        page.older &&
        model.openUnread !== null &&
        model.openUnread > 0 &&
        nextMessages.filter(countsAsUnread).length < model.openUnread;
      const commands = [];
      if (waiting && nextMessages.length > 0) {
        commands.push(
          FetchOlder({
            organization: model.organization,
            repository: model.repository,
            workstream: model.workstream,
            before: nextMessages[0].id,
          }),
        );
      }
      return {
        model: {
          ...model,
          loadingOlder: waiting,
          older: page.older,
          messages: nextMessages,
        },
        commands,
      };
    },

    PageFailed: ({ error, sent }) => {
      const commands = error === "login required" ? [RequestLogin()] : [];
      return {
        model: {
          ...model,
          loadingOlder: false,
          error,
          queue: (model.queue as Waiting[]).filter((item) => item.id !== sent),
        },
        commands,
      };
    },

    ListNearTop: () => {
      const messages = model.messages as ChatMessage[];
      if (model.older && !model.loadingOlder && messages.length > 0) {
        return {
          model: {
            ...model,
            loadingOlder: true,
          },
          commands: [
            FetchOlder({
              organization: model.organization,
              repository: model.repository,
              workstream: model.workstream,
              before: messages[0].id,
            }),
          ],
        };
      }
      return { model };
    },

    QueueLoaded: ({ items: rawItems }) => {
      const stored = rawItems as Queued[];
      const storedIds = new Set((model.messages as ChatMessage[]).map((m) => m.browserId));
      const queue = (model.queue as Waiting[]).concat(
        stored.filter(
          (item) =>
            !storedIds.has(item.id) &&
            !(model.queue as Waiting[]).some((other) => other.id === item.id),
        ),
      );
      const next = queue.find((item) => !item.sent);
      const commands = [];
      if (next && !model.attempt) {
        const delay = retryDelays[0];
        commands.push(PostQueued({ item: next, delay }));
      }
      return {
        model: {
          ...model,
          queueLoaded: true,
          queue,
        },
        commands,
      };
    },

    QueueFailed: ({ error }) => ({
      model: {
        ...model,
        queueLoaded: true,
        error,
      },
    }),

    MessageRefused: ({ error, text, images, inboxItems }) => {
      const draftImages = images as File[];
      const draftItems = inboxItems as DraftInboxItem[];
      const restored = {
        text: model.draftText === text ? text : [text, model.draftText].filter(Boolean).join(" "),
        images: draftImages,
        inboxItems: [
          ...draftItems,
          ...(model.draftInboxItems as DraftInboxItem[]).filter(
            (item) => !draftItems.some(({ id }) => id === item.id),
          ),
        ],
      };
      return {
        model: {
          ...model,
          sendError: error,
          draftText: restored.text,
          draftImages: restored.images,
          draftInboxItems: restored.inboxItems,
        },
        commands: [
          SaveDraft({
            organization: model.organization,
            repository: model.repository,
            workstream: model.workstream,
            draft: restored,
          }),
        ],
      };
    },

    MessageQueued: ({ item: rawItem }) => {
      const item = rawItem as Queued;
      const queue: Waiting[] = [...(model.queue as Waiting[]), item];
      const next = queue.find((i) => !i.sent);
      const commands = [];
      if (next && !model.attempt && next.id === item.id) {
        const delay = retryDelays[0];
        commands.push(PostQueued({ item: next, delay }));
      }
      return {
        model: {
          ...model,
          queue,
        },
        commands,
      };
    },

    SendAttemptSucceeded: ({ id }) => {
      const queue = (model.queue as Waiting[]).map((item) =>
        item.id === id ? { ...item, sent: true } : item,
      );
      const next = queue.find((item) => !item.sent);
      const commands = [];
      commands.push(
        FetchPage({
          organization: model.organization,
          repository: model.repository,
          workstream: model.workstream,
          isInitial: false,
          known: newestMessage(model),
          sent: id,
        }),
      );
      if (next) {
        const delay = retryDelays[0];
        commands.push(PostQueued({ item: next, delay }));
      }
      return {
        model: {
          ...model,
          queue,
          attempt: null,
          failureCount: 0,
        },
        commands,
      };
    },

    SendAttemptFailed: ({ id, error, retryAt }) => {
      const waiting = model.queue as Waiting[];
      if (!waiting.some((item) => item.id === id)) {
        // The server stored the message while the request ran, so the item is not in the queue. No wait is needed.
        const next = waiting.find((item) => !item.sent);
        return {
          model,
          commands: next ? [PostQueued({ item: next, delay: retryDelays[0] })] : noCommands,
        };
      }
      const delay = retryDelays[Math.min(model.failureCount, retryDelays.length - 1)];
      return {
        model: {
          ...model,
          attempt: { id, error, retryAt },
          failureCount: model.failureCount + 1,
        },
        commands: [WaitToRetry({ id, delay, sequence: model.retrySequence })],
      };
    },

    RetryDue: ({ id, sequence }) => {
      const next = (model.queue as Waiting[]).find((item) => !item.sent);
      if (sequence !== model.retrySequence || !next || next.id !== id || model.attempt?.id !== id) {
        return { model };
      }
      const delay = retryDelays[Math.min(model.failureCount, retryDelays.length - 1)];
      return {
        model: { ...model, attempt: null },
        commands: [PostQueued({ item: next, delay })],
      };
    },

    RetryNowClicked: () => {
      const next = (model.queue as Waiting[]).find((item) => !item.sent);
      const commands = [];
      if (next) {
        const delay = retryDelays[Math.min(model.failureCount, retryDelays.length - 1)];
        commands.push(PostQueued({ item: next, delay }));
      }
      return {
        model: {
          ...model,
          attempt: null,
          retrySequence: model.retrySequence + 1,
        },
        commands,
      };
    },

    DiscardQueuedClicked: ({ id }) => {
      const queue = (model.queue as Waiting[]).filter((item) => item.id !== id);
      const retrying = model.attempt?.id === id;
      const next = queue.find((item) => !item.sent);
      return {
        model: {
          ...model,
          queue,
          attempt: retrying ? null : model.attempt,
          failureCount: retrying ? 0 : model.failureCount,
          retrySequence: retrying ? model.retrySequence + 1 : model.retrySequence,
        },
        commands: [
          RemoveQueued({ id }),
          ...(retrying && next ? [PostQueued({ item: next, delay: retryDelays[0] })] : []),
        ],
      };
    },

    CompletedRemoveQueued: () => ({ model }),

    StopChatClicked: () =>
      model.stopping
        ? { model }
        : {
            model: {
              ...model,
              stopping: true,
            },
            commands: [
              StopChat({
                organization: model.organization,
                repository: model.repository,
                workstream: model.workstream,
              }),
            ],
          },

    // After a success the button stays pending until the server says that the chat is not stoppable.
    StopChatCompleted: ({ error }) =>
      error === null ? { model } : { model: { ...model, stopping: false, sendError: error } },

    DraftLoaded: ({ draft: rawDraft }) => {
      const draft = rawDraft as {
        text: string;
        images: File[];
        inboxItems: DraftInboxItem[];
      };
      return {
        model: {
          ...model,
          draftLoaded: true,
          draftText: draft.text,
          draftImages: draft.images,
          draftInboxItems: draft.inboxItems,
        },
      };
    },

    DraftTextChanged: ({ text }) => {
      const draft = {
        text,
        images: model.draftImages as File[],
        inboxItems: model.draftInboxItems as DraftInboxItem[],
      };
      return {
        model: {
          ...model,
          draftText: text,
        },
        commands: [
          SaveDraft({
            organization: model.organization,
            repository: model.repository,
            workstream: model.workstream,
            draft,
          }),
        ],
      };
    },

    ImagesChosen: ({ files }) => ({
      model,
      commands: [
        FitImages({
          files: files as File[],
          room: maxImages - (model.draftImages as File[]).length,
        }),
      ],
    }),

    ImagesFitted: ({ images: rawImages, problem }) => {
      const all = [...(model.draftImages as File[]), ...(rawImages as File[])];
      const nextImages = all.slice(0, maxImages);
      return {
        model: {
          ...model,
          draftImages: nextImages,
          sendError: !problem && all.length > maxImages ? tooManyImages : problem,
        },
        commands: [
          SaveDraft({
            organization: model.organization,
            repository: model.repository,
            workstream: model.workstream,
            draft: {
              text: model.draftText,
              images: nextImages,
              inboxItems: model.draftInboxItems as DraftInboxItem[],
            },
          }),
        ],
      };
    },

    AttachClicked: () => ({ model, commands: [ClickAttach()] }),

    CompletedClickAttach: () => ({ model }),

    DraftImageRemoved: ({ position }) => {
      const nextImages = (model.draftImages as File[]).filter((_, i) => i !== position);
      const draft = {
        text: model.draftText,
        images: nextImages,
        inboxItems: model.draftInboxItems as DraftInboxItem[],
      };
      return {
        model: {
          ...model,
          draftImages: nextImages,
        },
        commands: [
          SaveDraft({
            organization: model.organization,
            repository: model.repository,
            workstream: model.workstream,
            draft,
          }),
        ],
      };
    },

    DraftInboxItemAdded: ({ item: rawItem }) => {
      const item = rawItem as DraftInboxItem;
      const nextItems = (model.draftInboxItems as DraftInboxItem[]).some(
        (other) => other.id === item.id,
      )
        ? (model.draftInboxItems as DraftInboxItem[])
        : [...(model.draftInboxItems as DraftInboxItem[]), item];
      const draft = {
        text: model.draftText,
        images: model.draftImages as File[],
        inboxItems: nextItems,
      };
      return {
        model: {
          ...model,
          draftInboxItems: nextItems,
        },
        commands: [
          SaveDraft({
            organization: model.organization,
            repository: model.repository,
            workstream: model.workstream,
            draft,
          }),
        ],
      };
    },

    DraftInboxItemRemoved: ({ id }) => {
      const nextItems = (model.draftInboxItems as DraftInboxItem[]).filter(
        (item) => item.id !== id,
      );
      const draft = {
        text: model.draftText,
        images: model.draftImages as File[],
        inboxItems: nextItems,
      };
      return {
        model: {
          ...model,
          draftInboxItems: nextItems,
        },
        commands: [
          SaveDraft({
            organization: model.organization,
            repository: model.repository,
            workstream: model.workstream,
            draft,
          }),
        ],
      };
    },

    SendSubmitted: () => {
      if (model.waitingForSend || model.probingSend || isEmptyDraft(model)) {
        return { model };
      }
      return {
        model: { ...model, probingSend: true },
        commands: [
          ProbeVoice({
            region: regionOf(model, "chat"),
            draftText: model.draftText,
            target: "chat",
          }),
        ],
      };
    },

    VoiceProbed: ({ live, target }) => {
      if (target === "answer") {
        const item = model.activePanel?.item;
        if (!item) {
          return { model: { ...model, probingSend: false } };
        }
        const selected = model.selectedOptions as Record<number, number[]>;
        if (live) {
          return {
            model: {
              ...model,
              probingSend: false,
              answerSend: {
                item,
                text: model.answerText,
                selected,
                region: regionOf(model, "answer"),
              },
            },
            commands: [FinishVoiceForSend({ target })],
          };
        }
        return {
          model: { ...model, probingSend: false, requesting: [...requestingOf(model), item.id] },
          commands: [StopVoice(), ...answerCommands(model, item, model.answerText, selected)],
        };
      }
      if (live) {
        return {
          model: { ...model, probingSend: false, waitingForSend: true },
          commands: [FinishVoiceForSend({ target })],
        };
      }
      return {
        model: { ...clearedDraft(model), probingSend: false, sendError: "" },
        commands: [StopVoice(), ...sendCommands(model, model.draftText)],
      };
    },

    CompletedStopVoice: () => ({ model }),

    StopReadyChanged: ({ ready }) => ({ model: { ...model, stopReady: ready } }),

    CompletedSaveDraft: () => ({ model }),

    EventToggled: ({ id }) => ({
      model: {
        ...model,
        openEvents: (model.openEvents as number[]).includes(id)
          ? (model.openEvents as number[]).filter((other) => other !== id)
          : [...(model.openEvents as number[]), id],
      },
    }),

    BriefToggled: () => ({
      model: {
        ...model,
        briefOpen: !model.briefOpen,
      },
    }),

    BriefMeasured: ({ long }) => ({
      model: {
        ...model,
        briefMeasured: true,
        briefLong: long,
      },
    }),

    TrayToggled: () => ({
      model: { ...model, trayCollapsed: !model.trayCollapsed },
      commands: [SaveTrayState({ collapsed: !model.trayCollapsed })],
    }),

    CompletedSaveTrayState: () => ({ model }),

    OpenAnswerPanel: ({ item, workstreamTitle }) => {
      const inboxItem = item as InboxItem;
      const commands = [];
      if (inboxItem.action?.tool === "edit_brief" && inboxItem.action.state === "pending") {
        commands.push(FetchActionBrief({ actionId: inboxItem.action.id }));
      }
      return {
        model: {
          ...model,
          activePanel: {
            type: "answer",
            item: inboxItem,
            workstreamTitle,
          },
          answerText: "",
          answerLoaded: false,
          selectedOptions: {},
          answerError: model.answerErrorId === inboxItem.id ? "" : model.answerError,
          answerErrorId: model.answerErrorId === inboxItem.id ? 0 : model.answerErrorId,
          showRemovedText: false,
          briefActionBrief: "",
          voiceRegion: model.voiceRegion?.target === "answer" ? null : model.voiceRegion,
        },
        commands: [...commands, LoadAnswerDraft({ id: inboxItem.id })],
      };
    },

    OpenDetailsPanel: ({ item, workstreamTitle, fromTray }) => {
      const inboxItem = item as InboxItem;
      const commands = [];
      if (inboxItem.action?.tool === "edit_brief" && inboxItem.action.state === "pending") {
        commands.push(FetchActionBrief({ actionId: inboxItem.action.id }));
      }
      return {
        model: {
          ...model,
          activePanel: {
            type: "details",
            item: inboxItem,
            workstreamTitle,
            fromTray,
          },
          selectedOptions: {},
          showRemovedText: false,
          briefActionBrief: "",
        },
        commands,
      };
    },

    ClosePanel: () => ({
      model: {
        ...model,
        answerError: model.answerErrorId === model.activePanel?.item.id ? "" : model.answerError,
        answerErrorId: model.answerErrorId === model.activePanel?.item.id ? 0 : model.answerErrorId,
        activePanel: null,
        showRemovedText: false,
        briefActionBrief: "",
      },
    }),

    ToggleRemovedText: () => ({
      model: {
        ...model,
        showRemovedText: !model.showRemovedText,
      },
    }),

    ActionBriefFetched: ({ brief }) => ({
      model: {
        ...model,
        briefActionBrief: brief,
      },
    }),

    ResumeClicked: ({ item: rawItem }) => {
      const item = rawItem as InboxItem;
      if (item.id in (model.trayRunning as Record<number, Running>)) {
        return { model };
      }
      return {
        model: {
          ...model,
          trayRunning: { ...(model.trayRunning as Record<number, Running>), [item.id]: "action" },
          trayError: "",
        },
        commands: [
          ResumeIssue({
            id: item.id,
            repository: item.repository || model.repository,
            issue: item.issue,
            workstreamRepository: model.repository,
          }),
        ],
      };
    },

    ResumeCompleted: ({ id, error }) => {
      const trayItems = error
        ? model.trayItems
        : (model.trayItems as InboxItem[]).filter((item) => item.id !== id);
      return {
        model: {
          ...model,
          trayRunning: error
            ? withoutKey(model.trayRunning as Record<number, Running>, id)
            : model.trayRunning,
          trayError: error || "",
          trayItems,
        },
      };
    },

    // The row leaves the tray when the server sends the new list, so the button stays pending after a success.
    TrayActionClicked: ({ item: rawItem, kind }) => {
      const item = rawItem as InboxItem;
      if (item.id in (model.trayRunning as Record<number, Running>)) {
        return { model };
      }
      const running: Running =
        kind === "merge" ? "merge" : kind === "dismiss" ? "dismiss" : "action";
      return {
        model: {
          ...model,
          trayRunning: { ...(model.trayRunning as Record<number, Running>), [item.id]: running },
          trayRefusals: withoutKey(model.trayRefusals as Record<number, string>, item.id),
        },
        commands: [RunTrayAction({ item, kind })],
      };
    },

    TrayActionDone: ({ id, kind, error, merging }) => {
      const running = model.trayRunning as Record<number, Running>;
      if (kind === "merge") {
        return {
          model: {
            ...model,
            trayRunning: withoutKey(running, id),
            trayMerging: { ...(model.trayMerging as Record<number, boolean>), [id]: merging },
            trayRefusals: error
              ? { ...(model.trayRefusals as Record<number, string>), [id]: error }
              : model.trayRefusals,
          },
        };
      }
      return {
        model: {
          ...model,
          trayRunning: error ? withoutKey(running, id) : running,
          trayError: error,
        },
      };
    },

    OptionToggled: ({ questionIndex, optionIndex, multiple }) => {
      const current = (model.selectedOptions as Record<number, number[]>)[questionIndex] ?? [];
      let next: number[];
      if (multiple) {
        next = current.includes(optionIndex)
          ? current.filter((i) => i !== optionIndex)
          : [...current, optionIndex];
      } else {
        next = current.includes(optionIndex) ? [] : [optionIndex];
      }
      return {
        model: {
          ...model,
          selectedOptions: {
            ...(model.selectedOptions as Record<number, number[]>),
            [questionIndex]: next,
          },
        },
      };
    },

    AnswerTextChanged: ({ text }) => ({
      model: { ...model, answerText: text },
      commands: model.activePanel ? [SaveAnswerDraft({ id: model.activePanel.item.id, text })] : [],
    }),

    AnswerDraftLoaded: ({ text }) => ({
      model: {
        ...model,
        answerLoaded: true,
        answerText: model.answerLoaded ? model.answerText : text,
      },
    }),

    CompletedSaveAnswerDraft: () => ({ model }),

    AnswerSubmitted: () => {
      const panel = model.activePanel;
      if (
        !panel ||
        panel.type !== "answer" ||
        model.answerSend?.item.id === panel.item.id ||
        model.probingSend ||
        requestingOf(model).includes(panel.item.id)
      ) {
        return { model };
      }
      const empty = !model.answerText.trim() && !hasSelection(model) && !panel.item.todo;
      if (empty || panel.item.action) {
        return { model };
      }
      return {
        model: { ...model, probingSend: true, answerError: "", answerErrorId: 0 },
        commands: [
          ProbeVoice({
            region: regionOf(model, "answer"),
            draftText: model.answerText,
            target: "answer",
          }),
        ],
      };
    },

    AnswerCompleted: ({ id, result: rawRes }) => {
      const res = rawRes as { error?: string };
      return reopened(
        model,
        { ...finishRequest(model, id, res.error || ""), requesting: without(model, id) },
        id,
      );
    },

    ActionDecided: ({ decision }) => {
      const panel = model.activePanel;
      if (!panel || requestingOf(model).includes(panel.item.id)) {
        return { model };
      }
      const text = model.answerText;
      const item = panel.item;
      const actionId = item.action ? item.action.id : item.id;
      const inboxItemId = item.action ? item.id : 0;
      const title = item.title || item.reason;
      const cmd =
        decision === "approve"
          ? ApproveAction({
              actionId,
              inboxItemId,
              repository: item.repository || model.repository,
              issue: item.issue,
              title,
              text,
            })
          : RejectAction({
              actionId,
              inboxItemId,
              repository: item.repository || model.repository,
              issue: item.issue,
              title,
              text,
            });
      return {
        model: {
          ...model,
          requesting: [...requestingOf(model), panel.item.id],
          answerError: "",
          answerErrorId: 0,
        },
        commands: [StopVoice(), cmd],
      };
    },

    // The request failed, so the list does not change. The panel of the item opens again with the error and the note.
    ActionFailed: ({ id, error }) =>
      reopened(model, { ...finishRequest(model, id, error), requesting: without(model, id) }, id),

    ActionCompleted: ({ id, action: rawAction }) => {
      const action = rawAction as PendingAction & { error?: string };
      const isPending = action.state === "pending";
      const err = isPending ? action.error || "" : "";
      const updatedMessages = (model.messages as ChatMessage[]).map((msg) =>
        msg.action?.id === action.id ? { ...msg, action } : msg,
      );
      const updatedTrayItems = isPending
        ? (model.trayItems as InboxItem[]).map((item) =>
            item.action?.id === action.id ? { ...item, action } : item,
          )
        : (model.trayItems as InboxItem[]).filter((item) => item.action?.id !== action.id);
      const withAction = (panel: Model["activePanel"]) =>
        panel && panel.item.id === id && err
          ? { ...panel, item: { ...panel.item, action: { ...panel.item.action, ...action } } }
          : panel;
      return reopened(
        model,
        {
          ...finishRequest(
            {
              ...model,
              activePanel: withAction(model.activePanel),
              closedPanel: withAction(model.closedPanel),
            },
            id,
            err,
          ),
          messages: updatedMessages,
          trayItems: updatedTrayItems,
          requesting: without(model, id),
        },
        id,
      );
    },

    VoiceButtonClicked: () => ({ model, commands: [ToggleVoice()] }),

    VoiceResendClicked: () => ({
      model: {
        ...model,
        voicePhase: "converting",
      },
      commands: [ResendVoice()],
    }),

    VoiceDiscardClicked: () => ({
      model: {
        ...model,
        voiceError: "",
        voiceCanResend: false,
      },
      commands: [DiscardVoice()],
    }),

    VoiceSendFinished: ({ text, target }) => {
      if (target === "answer") {
        const send = model.answerSend;
        if (!send) {
          return { model };
        }
        const sent = !send.region
          ? send.text
          : text === undefined
            ? send.region.value
            : send.region.lead + text + send.region.trail;
        return {
          model: {
            ...model,
            answerSend: null,
            requesting: [...requestingOf(model), send.item.id],
          },
          commands: answerCommands(model, send.item, sent, send.selected),
        };
      }
      const region = regionOf(model, target);
      const sent = !region
        ? model.draftText
        : text === undefined
          ? region.value
          : region.lead + text + region.trail;
      return {
        model: { ...clearedDraft(model), waitingForSend: false, sendError: "" },
        commands: sendCommands(model, sent),
      };
    },

    VoicePhaseChanged: ({ phase, error, canResend }) => ({
      model: {
        ...model,
        voicePhase: phase as "idle" | "recording" | "converting",
        voiceError: error,
        voiceCanResend: canResend,
      },
    }),

    VoiceTextPlaced: ({ value, lead, trail, target }) =>
      target === "answer"
        ? {
            model: { ...model, answerText: value, voiceRegion: { lead, trail, value, target } },
            commands: model.activePanel
              ? [SaveAnswerDraft({ id: model.activePanel.item.id, text: value })]
              : [],
          }
        : {
            model: { ...model, draftText: value, voiceRegion: { lead, trail, value, target } },
            commands: [
              SaveDraft({
                organization: model.organization,
                repository: model.repository,
                workstream: model.workstream,
                draft: {
                  text: value,
                  images: model.draftImages as File[],
                  inboxItems: model.draftInboxItems as DraftInboxItem[],
                },
              }),
            ],
          },

    NavigateTo: ({ path }) => ({
      model: { ...model, activePanel: null },
      commands: [Navigate({ path })],
    }),

    CompletedFocusPanel: () => ({ model }),
    CompletedShowFile: () => ({ model }),
    CompletedNavigate: () => ({ model }),
    CompletedSeeChat: () => ({ model }),
    CompletedRequestLogin: () => ({ model }),
  });
}
