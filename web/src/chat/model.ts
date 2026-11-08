import { Result, Schema } from "effect";
import { StoreFailed } from "./errors";
import type { Flags } from "./flags";
import {
  AnswerSend,
  ChatMessage,
  DraftInboxItems,
  Failure,
  Images,
  InboxItem,
  Panel,
  Selection,
  TargetedVoiceRegion,
  TrayRunning,
  Waiting,
  Workstream,
} from "./schemas";

export const retryDelays = [2000, 5000, 10_000, 30_000];

export type { Failure, Waiting } from "./schemas";

// A read that the server made between the chat message of a question and its Inbox item has no question, and it can
// come after the live message that has it.
export function upsert(list: readonly ChatMessage[], message: ChatMessage): readonly ChatMessage[] {
  const known = list.find((other) => other.id === message.id);
  if (
    known &&
    (known.text.length > message.text.length ||
      (known.deliveredAt &&
        !message.deliveredAt &&
        !(message.failedAt && new Date(message.failedAt) > new Date(known.deliveredAt))) ||
      (known.stoppedAt && !message.stoppedAt) ||
      (known.question && !message.question) ||
      (known.question?.dismissedAt && !message.question?.dismissedAt))
  ) {
    return list;
  }
  return [...list.filter((other) => other.id !== message.id), message].toSorted(
    (a, b) => a.id - b.id,
  );
}

export const countsAsUnread = (message: ChatMessage) =>
  message.author !== "Owner" && message.author !== "Event" && message.author !== "Record";

const ChatModelSchema = Schema.Struct({
  organization: Schema.String,
  repository: Schema.String,
  workstream: Schema.Number,
  agent: Schema.String,
  unread: Schema.NullOr(Schema.Number),
  brief: Schema.NullOr(Workstream),
  trayItems: Schema.Array(InboxItem),
  workstreams: Schema.NullOr(Schema.Array(Workstream)),
  openUnread: Schema.NullOr(Schema.Number),
  messages: Schema.Array(ChatMessage),
  loaded: Schema.Boolean,
  older: Schema.Boolean,
  loadingOlder: Schema.Boolean,
  writing: Schema.Boolean,
  stoppable: Schema.Boolean,
  pausedUntil: Schema.NullOr(Schema.String),
  failure: Schema.String,
  error: Schema.NullOr(Schema.String),
  sendError: Schema.String,
  stopping: Schema.Boolean,
  queue: Schema.Array(Waiting),
  queueLoaded: Schema.Boolean,
  attempt: Schema.NullOr(Failure),
  failureCount: Schema.Number,
  retrySequence: Schema.Number,
  briefMeasured: Schema.Boolean,
  briefOpen: Schema.Boolean,
  briefLong: Schema.Boolean,
  openEvents: Schema.Array(Schema.Number),
  trayCollapsed: Schema.Boolean,
  trayError: Schema.String,
  trayRunning: Schema.Record(Schema.Number, TrayRunning),
  trayMerging: Schema.Record(Schema.Number, Schema.Boolean),
  trayRefusals: Schema.Record(Schema.Number, Schema.String),
  draftText: Schema.String,
  draftImages: Images,
  draftInboxItems: DraftInboxItems,
  draftLoaded: Schema.Boolean,
  voicePhase: Schema.Literals(["idle", "recording", "converting"]),
  voiceError: Schema.String,
  voiceRegion: Schema.NullOr(TargetedVoiceRegion),
  stopReady: Schema.Boolean,
  probingSend: Schema.Boolean,
  voiceCanResend: Schema.Boolean,
  waitingForSend: Schema.Boolean,
  closedPanel: Schema.NullOr(Panel),
  activePanel: Schema.NullOr(Panel),
  selectedOptions: Selection,
  answerText: Schema.String,
  answerLoaded: Schema.Boolean,
  answerSend: Schema.NullOr(AnswerSend),
  answerError: Schema.String,
  answerErrorId: Schema.Number,
  answerErrorAwaitsRow: Schema.Boolean,
  requesting: Schema.Array(Schema.Number),
  showRemovedText: Schema.Boolean,
  briefActionBrief: Schema.String,
});

export const Model = ChatModelSchema;
export type Model = Schema.Schema.Type<typeof Model>;

const savedTrayCollapsed = () =>
  Result.try({
    try: () => localStorage.getItem("tray") === "collapsed",
    catch: (cause) => new StoreFailed({ message: String(cause) }),
  }).pipe(Result.getOrElse(() => false));

export function initialModel(flags: Flags): Model {
  return {
    organization: flags.organization,
    repository: flags.repository,
    workstream: flags.workstream,
    agent: flags.agent,
    unread: null,
    brief: null,
    trayItems: [],
    workstreams: null,
    openUnread: null,
    messages: [],
    loaded: false,
    older: false,
    loadingOlder: false,
    writing: false,
    stoppable: false,
    pausedUntil: null,
    failure: "",
    error: null,
    sendError: "",
    stopping: false,
    queue: [],
    queueLoaded: false,
    attempt: null,
    failureCount: 0,
    retrySequence: 0,
    briefMeasured: false,
    briefOpen: false,
    briefLong: false,
    openEvents: [],
    trayCollapsed: savedTrayCollapsed(),
    trayError: "",
    trayRunning: {},
    trayMerging: {},
    trayRefusals: {},
    draftText: "",
    draftImages: [],
    draftInboxItems: [],
    draftLoaded: false,
    voicePhase: "idle",
    voiceError: "",
    voiceRegion: null,
    stopReady: false,
    probingSend: false,
    voiceCanResend: false,
    waitingForSend: false,
    closedPanel: null,
    activePanel: null,
    selectedOptions: {},
    answerText: "",
    answerLoaded: false,
    answerSend: null,
    answerError: "",
    answerErrorId: 0,
    answerErrorAwaitsRow: false,
    requesting: [],
    showRemovedText: false,
    briefActionBrief: "",
  };
}
