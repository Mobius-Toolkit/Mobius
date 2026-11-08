import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { BoardTask } from "./schemas";

export const Message = defineMessageUnion({
  ConnectionOpened: {},
  BoardChanged: {},
  LoginRequired: {},
  BoardLoaded: { tasks: Schema.Array(BoardTask) },
  BoardFailed: { error: Schema.String },
  Ticked: { now: Schema.Number },
  NavigateTo: { path: Schema.String },
  CompletedNavigate: {},
  CompletedRequestLogin: {},
});

export type Message = typeof Message.Type;
