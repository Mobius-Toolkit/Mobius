import { Schema } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import {
  AnswerInboxItem,
  ApproveAction,
  ClickAttach,
  DoneInboxItem,
  EnqueueMessage,
  FetchActionBrief,
  FetchOlder,
  FetchPage,
  FitImages,
  Navigate,
  OpenChatWithItem,
  PostQueued,
  RejectAction,
  RemoveQueued,
  RemoveVoice,
  RequestLogin,
  ResendVoice,
  RunTrayAction,
  ResumeIssue,
  ResumeStoppedItem,
  SaveDraftImages,
  SaveDraftInboxItems,
  SaveDraftText,
  SaveTrayState,
  SeeChat,
  ShowBlockingTodo,
  StopChat,
  StopVoice,
  ToggleVoice,
  WaitToRetry,
} from "./commands";
import { InboxItemKind } from "@/api/api.gen";
import { answerDraftKey, chatDraftKey } from "@/lib/draft";
import { issueRef } from "@/lib/notice";
import { maxImages, tooManyImages } from "@/lib/images";
import { Message } from "./message";
type Running = "action" | "dismiss" | "merge";
import {
  countsAsUnread,
  hasNote,
  retryDelays,
  upsert,
  voiceBusy,
  type Model,
  type Waiting,
} from "./model";
import { InboxItem } from "./schemas";

const chatKey = (model: Model) =>
  chatDraftKey(model.organization, model.repository, model.workstream);

function isEmptyDraft(model: Model) {
  return (
    !model.draftText.trim() && model.draftImages.length === 0 && model.draftInboxItems.length === 0
  );
}

// The draft key of the field that a voice Message names: the field of the chat, or the field of the open answer panel.
function voiceKey(model: Model, target: "chat" | "answer") {
  if (target === "chat") {
    return chatKey(model);
  }
  return model.activePanel ? answerDraftKey(model.activePanel.item.id) : null;
}

function hasSelection(model: Model) {
  return Object.values(model.selectedOptions).some((options) => options.length > 0);
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
    voice: model.answerVoice?.state === "ready",
  };
  if (item.todo) {
    return [
      DoneInboxItem({
        ...target,
        notice: `Marked the todo ${item.todo.name || item.title} as done`,
        note: text,
      }),
    ];
  }
  if (item.kind === InboxItemKind.owner_action) {
    return [DoneInboxItem({ ...target, notice: "Marked the action as done", note: text })];
  }
  if (item.kind === InboxItemKind.stopped) {
    const notice = `Resumed ${issueRef(target.repository, item.issue, item.workstreamRepository)}`;
    return [
      text.trim()
        ? AnswerInboxItem({ ...target, answer: text, notice })
        : ResumeStoppedItem({
            id: target.id,
            repository: target.repository,
            issue: target.issue,
            notice,
          }),
    ];
  }
  const workstream = item.workstream || model.workstream;
  return [
    AnswerInboxItem({
      ...target,
      answer: selectionText(selected, item, text),
      notice:
        workstream === 0
          ? "Sent the answer to the Coordinator"
          : `Sent the answer to the Lead of #${workstream}`,
    }),
  ];
}

function clearedDraft(model: Model): Model {
  return {
    ...model,
    draftText: "",
    draftImages: [],
    draftInboxItems: [],
    draftVoice: null,
  };
}

function sendCommands(model: Model) {
  return [
    EnqueueMessage({
      organization: model.organization,
      repository: model.repository,
      workstream: model.workstream,
      key: chatKey(model),
      text: model.draftText,
      images: model.draftImages,
      inboxItems: model.draftInboxItems,
      voice: model.draftVoice?.state === "ready",
    }),
  ];
}

function withoutKey<T>(record: Record<number, T>, key: number): Record<number, T> {
  return Object.fromEntries(Object.entries(record).filter(([name]) => Number(name) !== key));
}

const sameInboxItem = Schema.toEquivalence(InboxItem);

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
  return model.requesting;
}

function without(model: Model, id: number) {
  return requestingOf(model).filter((other) => other !== id);
}

// A panel that opens again reads the draft of its own item: the Subscription of the answer draft starts again. The note
// of another panel that the Owner used in the meantime is not its note.
function reopened(before: Model, after: Model) {
  if (before.activePanel !== null || after.activePanel === null) {
    return { model: after, commands: noCommands };
  }
  return {
    model: {
      ...after,
      answerText: "",
      answerVoice: null,
      answerLoaded: false,
      selectedOptions: {},
    },
    commands: noCommands,
  };
}

function newestMessage(model: Model) {
  const messages = model.messages;
  return model.loaded && messages.length > 0 ? Math.max(...messages.map((m) => m.id)) : null;
}

function lastAgentMessage(model: Model) {
  return model.messages.findLast(
    (m) => m.author !== "Owner" && m.author !== "Event" && m.author !== "Record",
  )?.id;
}

// The page marks the messages as seen when the list has the first unread message. A failed call keeps the count, and
// the next message of the agent calls again.
function seesChat(model: Model) {
  const waiting =
    model.older &&
    model.openUnread !== null &&
    model.messages.filter(countsAsUnread).length < model.openUnread;
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
      const messages = model.messages;
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
        brief,
      },
    }),

    // The panel of a row goes away with the row, unless the request of the panel failed. A failed request brings the
    // row back, and a panel that closed meanwhile opens again at the end of the request.
    ReceivedTrayItems: ({ items }) => {
      const panel = model.activePanel;
      const rows = items.filter((item) => !item.action || item.action.state === "pending");
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
      const before = model.trayItems;
      const kept = (id: number) => {
        const old = before.find((item) => item.id === id);
        const next = items.find((item) => item.id === id);
        return old !== undefined && next !== undefined && sameInboxItem(old, next);
      };
      const keepKnown = <T>(record: Record<number, T>) =>
        Object.fromEntries(Object.entries(record).filter(([id]) => kept(Number(id))));
      return {
        model: {
          ...model,
          trayItems: items,
          trayMerging: keepKnown(model.trayMerging),
          trayRefusals: keepKnown(model.trayRefusals),
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
        workstreams,
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

    ReceivedLiveMessage: ({ message: liveMsg }) => {
      const messages = model.messages;
      if (messages.length > 0 && liveMsg.id < messages[0].id) {
        return { model };
      }
      const nextMessages = upsert(messages, liveMsg);
      const storedIds = new Set(nextMessages.map((m) => m.browserId));
      const nextQueue = model.queue.filter((item) => !storedIds.has(item.id));
      const commands = [];
      const storedInQueue = model.queue.filter((item) => storedIds.has(item.id));
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

    ReceivedLiveChatState: ({ state }) => {
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

    ReceivedLiveAction: ({ action }) => {
      const nextMessages = model.messages.map((m) =>
        m.action?.id === action.id ? { ...m, action } : m,
      );
      return {
        model: {
          ...model,
          messages: nextMessages,
        },
      };
    },

    ReceivedLiveQuestion: ({ question }) => {
      const nextMessages = model.messages.map((m) =>
        m.question?.id === question.id ? { ...m, question } : m,
      );
      return {
        model: {
          ...model,
          messages: nextMessages,
        },
      };
    },

    PageLoaded: ({ page, isInitial, sent }) => {
      const messages = model.messages;
      const nextMessages = page.messages.reduce(upsert, messages);
      const storedIds = new Set(nextMessages.map((m) => m.browserId));
      // A sent message stays on the screen until the end of the load that its own send started.
      const nextQueue = model.queue.filter((item) => !storedIds.has(item.id) && item.id !== sent);
      const commands = [];
      for (const item of model.queue.filter((i) => storedIds.has(i.id))) {
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

    OlderPageLoaded: ({ page }) => {
      const messages = model.messages;
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
          queue: model.queue.filter((item) => item.id !== sent),
        },
        commands,
      };
    },

    ListNearTop: () => {
      const messages = model.messages;
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

    QueueLoaded: ({ items: stored }) => {
      const storedIds = new Set(model.messages.map((m) => m.browserId));
      const queue = model.queue.concat(
        stored.filter(
          (item) => !storedIds.has(item.id) && !model.queue.some((other) => other.id === item.id),
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

    MessageRefused: ({ error }) => ({ model: { ...model, sendError: error } }),

    MessageQueued: ({ item }) => {
      const queue: Waiting[] = [...model.queue, item];
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
      const queue = model.queue.map((item) => (item.id === id ? { ...item, sent: true } : item));
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
      const waiting = model.queue;
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
      const next = model.queue.find((item) => !item.sent);
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
      const next = model.queue.find((item) => !item.sent);
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
      const queue = model.queue.filter((item) => item.id !== id);
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

    ReceivedDraft: ({ draft }) => ({
      model: {
        ...model,
        draftLoaded: true,
        draftText: draft.text,
        draftImages: draft.images,
        draftInboxItems: draft.inboxItems,
        draftVoice: draft.voice,
      },
    }),

    DraftTextChanged: ({ text }) => ({
      model: { ...model, draftText: text },
      commands: [SaveDraftText({ key: chatKey(model), text })],
    }),

    ImagesChosen: ({ files }) => ({
      model,
      commands: [
        FitImages({
          files,
          room: maxImages - model.draftImages.length,
        }),
      ],
    }),

    ImagesFitted: ({ images, problem }) => {
      const all = [...model.draftImages, ...images];
      const nextImages = all.slice(0, maxImages);
      return {
        model: {
          ...model,
          draftImages: nextImages,
          sendError: !problem && all.length > maxImages ? tooManyImages : problem,
        },
        commands: [SaveDraftImages({ key: chatKey(model), images: nextImages })],
      };
    },

    AttachClicked: () => ({ model, commands: [ClickAttach()] }),

    CompletedClickAttach: () => ({ model }),

    DraftImageRemoved: ({ position }) => {
      const nextImages = model.draftImages.filter((_, i) => i !== position);
      return {
        model: { ...model, draftImages: nextImages },
        commands: [SaveDraftImages({ key: chatKey(model), images: nextImages })],
      };
    },

    DraftInboxItemAdded: ({ item }) => {
      const nextItems = model.draftInboxItems.some((other) => other.id === item.id)
        ? model.draftInboxItems
        : [...model.draftInboxItems, item];
      return {
        model: { ...model, draftInboxItems: nextItems },
        commands: [SaveDraftInboxItems({ key: chatKey(model), inboxItems: nextItems })],
      };
    },

    DraftInboxItemRemoved: ({ id }) => {
      const nextItems = model.draftInboxItems.filter((item) => item.id !== id);
      return {
        model: { ...model, draftInboxItems: nextItems },
        commands: [SaveDraftInboxItems({ key: chatKey(model), inboxItems: nextItems })],
      };
    },

    // The Send button and Enter stop a voice run and send nothing.
    SendSubmitted: () => {
      if (model.voice.recording) {
        return { model, commands: [StopVoice()] };
      }
      if (isEmptyDraft(model) || voiceBusy(model.draftVoice)) {
        return { model };
      }
      return {
        model: { ...clearedDraft(model), sendError: "" },
        commands: sendCommands(model),
      };
    },

    CompletedStopVoice: () => ({ model }),

    StopReadyChanged: ({ ready }) => ({ model: { ...model, stopReady: ready } }),

    CompletedSaveDraft: () => ({ model }),

    EventToggled: ({ id }) => ({
      model: {
        ...model,
        openEvents: model.openEvents.includes(id)
          ? model.openEvents.filter((other) => other !== id)
          : [...model.openEvents, id],
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

    // The Subscription of the answer draft reads the draft of the panel when the panel opens. A panel that is open
    // already has its draft.
    OpenAnswerPanel: ({ item: inboxItem, workstreamTitle }) => {
      const sameAnswerPanel =
        model.activePanel?.type === "answer" && model.activePanel.item.id === inboxItem.id;
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
          ...(sameAnswerPanel
            ? {}
            : { answerText: "", answerVoice: null, answerLoaded: false, selectedOptions: {} }),
          answerError: model.answerErrorId === inboxItem.id ? "" : model.answerError,
          answerErrorId: model.answerErrorId === inboxItem.id ? 0 : model.answerErrorId,
          showRemovedText: false,
          briefActionBrief: "",
        },
        commands,
      };
    },

    OpenDetailsPanel: ({ item: inboxItem, workstreamTitle, fromTray }) => {
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

    ResumeClicked: ({ item }) => {
      if (item.id in model.trayRunning) {
        return { model };
      }
      return {
        model: {
          ...model,
          trayRunning: { ...model.trayRunning, [item.id]: "action" },
          trayError: "",
        },
        commands: [
          ResumeIssue({
            id: item.id,
            repository: item.repository || model.repository,
            issue: item.issue,
            workstreamRepository: item.workstreamRepository,
          }),
        ],
      };
    },

    ResumeCompleted: ({ id, error }) => {
      const trayItems = error ? model.trayItems : model.trayItems.filter((item) => item.id !== id);
      return {
        model: {
          ...model,
          trayRunning: error ? withoutKey(model.trayRunning, id) : model.trayRunning,
          trayError: error || "",
          trayItems,
        },
      };
    },

    // The row leaves the tray when the server sends the new list, so the button stays pending after a success.
    TrayActionClicked: ({ item, kind }) => {
      if (item.id in model.trayRunning) {
        return { model };
      }
      const running: Running =
        kind === "merge" ? "merge" : kind === "dismiss" ? "dismiss" : "action";
      return {
        model: {
          ...model,
          trayRunning: { ...model.trayRunning, [item.id]: running },
          trayRefusals: withoutKey(model.trayRefusals, item.id),
        },
        commands: [RunTrayAction({ item, kind })],
      };
    },

    TrayActionDone: ({ id, kind, error, merging }) => {
      const running = model.trayRunning;
      if (kind === "merge") {
        return {
          model: {
            ...model,
            trayRunning: withoutKey(running, id),
            trayMerging: { ...model.trayMerging, [id]: merging },
            trayRefusals: error ? { ...model.trayRefusals, [id]: error } : model.trayRefusals,
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
      const current = model.selectedOptions[questionIndex] ?? [];
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
            ...model.selectedOptions,
            [questionIndex]: next,
          },
        },
      };
    },

    AnswerTextChanged: ({ text }) => ({
      model: { ...model, answerText: text },
      commands: model.activePanel
        ? [SaveDraftText({ key: answerDraftKey(model.activePanel.item.id), text })]
        : [],
    }),

    ReceivedAnswerDraft: ({ id, draft }) =>
      model.activePanel?.type === "answer" && model.activePanel.item.id === id
        ? {
            model: {
              ...model,
              answerLoaded: true,
              answerText: draft.text,
              answerVoice: draft.voice,
            },
          }
        : { model },

    // The Send button and Enter stop a voice run and send nothing.
    AnswerSubmitted: () => {
      const panel = model.activePanel;
      if (!panel || panel.type !== "answer" || requestingOf(model).includes(panel.item.id)) {
        return { model };
      }
      if (model.voice.recording) {
        return { model, commands: [StopVoice()] };
      }
      const empty = !model.answerText.trim() && !hasSelection(model) && !hasNote(panel.item);
      if (empty || panel.item.action || voiceBusy(model.answerVoice)) {
        return { model };
      }
      return {
        model: {
          ...model,
          requesting: [...requestingOf(model), panel.item.id],
          answerError: "",
          answerErrorId: 0,
        },
        commands: answerCommands(model, panel.item, model.answerText, model.selectedOptions),
      };
    },

    AnswerCompleted: ({ id, error }) => {
      return reopened(model, {
        ...finishRequest(model, id, error),
        requesting: without(model, id),
      });
    },

    ActionDecided: ({ decision }) => {
      const panel = model.activePanel;
      if (!panel || requestingOf(model).includes(panel.item.id) || voiceBusy(model.answerVoice)) {
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
      reopened(model, { ...finishRequest(model, id, error), requesting: without(model, id) }),

    ActionCompleted: ({ id, action }) => {
      const isPending = action.state === "pending";
      const err = isPending ? action.error : "";
      const updatedMessages = model.messages.map((msg) =>
        msg.action?.id === action.id ? { ...msg, action } : msg,
      );
      const updatedTrayItems = isPending
        ? model.trayItems.map((item) =>
            item.action?.id === action.id ? { ...item, action } : item,
          )
        : model.trayItems.filter((item) => item.action?.id !== action.id);
      const withAction = (panel: Model["activePanel"]) =>
        panel && panel.item.id === id && err
          ? { ...panel, item: { ...panel.item, action: { ...panel.item.action, ...action } } }
          : panel;
      return reopened(model, {
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
      });
    },

    VoiceButtonClicked: ({ target }) => {
      const key = voiceKey(model, target);
      return key === null ? { model } : { model, commands: [ToggleVoice({ key })] };
    },

    VoiceResendClicked: ({ target }) => {
      const key = voiceKey(model, target);
      return key === null ? { model } : { model, commands: [ResendVoice({ key })] };
    },

    VoiceRemoveClicked: ({ target }) => {
      const key = voiceKey(model, target);
      return key === null ? { model } : { model, commands: [RemoveVoice({ key })] };
    },

    VoiceChanged: ({ voice }) => ({ model: { ...model, voice } }),

    VoiceTicked: ({ now }) => ({ model: { ...model, now } }),

    BlockingTodoClicked: ({ id }) => ({ model, commands: [ShowBlockingTodo({ id })] }),

    ChatWithItemClicked: ({ item }) => ({
      model,
      commands: [OpenChatWithItem({ item, organization: model.organization })],
    }),

    NavigateTo: ({ path }) => ({
      model: { ...model, activePanel: null },
      commands: [Navigate({ path })],
    }),

    CompletedFocusPanel: () => ({ model }),
    CompletedShowFile: () => ({ model }),
    CompletedNavigate: () => ({ model }),
    CompletedToggleVoice: () => ({ model }),
    CompletedResendVoice: () => ({ model }),
    CompletedRemoveVoice: () => ({ model }),
    CompletedScrollLive: () => ({ model }),
    CompletedShowBlockingTodo: () => ({ model }),
    CompletedOpenChat: () => ({ model }),
    CompletedSeeChat: () => ({ model }),
    CompletedRequestLogin: () => ({ model }),
  });
}
