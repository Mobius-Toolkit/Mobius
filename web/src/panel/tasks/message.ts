import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import * as Setting from "@/setting/message";
import { NextSetting } from "@/setting/schemas";
import { TaskLine } from "./model";

export const Message = defineMessageUnion({
  TasksLoaded: { lines: Schema.Array(TaskLine) },
  FallbackLoaded: { fallback: NextSetting },
  LoadFailed: { error: Schema.String },
  LoginRequired: {},
  ToggledClosed: {},
  ClickedStart: { key: Schema.String, line: TaskLine },
  ClickedStop: { key: Schema.String, line: TaskLine },
  ChangedIssue: { key: Schema.String },
  ChangeFailed: { key: Schema.String, error: Schema.String },
  ClickedSetting: { line: TaskLine },
  GotSettingMessage: { message: Setting.Message },
});

export type Message = typeof Message.Type;

export const OutMessage = defineMessageUnion({
  LoginRequired: {},
});

export type OutMessage = typeof OutMessage.Type;

export const toGotSettingMessage = (message: Setting.Message) =>
  Message.GotSettingMessage({ message });
