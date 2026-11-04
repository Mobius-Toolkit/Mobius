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

  PageLoaded: { page: Schema.Unknown, isInitial: Schema.Boolean },
  OlderPageLoaded: { page: Schema.Unknown },
  PageFailed: { error: Schema.String },
  LoadOlderRequested: {},
  Scrolled: { scrollTop: Schema.Number },

  QueueLoaded: { items: Schema.Unknown },
  QueueFailed: { error: Schema.String },
  MessageQueued: { item: Schema.Unknown },
  SendAttemptSucceeded: { id: Schema.String },
  SendAttemptFailed: { id: Schema.String, error: Schema.String, retryAt: Schema.Number },
  RetryNowClicked: {},
  DiscardQueuedClicked: { id: Schema.String },
  CompletedRemoveQueued: { id: Schema.String },

  StopChatClicked: {},
  StopChatCompleted: { error: Schema.NullOr(Schema.String) },

  DraftLoaded: { draft: Schema.Unknown },
  DraftTextChanged: { text: Schema.String },
  DraftImagesAdded: { images: Schema.Unknown },
  DraftImageRemoved: { position: Schema.Number },
  DraftInboxItemAdded: { item: Schema.Unknown },
  DraftInboxItemRemoved: { id: Schema.Number },
  SendSubmitted: {},
  CompletedSaveDraft: {},

  BriefToggled: {},
  BriefMeasured: { long: Schema.Boolean },
  TrayToggled: {},

  OpenAnswerPanel: { item: Schema.Unknown, workstreamTitle: Schema.optional(Schema.String) },
  OpenDetailsPanel: { item: Schema.Unknown, workstreamTitle: Schema.optional(Schema.String) },
  ClosePanel: {},
  OptionToggled: {
    questionIndex: Schema.Number,
    optionIndex: Schema.Number,
    multiple: Schema.Boolean,
  },
  AnswerTextChanged: { text: Schema.String },
  AnswerSubmitted: {},
  AnswerCompleted: { result: Schema.Unknown },
  ActionDecided: { decision: Schema.String, text: Schema.String },
  ActionCompleted: { action: Schema.Unknown },
  ToggleRemovedText: {},
  ActionBriefFetched: { brief: Schema.String },
  ResumeClicked: { item: Schema.Unknown },
  ResumeCompleted: { id: Schema.Number, error: Schema.optional(Schema.String) },

  VoiceButtonClicked: {},
  VoiceResendClicked: {},
  VoiceDiscardClicked: {},
  VoiceSendFinished: { text: Schema.optional(Schema.String) },
  VoicePhaseChanged: {
    phase: Schema.String,
    error: Schema.String,
    canResend: Schema.Boolean,
  },
  VoiceLiveTextChanged: { text: Schema.String, first: Schema.Boolean },
  VoiceFinalTextReceived: { text: Schema.String, live: Schema.Boolean },

  NavigateTo: { path: Schema.String },
  CompletedNavigate: {},
  CompletedSeeChat: {},
  CompletedRequestLogin: {},
});

export type Message = typeof Message.Type;
