import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import * as Composer from "@/composer";
import {
  ChatMessage,
  ChatPage,
  ChatState,
  Draft,
  DraftInboxItem,
  InboxItem,
  PendingAction,
  Queued,
  VoiceState,
  VoiceTarget,
  Workstream,
} from "./schemas";

export const Message = defineMessageUnion({
  ReceivedUnread: { unread: Schema.NullOr(Schema.Number) },
  ReceivedBrief: { brief: Schema.NullOr(Workstream) },
  ReceivedTrayItems: { items: Schema.Array(InboxItem) },
  ReceivedWorkstreams: { workstreams: Schema.NullOr(Schema.Array(Workstream)) },

  ConnectionOpened: {},
  ReceivedLiveMessage: { message: ChatMessage },
  ReceivedLiveChatState: { state: ChatState },
  ReceivedLiveAction: { action: PendingAction },
  ReceivedLiveQuestion: { question: InboxItem },

  PageLoaded: {
    page: ChatPage,
    isInitial: Schema.Boolean,
    sent: Schema.optional(Schema.String),
  },
  OlderPageLoaded: { page: ChatPage },
  PageFailed: { error: Schema.String, sent: Schema.optional(Schema.String) },
  ListNearTop: {},

  QueueLoaded: { items: Schema.Array(Queued) },
  QueueFailed: { error: Schema.String },
  SendAttemptSucceeded: { id: Schema.String },
  SendAttemptFailed: { id: Schema.String, error: Schema.String, retryAt: Schema.Number },
  RetryDue: { id: Schema.String, sequence: Schema.Number },
  RetryNowClicked: {},
  DiscardQueuedClicked: { id: Schema.String },
  CompletedRemoveQueued: { id: Schema.String },
  OutboxChanged: {},

  StopChatCompleted: { error: Schema.NullOr(Schema.String) },

  GotComposerMessage: { message: Composer.Message },
  DraftInboxItemAdded: { item: DraftInboxItem },
  CompletedSaveDraft: {},

  BriefToggled: {},
  EventToggled: { id: Schema.Number },
  BriefMeasured: { long: Schema.Boolean },
  TrayToggled: {},
  CompletedSaveTrayState: {},

  OpenAnswerPanel: { item: InboxItem, workstreamTitle: Schema.optional(Schema.String) },
  OpenDetailsPanel: {
    item: InboxItem,
    workstreamTitle: Schema.optional(Schema.String),
    fromTray: Schema.optional(Schema.Boolean),
  },
  ClosePanel: {},
  CompletedFocusPanel: {},
  CompletedShowFile: {},
  OptionToggled: {
    questionIndex: Schema.Number,
    optionIndex: Schema.Number,
    multiple: Schema.Boolean,
  },
  AnswerTextChanged: { text: Schema.String },
  ReceivedAnswerDraft: { id: Schema.Number, draft: Draft },
  AnswerSubmitted: {},
  AnswerCompleted: { id: Schema.Number, error: Schema.String },
  ActionDecided: { decision: Schema.String },
  ActionCompleted: { id: Schema.Number, action: PendingAction },
  ActionFailed: { id: Schema.Number, error: Schema.String },
  TrayActionDone: {
    id: Schema.Number,
    kind: Schema.Literals(["action", "dismiss", "merge"]),
    error: Schema.String,
    merging: Schema.Boolean,
  },
  TrayActionClicked: {
    item: InboxItem,
    kind: Schema.Literals(["dismiss", "merge", "send_again"]),
  },
  ToggleRemovedText: {},
  ActionBriefFetched: { brief: Schema.String },
  ResumeClicked: { item: InboxItem },
  ResumeCompleted: { id: Schema.Number, error: Schema.optional(Schema.String) },

  VoiceButtonClicked: { target: VoiceTarget },
  VoiceResendClicked: { target: VoiceTarget },
  VoiceRemoveClicked: { target: VoiceTarget },
  VoiceChanged: { voice: VoiceState },
  VoiceTicked: { now: Schema.Number },
  CompletedToggleVoice: {},
  CompletedStopVoice: {},
  CompletedResendVoice: {},
  CompletedRemoveVoice: {},
  CompletedScrollLive: {},
  StopReadyChanged: { ready: Schema.Boolean },

  BlockingTodoClicked: { id: Schema.Number },
  CompletedShowBlockingTodo: {},

  ChatWithItemClicked: { item: InboxItem },
  CompletedOpenChat: {},

  NavigateTo: { path: Schema.String },
  CompletedNavigate: {},
  CompletedSeeChat: {},
  CompletedRequestLogin: {},
});

export type Message = typeof Message.Type;

export const toGotComposerMessage = (message: Composer.Message): Message =>
  Message.GotComposerMessage({ message });
