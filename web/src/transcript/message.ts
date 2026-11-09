import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { Line } from "./schemas";

export const Message = defineMessageUnion({
  StreamOpened: {},
  StreamClosed: {},
  StreamRetried: {},
  LineReceived: { line: Line },
  Loaded: { lines: Schema.Array(Line), older: Schema.Boolean, initial: Schema.Boolean },
  OlderLoaded: { lines: Schema.Array(Line), older: Schema.Boolean },
  LoadFailed: { error: Schema.String },
  LoginRequired: {},
  NearTop: {},
  ArrivedWhileAway: {},
  ReachedEnd: {},
  JumpedToEnd: {},
  ToggledOpen: { id: Schema.Number },
  ToggledRaw: { id: Schema.Number },
  ClickedBack: {},
  ClickedStartCheck: {},
  StartedCheck: {},
  StartCheckFailed: { error: Schema.String },
});

export type Message = typeof Message.Type;

export const OutMessage = defineMessageUnion({
  Closed: {},
  LoginRequired: {},
});

export type OutMessage = typeof OutMessage.Type;
