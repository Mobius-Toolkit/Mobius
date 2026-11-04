import { Schema } from "effect";
import type { ChatMessage, InboxItem, Workstream } from "@/api/api.gen";
import type { DraftInboxItem } from "@/lib/draft";
import type { Queued } from "@/lib/outbox";
import type { Flags } from "./flags";

export const retryDelays = [2000, 5000, 10_000, 30_000];

export type Waiting = Queued & { sent?: boolean };
export type Failure = { id: string; error: string; retryAt: number };

export function upsert(list: ChatMessage[], message: ChatMessage): ChatMessage[] {
  const known = list.find((other) => other.id === message.id);
  if (
    known &&
    (known.text.length > message.text.length ||
      (known.deliveredAt &&
        !message.deliveredAt &&
        !(message.failedAt && new Date(message.failedAt) > new Date(known.deliveredAt))) ||
      (known.stoppedAt && !message.stoppedAt) ||
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
  brief: Schema.NullOr(Schema.Unknown as Schema.Schema<Workstream>),
  trayItems: Schema.Unknown as Schema.Schema<InboxItem[]>,
  workstreams: Schema.NullOr(Schema.Unknown as Schema.Schema<Workstream[]>),
  openUnread: Schema.NullOr(Schema.Number),
  messages: Schema.Unknown as Schema.Schema<ChatMessage[]>,
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
  queue: Schema.Unknown as Schema.Schema<Waiting[]>,
  queueLoaded: Schema.Boolean,
  attempt: Schema.NullOr(Schema.Unknown as Schema.Schema<Failure>),
  failureCount: Schema.Number,
  retrySequence: Schema.Number,
  briefMeasured: Schema.Boolean,
  briefOpen: Schema.Boolean,
  briefLong: Schema.Boolean,
  openEvents: Schema.Unknown as Schema.Schema<number[]>,
  trayCollapsed: Schema.Boolean,
  trayError: Schema.String,
  trayRunning: Schema.Unknown as Schema.Schema<Record<number, "action" | "dismiss" | "merge">>,
  trayMerging: Schema.Unknown as Schema.Schema<Record<number, boolean>>,
  trayRefusals: Schema.Unknown as Schema.Schema<Record<number, string>>,
  draftText: Schema.String,
  draftImages: Schema.Unknown as Schema.Schema<File[]>,
  draftInboxItems: Schema.Unknown as Schema.Schema<DraftInboxItem[]>,
  draftLoaded: Schema.Boolean,
  voicePhase: Schema.Literals(["idle", "recording", "converting"]),
  voiceError: Schema.String,
  voiceRegion: Schema.NullOr(
    Schema.Unknown as Schema.Schema<{
      lead: string;
      trail: string;
      value: string;
      target: "chat" | "answer";
    }>,
  ),
  stopReady: Schema.Boolean,
  probingSend: Schema.Boolean,
  voiceCanResend: Schema.Boolean,
  waitingForSend: Schema.Boolean,
  closedPanel: Schema.NullOr(
    Schema.Unknown as Schema.Schema<{
      type: "answer" | "details";
      item: InboxItem;
      workstreamTitle?: string;
      fromTray?: boolean;
    }>,
  ),
  activePanel: Schema.NullOr(
    Schema.Unknown as Schema.Schema<{
      type: "answer" | "details";
      item: InboxItem;
      workstreamTitle?: string;
      fromTray?: boolean;
    }>,
  ),
  selectedOptions: Schema.Unknown as Schema.Schema<Record<number, number[]>>,
  answerText: Schema.String,
  answerLoaded: Schema.Boolean,
  answerWaiting: Schema.Boolean,
  answerError: Schema.String,
  answerErrorId: Schema.Number,
  answerSubmitting: Schema.Boolean,
  decisionSubmitting: Schema.Boolean,
  showRemovedText: Schema.Boolean,
  briefActionBrief: Schema.String,
});

export const Model = ChatModelSchema;
export type Model = Schema.Schema.Type<typeof Model>;

function savedTrayCollapsed() {
  try {
    return localStorage.getItem("tray") === "collapsed";
  } catch {
    return false;
  }
}

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
    answerWaiting: false,
    answerError: "",
    answerErrorId: 0,
    answerSubmitting: false,
    decisionSubmitting: false,
    showRemovedText: false,
    briefActionBrief: "",
  };
}
