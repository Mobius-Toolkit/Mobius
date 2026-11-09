import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import * as Setting from "@/setting/message";
import { Role } from "@/setting/schemas";
import { ClosureItem, Session, WorkstreamDetails } from "./model";

export const Message = defineMessageUnion({
  DetailsLoaded: { details: WorkstreamDetails },
  RolesLoaded: { roles: Schema.Array(Role) },
  LoadFailed: { error: Schema.String },
  LoginRequired: {},
  SwitchedEvergreen: { on: Schema.Boolean },
  EvergreenChanged: { on: Schema.Boolean },
  EvergreenFailed: { error: Schema.String },
  ClickedChange: { session: Session },
  ClickedClose: {},
  ClosureLoaded: { items: Schema.Array(ClosureItem) },
  ClosureFailed: { error: Schema.String },
  ClickedCloseWorkstream: {},
  WorkstreamClosed: {},
  CloseFailed: { error: Schema.String },
  DismissedClosure: {},
  PressedEscape: {},
  CompletedFocusClosure: {},
  GotSettingMessage: { message: Setting.Message },
});

export type Message = typeof Message.Type;

export const OutMessage = defineMessageUnion({
  LoginRequired: {},
});

export type OutMessage = typeof OutMessage.Type;

export const toGotSettingMessage = (message: Setting.Message) =>
  Message.GotSettingMessage({ message });
