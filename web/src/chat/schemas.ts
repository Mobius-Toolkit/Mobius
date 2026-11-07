import { Schema } from "effect";
import { GetChatResponse, ListInboxResponse, ListWorkstreamsResponse } from "@/api/schemas.gen";

export const ChatPage = GetChatResponse.fields.data;

export const ChatMessage = ChatPage.fields.messages.value;
export const ChatInboxItem = ChatMessage.fields.inboxItems.value;
export const InboxItem = ListInboxResponse.fields.data.value;
export const PendingAction = InboxItem.fields.action.members[0];
export const Workstream = ListWorkstreamsResponse.fields.data.value;

export const ChatState = Schema.Struct({
  error: Schema.String,
  organization: Schema.String,
  pausedUntil: Schema.NullOr(Schema.String),
  repository: Schema.String,
  stoppable: Schema.Boolean,
  workstream: Schema.Number,
  writing: Schema.Boolean,
});

export const DraftInboxItem = Schema.Struct({ ...ChatInboxItem.fields, id: Schema.Number });
export const DraftInboxItems = Schema.mutable(Schema.Array(DraftInboxItem));
export const Images = Schema.mutable(Schema.Array(Schema.instanceOf(File)));
export const Draft = Schema.Struct({
  text: Schema.String,
  images: Images,
  inboxItems: DraftInboxItems,
});
export const Queued = Schema.Struct({
  id: Schema.String,
  organization: Schema.String,
  repository: Schema.String,
  workstream: Schema.Number,
  text: Schema.String,
  images: Images,
  inboxItems: Schema.mutable(Schema.Array(ChatInboxItem)),
  time: Schema.String,
});
export const Waiting = Schema.Struct({ ...Queued.fields, sent: Schema.optional(Schema.Boolean) });
export const Failure = Schema.Struct({
  id: Schema.String,
  error: Schema.String,
  retryAt: Schema.Number,
});

export const VoiceTarget = Schema.Literals(["chat", "answer"]);
export const VoiceRegion = Schema.Struct({
  lead: Schema.String,
  trail: Schema.String,
  value: Schema.String,
});
export const TargetedVoiceRegion = Schema.Struct({ ...VoiceRegion.fields, target: VoiceTarget });

export const Panel = Schema.Struct({
  type: Schema.Literals(["answer", "details"]),
  item: InboxItem,
  workstreamTitle: Schema.optional(Schema.String),
  fromTray: Schema.optional(Schema.Boolean),
});
export const Selection = Schema.Record(Schema.Number, Schema.mutable(Schema.Array(Schema.Number)));
export const AnswerSend = Schema.Struct({
  item: InboxItem,
  text: Schema.String,
  selected: Selection,
  region: Schema.NullOr(VoiceRegion),
});

export const TrayRunning = Schema.Literals(["action", "dismiss", "merge"]);

export type ChatMessage = typeof ChatMessage.Type;
export type ChatInboxItem = typeof ChatInboxItem.Type;
export type InboxItem = typeof InboxItem.Type;
export type PendingAction = typeof PendingAction.Type;
export type Workstream = typeof Workstream.Type;
export type ChatState = typeof ChatState.Type;
export type DraftInboxItem = typeof DraftInboxItem.Type;
export type Draft = typeof Draft.Type;
export type Queued = typeof Queued.Type;
export type Waiting = typeof Waiting.Type;
export type Failure = typeof Failure.Type;
export type Panel = typeof Panel.Type;
