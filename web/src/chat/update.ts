import { Option, Schema } from "effect";
import { Command } from "foldkit";
import * as Update from "foldkit/update";
import type { Return as UpdateReturn } from "foldkit/update";
import * as Composer from "@/composer";
import { OutMessage as ComposerOutMessage } from "@/composer/message";
import {
  AddSelection,
  AnswerInboxItem,
  ApproveAction,
  DoneInboxItem,
  FetchActionBrief,
  FetchOlder,
  FetchPage,
  ListQueued,
  Navigate,
  OpenChatWithItem,
  PostQueued,
  RejectAction,
  RemoveQueued,
  RequestLogin,
  RunTrayAction,
  ResumeIssue,
  ResumeStoppedItem,
  SaveTrayState,
  SeeChat,
  StopChat,
  WaitToRetry,
} from "./commands";
import { StopVoice } from "@/composer/commands";
import { InboxItemKind } from "@/api/api.gen";
import { issueRef } from "@/lib/notice";
import { newAnswerComposer } from "./composer";
import * as Selection from "@/ui/selection";
import { FetchCheckup } from "@/workstreamSetup/commands";
import { update as updateWorkstreamSetup } from "@/workstreamSetup/update";
import {
  Message,
  OutMessage,
  toGotAnswerComposerMessage,
  toGotComposerMessage,
  toGotSelectionMessage,
  toGotWorkstreamSetupMessage,
} from "./message";
type Running = "action" | "dismiss" | "merge";
import {
  authorLabel,
  countsAsUnread,
  hasNote,
  isMissingWorkstream,
  retryDelays,
  todoRowOf,
  upsert,
  type Model,
  type Waiting,
} from "./model";
import { InboxItem, type Queued } from "./schemas";

export function enqueue(model: Model, item: Queued): UpdateReturn<Model, Message> {
  if (model.queue.some((other) => other.id === item.id)) {
    return { model };
  }
  const queue: Waiting[] = [...model.queue, item];
  const next = queue.find((i) => !i.sent);
  const commands = [];
  if (next && !model.attempt && next.id === item.id) {
    const delay = retryDelays[0];
    commands.push(PostQueued({ item: next, delay }));
  }
  return { model: { ...model, queue }, commands };
}

export function stopChat(model: Model): UpdateReturn<Model, Message> {
  if (model.stopping) {
    return { model };
  }
  return {
    model: { ...model, stopping: true },
    commands: [
      StopChat({
        organization: model.organization,
        repository: model.repository,
        workstream: model.workstream,
      }),
    ],
  };
}

const foldComposer = Update.foldChild({
  update: Composer.update,
  read: (model: Model) => Option.some(model.composer),
  write: (model, composer) => ({ ...model, composer }),
  toParentMessage: toGotComposerMessage,
  foldOutMessage: (outMessage) =>
    ComposerOutMessage.match<Update.Step<Model, Message>>(outMessage, {
      StopClicked: () => stopChat,
      MessageEnqueued:
        ({ item }) =>
        (model) =>
          enqueue(model, item),
      DraftSubmitted: () => (model) => ({ model }),
    }),
});

function addSelection(model: Model, owner: string, text: string, context: string) {
  const message = model.messages.find((other) => String(other.id) === owner);
  if (!message) {
    return { model };
  }
  return {
    model,
    commands: [
      AddSelection({
        key: model.composer.draftKey,
        selection: {
          source: "message",
          text,
          context,
          reference: "",
          link: "",
          part: "",
          messageId: message.id,
          messageAuthor: authorLabel(message),
        },
      }),
    ],
  };
}

const foldSelection = Update.foldChild({
  update: Selection.update,
  read: (model: Model) => Option.some(model.selection),
  write: (model, selection) => ({ ...model, selection }),
  toParentMessage: toGotSelectionMessage,
  foldOutMessage: (outMessage) =>
    Selection.OutMessage.match<Update.Step<Model, Message>>(outMessage, {
      AddRequested:
        ({ owner, text, context }) =>
        (model) =>
          addSelection(model, owner, text, context),
    }),
});

const foldWorkstreamSetup = Update.foldChild({
  update: updateWorkstreamSetup,
  read: (model: Model) => Option.some(model.workstreamSetup),
  write: (model, workstreamSetup) => ({ ...model, workstreamSetup }),
  toParentMessage: toGotWorkstreamSetupMessage,
});

// A row of a missing Workstream needs the checkup of its organization.
const fetchesCheckup = (before: readonly InboxItem[], items: readonly InboxItem[]) =>
  !before.some(isMissingWorkstream) && items.some(isMissingWorkstream);

const foldSendError = Update.foldChild({
  update: (composer: Composer.Model, error: string) => Composer.setSendError(composer, error),
  read: (model: Model) => Option.some(model.composer),
  write: (model, composer) => ({ ...model, composer }),
  toParentMessage: toGotComposerMessage,
});

const foldAddInboxItem = Update.foldChild({
  update: Composer.addDraftInboxItem,
  read: (model: Model) => Option.some(model.composer),
  write: (model, composer) => ({ ...model, composer }),
  toParentMessage: toGotComposerMessage,
});

function withSendError(composer: Composer.Model | null, error: string) {
  return composer === null ? null : { ...composer, sendError: error };
}

// The parts of the draft of the answer field that go with the text.
function partsOf(composer: Composer.Model) {
  return {
    images: composer.draftImages,
    inboxItems: composer.draftInboxItems,
    selections: composer.draftSelections,
    voice: composer.draftVoice?.state === "ready",
  };
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
  composer: Composer.Model,
  selected: Record<number, number[]>,
) {
  const text = composer.draftText;
  const parts = partsOf(composer);
  const target = {
    id: item.id,
    repository: item.repository || model.repository,
    issue: item.issue,
  };
  if (item.todo) {
    return [
      DoneInboxItem({
        ...target,
        ...parts,
        notice: `Marked the todo ${item.todo.name || item.title} as done`,
        note: text,
      }),
    ];
  }
  if (item.kind === InboxItemKind.owner_action) {
    return [
      DoneInboxItem({ ...target, ...parts, notice: "Marked the action as done", note: text }),
    ];
  }
  if (item.kind === InboxItemKind.stopped) {
    const notice = `Resumed ${issueRef(target.repository, item.issue, item.workstreamRepository)}`;
    return [
      text.trim() || Composer.hasAttachments(composer)
        ? AnswerInboxItem({ ...target, ...parts, answer: text, notice })
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
      ...parts,
      answer: selectionText(selected, item, text),
      notice:
        workstream === 0
          ? "Sent the answer to the Coordinator"
          : `Sent the answer to the Lead of #${workstream}`,
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

// The request of the item ends. Only the panel of that item changes: a success closes it, and a failure opens it again.
// The panel of another item that the Owner opened in the meantime stays as it is.
function finishRequest(model: Model, id: number): Model {
  const ownActive = model.activePanel?.item.id === id;
  const ownClosed = model.closedPanel?.item.id === id;
  return {
    ...model,
    activePanel: ownActive ? null : model.activePanel,
    closedPanel: ownClosed ? null : model.closedPanel,
    todoList: todoRowOf(model.trayItems, model.todoList) ? model.todoList : null,
  };
}

function failRequest(model: Model, id: number): Model {
  const reopen = !model.activePanel && model.closedPanel?.item.id === id;
  return {
    ...model,
    activePanel: reopen ? model.closedPanel : model.activePanel,
    closedPanel: reopen ? null : model.closedPanel,
  };
}

function requestingOf(model: Model) {
  return model.requesting;
}

function without(model: Model, id: number) {
  return requestingOf(model).filter((other) => other !== id);
}

// A request of the item failed. The field of the open panel of the item shows the error. A panel that opens again gets
// a new field, which reads the draft of its own item: the note of another panel that the Owner used in the meantime
// is not its note.
function showError(before: Model, after: Model, id: number, error: string, awaitsRow: boolean) {
  const panel = after.activePanel;
  if (panel?.type !== "answer" || panel.item.id !== id) {
    return after;
  }
  const opensAgain = before.activePanel === null;
  return {
    ...after,
    answerComposer: opensAgain
      ? newAnswerComposer(after, panel.item, error)
      : withSendError(after.answerComposer, error),
    selectedOptions: opensAgain ? {} : after.selectedOptions,
    answerErrorAwaitsRow: awaitsRow,
  };
}

// The request of the item ended with the result of the server.
function endRequest(model: Model, id: number, error: string) {
  const ended = {
    ...(error ? failRequest(model, id) : finishRequest(model, id)),
    requesting: without(model, id),
  };
  return error ? showError(model, ended, id, error, true) : ended;
}

// The Send button and Enter of the field. The panel sends the draft of the field with the request of its item.
const submitAnswer: Update.Step<Model, Message> = (model) => {
  const panel = model.activePanel;
  const composer = model.answerComposer;
  if (
    !panel ||
    panel.type !== "answer" ||
    !composer ||
    requestingOf(model).includes(panel.item.id)
  ) {
    return { model };
  }
  const empty = Composer.isEmptyDraft(composer) && !hasSelection(model) && !hasNote(panel.item);
  if (empty || panel.item.action) {
    return { model };
  }
  return {
    model: {
      ...model,
      requesting: [...requestingOf(model), panel.item.id],
      answerComposer: withSendError(composer, ""),
    },
    commands: answerCommands(model, panel.item, composer, model.selectedOptions),
  };
};

const foldAnswerComposer = Update.foldChild({
  update: Composer.update,
  read: (model: Model) => Option.fromNullishOr(model.answerComposer),
  write: (model, answerComposer) => ({ ...model, answerComposer }),
  toParentMessage: toGotAnswerComposerMessage,
  foldOutMessage: (outMessage) =>
    ComposerOutMessage.match<Update.Step<Model, Message>>(outMessage, {
      StopClicked: () => (model) => ({ model }),
      MessageEnqueued: () => (model) => ({ model }),
      DraftSubmitted: () => submitAnswer,
    }),
});

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

type StepReturn = Update.ReturnWithOutMessage<Model, Message, OutMessage>;

// The field of the answer panel goes away with the panel.
function closeAnswerComposer(result: StepReturn): StepReturn {
  const { model } = result;
  return model.activePanel?.type !== "answer" && model.answerComposer !== null
    ? { ...result, model: { ...model, answerComposer: null } }
    : result;
}

export function update(model: Model, message: Message): StepReturn {
  const result = closeAnswerComposer(step(model, message));
  const next = result.model;
  const seen = lastAgentMessage(next);
  if (!seesChat(next) || seen === undefined || seen <= next.seenMessage) {
    return result;
  }
  return {
    ...result,
    model: { ...next, seenMessage: seen },
    commands: [
      ...(result.commands ?? []),
      SeeChat({
        organization: next.organization,
        repository: next.repository,
        workstream: next.workstream,
        message: seen,
      }),
    ],
  };
}

function step(model: Model, message: Message): StepReturn {
  return Message.match<StepReturn>(message, {
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
      const showsError =
        model.answerErrorAwaitsRow &&
        model.answerComposer !== null &&
        model.answerComposer.sendError !== "";
      const awaitsRow = panel !== null && showsError;
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
      const pendingTodo = (panel ?? model.closedPanel)?.item;
      const keepsTodoList =
        pendingTodo?.todo != null && (model.requesting.includes(pendingTodo.id) || showsError);
      return {
        model: {
          ...model,
          trayItems: items,
          trayMerging: keepKnown(model.trayMerging),
          trayRefusals: keepKnown(model.trayRefusals),
          answerErrorAwaitsRow:
            model.answerErrorAwaitsRow &&
            panel !== null &&
            !rows.some((row) => row.id === panel.item.id),
          activePanel: gone ? null : model.activePanel,
          closedPanel: gone ? panel : model.closedPanel,
          todoList: keepsTodoList || todoRowOf(items, model.todoList) ? model.todoList : null,
        },
        commands: fetchesCheckup(before, items)
          ? [
              Command.mapMessage(
                FetchCheckup({ organization: model.organization }),
                toGotWorkstreamSetupMessage,
              ),
            ]
          : [],
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
      const added = stored.filter(
        (item) => !storedIds.has(item.id) && !model.queue.some((other) => other.id === item.id),
      );
      const queue = model.queue.concat(added);
      const next = queue.find((item) => !item.sent);
      const commands = [];
      if (next && !model.attempt && added.some((item) => item.id === next.id)) {
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

    // After a success the button stays pending until the server says that the chat is not stoppable.
    StopChatCompleted: ({ error }) =>
      error === null ? { model } : foldSendError({ ...model, stopping: false }, error),

    GotComposerMessage: ({ message: composerMsg }) => foldComposer(model, composerMsg),

    DraftInboxItemAdded: ({ item }) => foldAddInboxItem(model, item),

    OutboxChanged: () => ({
      model,
      commands: [
        ListQueued({
          organization: model.organization,
          repository: model.repository,
          workstream: model.workstream,
        }),
      ],
    }),

    CompletedSaveDraft: () => ({ model }),

    GotSelectionMessage: ({ message: selectionMsg }) => foldSelection(model, selectionMsg),

    GotWorkstreamSetupMessage: ({ message: setupMsg }) => foldWorkstreamSetup(model, setupMsg),

    CompletedAddSelection: () => ({ model }),

    StopReadyChanged: ({ ready }) => ({ model: { ...model, stopReady: ready } }),

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

    // The new field of the panel reads the draft of the item when the panel opens. A panel that is open already has
    // its field.
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
          selectedOptions: sameAnswerPanel ? model.selectedOptions : {},
          answerComposer: sameAnswerPanel
            ? withSendError(model.answerComposer, "")
            : newAnswerComposer(model, inboxItem),
          answerErrorAwaitsRow: false,
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
        answerErrorAwaitsRow: false,
        activePanel: null,
        todoList: todoRowOf(model.trayItems, model.todoList) ? model.todoList : null,
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
      const inPanel = model.activePanel?.item.id === item.id;
      return {
        model: {
          ...model,
          trayRunning: { ...model.trayRunning, [item.id]: running },
          trayRefusals: withoutKey(model.trayRefusals, item.id),
          answerComposer: inPanel ? withSendError(model.answerComposer, "") : model.answerComposer,
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
      const ownActive = model.activePanel?.item.id === id;
      const ownClosed = model.closedPanel?.item.id === id;
      if (kind === "dismiss" && (ownActive || ownClosed)) {
        if (error) {
          return {
            model: showError(
              model,
              { ...failRequest(model, id), trayRunning: withoutKey(running, id) },
              id,
              error,
              false,
            ),
          };
        }
        return {
          model: {
            ...model,
            trayRunning: withoutKey(running, id),
            activePanel: ownActive ? null : model.activePanel,
            closedPanel: ownClosed ? null : model.closedPanel,
            todoList: todoRowOf(model.trayItems, model.todoList) ? model.todoList : null,
            showRemovedText: ownActive ? false : model.showRemovedText,
            briefActionBrief: ownActive ? "" : model.briefActionBrief,
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

    GotAnswerComposerMessage: ({ message: composerMsg }) => foldAnswerComposer(model, composerMsg),

    AnswerCompleted: ({ id, error }) => ({ model: endRequest(model, id, error) }),

    ActionDecided: ({ decision }) => {
      const panel = model.activePanel;
      const composer = model.answerComposer;
      if (
        !panel ||
        !composer ||
        requestingOf(model).includes(panel.item.id) ||
        Composer.voiceBusy(composer.draftVoice)
      ) {
        return { model };
      }
      const item = panel.item;
      const args = {
        actionId: item.action ? item.action.id : item.id,
        inboxItemId: item.action ? item.id : 0,
        repository: item.repository || model.repository,
        issue: item.issue,
        title: item.title || item.reason,
        text: composer.draftText,
        ...partsOf(composer),
      };
      return {
        model: {
          ...model,
          requesting: [...requestingOf(model), panel.item.id],
          answerComposer: withSendError(composer, ""),
        },
        commands: [
          Command.mapMessage(StopVoice(), toGotAnswerComposerMessage),
          decision === "approve" ? ApproveAction(args) : RejectAction(args),
        ],
      };
    },

    // The request failed, so the list does not change. The panel of the item opens again with the error and the note.
    ActionFailed: ({ id, error }) => ({ model: endRequest(model, id, error) }),

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
      return {
        model: endRequest(
          {
            ...model,
            activePanel: withAction(model.activePanel),
            closedPanel: withAction(model.closedPanel),
            messages: updatedMessages,
            trayItems: updatedTrayItems,
          },
          id,
          err,
        ),
      };
    },

    OpenTodoList: ({ repository, pullRequest }) => {
      const target = { repository, pullRequest };
      if (!todoRowOf(model.trayItems, target)) {
        return { model };
      }
      return { model: { ...model, todoList: target } };
    },

    CloseTodoList: () => ({ model: { ...model, todoList: null } }),

    CompletedStopPropagation: () => ({ model }),

    ChatWithItemClicked: ({ item }) => ({
      model,
      commands: [OpenChatWithItem({ item, organization: model.organization })],
    }),

    NavigateTo: ({ path }) => ({
      model: { ...model, activePanel: null, todoList: null },
      commands: [Navigate({ path })],
    }),

    CompletedFocusPanel: () => ({ model }),
    CompletedNavigate: () => ({ model }),
    CompletedOpenChat: () => ({ model }),
    ClickedTaskCard: ({ repository, number }) => ({
      model,
      outMessage: OutMessage.TaskOpened({ repository, number }),
    }),
    CompletedSeeChat: () => ({ model }),
    CompletedRequestLogin: () => ({ model }),
  });
}
