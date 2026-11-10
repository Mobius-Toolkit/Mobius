import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { Devices } from "../schemas";

export const Message = defineMessageUnion({
  DevicesLoaded: { devices: Devices },
  LoadFailed: { error: Schema.String },
  LoginRequired: {},
  ClickedLogOut: { id: Schema.Number },
  LoggedOut: { id: Schema.Number },
  LogOutFailed: { id: Schema.Number, error: Schema.String },
  LogOutLoginRequired: { id: Schema.Number },
  CompletedRequestLogin: {},
});

export type Message = typeof Message.Type;
