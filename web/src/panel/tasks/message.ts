import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import * as Composer from "@/composer";
import * as Setting from "@/setting/message";
import { NextSetting } from "@/setting/schemas";
import * as Selection from "@/ui/selection";
import { TaskDetail, TaskLine } from "./model";

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
  ClickedChat: { key: Schema.String, line: TaskLine },
  AttachedTask: {},
  AttachFailed: { key: Schema.String, error: Schema.String },
  ClickedSetting: { line: TaskLine },
  GotSettingMessage: { message: Setting.Message },
  ClickedTask: { line: TaskLine },
  OpenedTask: { line: TaskLine, scroll: Schema.Number },
  ClickedBack: {},
  GotComposerMessage: { message: Composer.Message },
  GotSelectionMessage: { message: Selection.Message },
  AddedSelection: {},
  CompletedRestoreScroll: {},
  TaskDetailLoaded: {
    repository: Schema.String,
    number: Schema.Number,
    detail: TaskDetail,
  },
  TaskDetailFailed: {
    repository: Schema.String,
    number: Schema.Number,
    error: Schema.String,
  },
});

export type Message = typeof Message.Type;

export const OutMessage = defineMessageUnion({
  LoginRequired: {},
  TaskAttached: {},
});

export type OutMessage = typeof OutMessage.Type;

export const toGotSettingMessage = (message: Setting.Message) =>
  Message.GotSettingMessage({ message });

export const toGotComposerMessage = (message: Composer.Message) =>
  Message.GotComposerMessage({ message });

export const toGotSelectionMessage = (message: Selection.Message) =>
  Message.GotSelectionMessage({ message });
