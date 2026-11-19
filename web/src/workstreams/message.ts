import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { NeedsHuman, Unread, WaitingWorkstream, Workstream } from "./schemas";

export const Message = defineMessageUnion({
  ReceivedWorkstreams: {
    workstreams: Schema.Option(Schema.Array(Workstream)),
    waiting: Schema.Array(WaitingWorkstream),
    working: Schema.Array(Schema.String),
    error: Schema.String,
  },
  ReceivedUnread: { unread: Schema.Array(Unread) },
  ReceivedNeedsHuman: { needsHuman: Schema.Array(NeedsHuman) },
  NavigateTo: { path: Schema.String },
  CompletedNavigate: {},
});

export type Message = typeof Message.Type;
