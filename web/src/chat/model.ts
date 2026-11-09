import { Result, Schema } from "effect";
import { InboxItemKind } from "@/api/api.gen";
import { StoreFailed } from "@/lib/effects";
import type { Flags } from "./flags";
import {
  ChatMessage,
  type DraftInboxItem,
  DraftInboxItems,
  DraftVoice,
  Failure,
  Images,
  InboxItem,
  Panel,
  Selection,
  TrayRunning,
  VoiceState,
  Waiting,
  Workstream,
} from "./schemas";

export const retryDelays = [2000, 5000, 10_000, 30_000];

// A pull request, a todo and an owner action with a pull request show the pull request. Each other item shows its
// issue.
export function itemReference(item: InboxItem) {
  const ofPullRequest =
    item.kind === InboxItemKind.ready_for_review ||
    item.kind === InboxItemKind.todo ||
    (item.kind === InboxItemKind.owner_action && item.pullRequestUrl !== "");
  return {
    reference: !item.issue ? "" : ofPullRequest ? `PR #${item.pullRequest}` : `#${item.issue}`,
    link: ofPullRequest ? item.pullRequestUrl : item.link,
  };
}

// The panel of these items has an optional note, and the Send button needs no text.
export const hasNote = (item: InboxItem) =>
  item.todo !== null ||
  item.kind === InboxItemKind.owner_action ||
  item.kind === InboxItemKind.stopped;

export function draftInboxItem(item: InboxItem): DraftInboxItem {
  const { reference, link } = itemReference(item);
  return { id: item.id, reference, title: item.title, reason: item.reason, link };
}

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
      (known.action && !message.action) ||
      (known.question?.dismissedAt && !message.question?.dismissedAt))
  ) {
    return list;
  }
  return [...list.filter((other) => other.id !== message.id), message].toSorted(
    (a, b) => a.id - b.id,
  );
}

// A card that converts or has failed blocks each send.
export const voiceBusy = (voice: DraftVoice | null) =>
  voice?.state === "converting" || voice?.state === "failed";

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
  draftVoice: Schema.NullOr(DraftVoice),
  draftLoaded: Schema.Boolean,
  voice: VoiceState,
  now: Schema.Number,
  stopReady: Schema.Boolean,
  closedPanel: Schema.NullOr(Panel),
  activePanel: Schema.NullOr(Panel),
  selectedOptions: Selection,
  answerText: Schema.String,
  answerVoice: Schema.NullOr(DraftVoice),
  answerLoaded: Schema.Boolean,
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
    draftVoice: null,
    draftLoaded: false,
    voice: { recording: false, key: "", startedAt: 0, live: "", notice: { key: "", text: "" } },
    now: 0,
    stopReady: false,
    closedPanel: null,
    activePanel: null,
    selectedOptions: {},
    answerText: "",
    answerVoice: null,
    answerLoaded: false,
    answerError: "",
    answerErrorId: 0,
    answerErrorAwaitsRow: false,
    requesting: [],
    showRemovedText: false,
    briefActionBrief: "",
  };
}
