import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";

export const Message = defineMessageUnion({
  ReceivedUnread: { unread: Schema.NullOr(Schema.Number) },
  ReceivedBrief: { brief: Schema.NullOr(Schema.Unknown) },
  ReceivedTrayItems: { items: Schema.Unknown },
  ReceivedWorkstreams: { workstreams: Schema.NullOr(Schema.Unknown) },

  ConnectionOpened: {},
  ReceivedLiveMessage: { message: Schema.Unknown },
  ReceivedLiveChatState: { state: Schema.Unknown },
  ReceivedLiveAction: { action: Schema.Unknown },
  ReceivedLiveQuestion: { question: Schema.Unknown },
  RequestLoginFromSubscription: {},

  PageLoaded: {
    page: Schema.Unknown,
    isInitial: Schema.Boolean,
    sent: Schema.optional(Schema.String),
  },
  OlderPageLoaded: { page: Schema.Unknown },
  PageFailed: { error: Schema.String, sent: Schema.optional(Schema.String) },
  LoadOlderRequested: {},
  ListNearTop: {},

  QueueLoaded: { items: Schema.Unknown },
  QueueFailed: { error: Schema.String },
  MessageRefused: {
    error: Schema.String,
    text: Schema.String,
    images: Schema.Unknown,
    inboxItems: Schema.Unknown,
  },
  MessageQueued: { item: Schema.Unknown },
  SendAttemptSucceeded: { id: Schema.String },
  SendAttemptFailed: { id: Schema.String, error: Schema.String, retryAt: Schema.Number },
  RetryDue: { id: Schema.String, sequence: Schema.Number },
  RetryNowClicked: {},
  DiscardQueuedClicked: { id: Schema.String },
  CompletedRemoveQueued: { id: Schema.String },

  StopChatClicked: {},
  StopChatCompleted: { error: Schema.NullOr(Schema.String) },

  DraftLoaded: { draft: Schema.Unknown },
  DraftTextChanged: { text: Schema.String },
  ImagesChosen: { files: Schema.Unknown },
  ImagesFitted: { images: Schema.Unknown, problem: Schema.String },
  AttachClicked: {},
  CompletedClickAttach: {},
  DraftImageRemoved: { position: Schema.Number },
  DraftInboxItemAdded: { item: Schema.Unknown },
  DraftInboxItemRemoved: { id: Schema.Number },
  SendSubmitted: {},
  CompletedSaveDraft: {},

  BriefToggled: {},
  EventToggled: { id: Schema.Number },
  BriefMeasured: { long: Schema.Boolean },
  TrayToggled: {},

  OpenAnswerPanel: { item: Schema.Unknown, workstreamTitle: Schema.optional(Schema.String) },
  OpenDetailsPanel: {
    item: Schema.Unknown,
    workstreamTitle: Schema.optional(Schema.String),
    fromTray: Schema.optional(Schema.Boolean),
  },
  ClosePanel: {},
  CompletedFocusPanel: {},
  CompletedPortalToBody: {},
  OptionToggled: {
    questionIndex: Schema.Number,
    optionIndex: Schema.Number,
    multiple: Schema.Boolean,
  },
  AnswerTextChanged: { text: Schema.String },
  AnswerDraftLoaded: { text: Schema.String },
  CompletedSaveAnswerDraft: {},
  AnswerSubmitted: {},
  AnswerCompleted: { id: Schema.Number, result: Schema.Unknown },
  ActionDecided: { decision: Schema.String, text: Schema.String },
  ActionCompleted: { id: Schema.Number, action: Schema.Unknown },
  ActionFailed: { id: Schema.Number, error: Schema.String },
  TrayActionDone: {
    id: Schema.Number,
    kind: Schema.Literals(["action", "dismiss", "merge"]),
    error: Schema.String,
    merging: Schema.Boolean,
  },
  TrayActionClicked: {
    item: Schema.Unknown,
    kind: Schema.Literals(["dismiss", "merge", "send_again"]),
  },
  ToggleRemovedText: {},
  ActionBriefFetched: { brief: Schema.String },
  ResumeClicked: { item: Schema.Unknown },
  ResumeCompleted: { id: Schema.Number, error: Schema.optional(Schema.String) },

  VoiceButtonClicked: {},
  VoiceResendClicked: {},
  VoiceDiscardClicked: {},
  VoiceSendFinished: {
    text: Schema.optional(Schema.String),
    target: Schema.Literals(["chat", "answer"]),
  },
  VoicePhaseChanged: {
    phase: Schema.String,
    error: Schema.String,
    canResend: Schema.Boolean,
  },
  VoiceTextPlaced: {
    value: Schema.String,
    lead: Schema.String,
    trail: Schema.String,
    target: Schema.Literals(["chat", "answer"]),
  },
  VoiceProbed: { live: Schema.Boolean, target: Schema.Literals(["chat", "answer"]) },
  CompletedStopVoice: {},
  StopReadyChanged: { ready: Schema.Boolean },

  NavigateTo: { path: Schema.String },
  CompletedNavigate: {},
  CompletedSeeChat: {},
  CompletedRequestLogin: {},
});

export type Message = typeof Message.Type;
