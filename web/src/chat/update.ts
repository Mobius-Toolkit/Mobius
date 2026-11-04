import type { Return as UpdateReturn } from "foldkit/update";
import type {
  ChatInboxItem,
  ChatMessage,
  InboxItem,
  PendingAction,
  Question,
  Workstream,
} from "@/api/api.gen";
import type { DraftInboxItem } from "@/lib/draft";
import type { Queued } from "@/lib/outbox";
import {
  AnswerInboxItem,
  ApproveAction,
  ClickAttach,
  DiscardVoice,
  EnqueueMessage,
  FetchActionBrief,
  FetchOlder,
  FetchPage,
  FinishVoiceForSend,
  FitImages,
  Navigate,
  PostQueued,
  ProbeVoice,
  RejectAction,
  RemoveQueued,
  RequestLogin,
  ResendVoice,
  ResumeIssue,
  SaveDraft,
  SeeChat,
  StopChat,
  StopVoice,
  ToggleVoice,
} from "./commands";
import { maxImages } from "@/lib/images";
import { Message } from "./message";
import {
  countsAsUnread,
  retryDelays,
  upsert,
  type Failure,
  type Model,
  type Waiting,
} from "./model";

function isEmptyDraft(model: Model) {
  return (
    !model.draftText.trim() &&
    (model.draftImages as File[]).length === 0 &&
    (model.draftInboxItems as DraftInboxItem[]).length === 0
  );
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

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    ReceivedUnread: ({ unread }) => {
      const nextOpen = model.openUnread === null ? unread : model.openUnread;
      const messages = model.messages as ChatMessage[];
      const lastAgent = messages.findLast(
        (m) => m.author !== "Owner" && m.author !== "Event" && m.author !== "Record",
      )?.id;
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
      if (
        model.queueLoaded &&
        unread &&
        unread > 0 &&
        lastAgent !== undefined &&
        messages.filter(countsAsUnread).length >= unread
      ) {
        commands.push(
          SeeChat({
            organization: model.organization,
            repository: model.repository,
            workstream: model.workstream,
            message: lastAgent,
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

    ReceivedTrayItems: ({ items }) => ({
      model: {
        ...model,
        trayItems: items as InboxItem[],
      },
    }),

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
      return {
        model: {
          ...model,
          messages: nextMessages,
          queue: nextQueue,
        },
        commands,
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

    PageLoaded: ({ page: rawPage, isInitial }) => {
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
      const nextQueue = (model.queue as Waiting[]).filter((item) => !storedIds.has(item.id));
      const commands = [];
      for (const item of (model.queue as Waiting[]).filter((i) => storedIds.has(i.id))) {
        commands.push(RemoveQueued({ id: item.id }));
      }
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
        },
        commands,
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

    RequestLoginFromSubscription: () => ({
      model: {
        ...model,
        error: "login required",
      },
      commands: [RequestLogin()],
    }),

    PageFailed: ({ error }) => {
      const commands = error === "login required" ? [RequestLogin()] : [];
      return {
        model: {
          ...model,
          loadingOlder: false,
          error,
        },
        commands,
      };
    },

    LoadOlderRequested: () => {
      const messages = model.messages as ChatMessage[];
      if (model.loadingOlder || !model.older || messages.length === 0) {
        return { model };
      }
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
    },

    Scrolled: ({ scrollTop }) => {
      const messages = model.messages as ChatMessage[];
      if (model.older && !model.loadingOlder && scrollTop < 600 && messages.length > 0) {
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
        sendError: error,
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
      const failureCount = model.failureCount + 1;
      const attempt: Failure = { id, error, retryAt };
      return {
        model: {
          ...model,
          attempt,
          failureCount,
        },
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
        },
        commands,
      };
    },

    DiscardQueuedClicked: ({ id }) => {
      const queue = (model.queue as Waiting[]).filter((item) => item.id !== id);
      return {
        model: {
          ...model,
          queue,
          attempt: model.attempt?.id === id ? null : model.attempt,
        },
        commands: [RemoveQueued({ id })],
      };
    },

    CompletedRemoveQueued: () => ({ model }),

    StopChatClicked: () => ({
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
    }),

    StopChatCompleted: ({ error }) => ({
      model: {
        ...model,
        stopping: false,
        sendError: error || "",
      },
    }),

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
      const nextImages = [...(model.draftImages as File[]), ...(rawImages as File[])].slice(
        0,
        maxImages,
      );
      return {
        model: { ...model, draftImages: nextImages, sendError: problem },
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
        commands: [ProbeVoice({ region: model.voiceRegion })],
      };
    },

    VoiceProbed: ({ live }) => {
      if (live) {
        return {
          model: { ...model, probingSend: false, waitingForSend: true },
          commands: [FinishVoiceForSend()],
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

    TrayToggled: () => {
      const next = !model.trayCollapsed;
      try {
        localStorage.setItem("tray", next ? "collapsed" : "expanded");
      } catch {
        // works with no saved choice
      }
      return {
        model: {
          ...model,
          trayCollapsed: next,
        },
      };
    },

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
          answerError: "",
          selectedOptions: {},
          showRemovedText: false,
          briefActionBrief: "",
        },
        commands,
      };
    },

    OpenDetailsPanel: ({ item, workstreamTitle }) => {
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
          },
          answerError: "",
          showRemovedText: false,
          briefActionBrief: "",
        },
        commands,
      };
    },

    ClosePanel: () => ({
      model: {
        ...model,
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
      return {
        model: {
          ...model,
          resumingId: item.id,
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
          resumingId: null,
          trayError: error || "",
          trayItems,
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
      model: {
        ...model,
        answerText: text,
      },
    }),

    AnswerSubmitted: () => {
      const panel = model.activePanel;
      if (!panel || panel.type !== "answer") {
        return { model };
      }
      const item = panel.item;
      // Compose answer from selected options and answer text
      const optionsText = (item.questions || [])
        .flatMap((q: Question, qIdx: number) => {
          const sel = (model.selectedOptions as Record<number, number[]>)[qIdx] ?? [];
          return sel.map((idx) => q.options[idx]?.title).filter(Boolean);
        })
        .join("\n");
      const answer = [optionsText, model.answerText].filter(Boolean).join("\n\n");
      return {
        model: {
          ...model,
          answerSubmitting: true,
        },
        commands: [
          AnswerInboxItem({
            id: item.id,
            repository: item.repository || model.repository,
            issue: item.issue,
            workstream: item.workstream || model.workstream,
            answer,
          }),
        ],
      };
    },

    AnswerCompleted: ({ result: rawRes }) => {
      const res = rawRes as { error?: string };
      return {
        model: {
          ...model,
          answerSubmitting: false,
          activePanel: res.error ? model.activePanel : null,
          answerError: res.error || "",
        },
      };
    },

    ActionDecided: ({ decision, text }) => {
      const panel = model.activePanel;
      if (!panel) {
        return { model };
      }
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
          decisionSubmitting: true,
        },
        commands: [cmd],
      };
    },

    ActionCompleted: ({ action: rawAction }) => {
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
      const activePanel =
        isPending && err
          ? model.activePanel
            ? {
                ...model.activePanel,
                item: {
                  ...model.activePanel.item,
                  action: { ...model.activePanel.item.action, ...action },
                },
              }
            : null
          : null;
      return {
        model: {
          ...model,
          messages: updatedMessages,
          trayItems: updatedTrayItems,
          decisionSubmitting: false,
          activePanel,
          answerError: err,
        },
      };
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

    VoiceSendFinished: ({ text }) => {
      const region = model.voiceRegion;
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

    VoiceTextPlaced: ({ value, lead, trail }) => ({
      model: { ...model, draftText: value, voiceRegion: { lead, trail, value } },
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
    }),

    NavigateTo: ({ path }) => ({
      model,
      commands: [Navigate({ path })],
    }),

    CompletedNavigate: () => ({ model }),
    CompletedSeeChat: () => ({ model }),
    CompletedRequestLogin: () => ({ model }),
  });
}
