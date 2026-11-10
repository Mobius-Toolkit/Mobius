import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { RecurringTask } from "./model";

export const Message = defineMessageUnion({
  TasksLoaded: { tasks: Schema.Array(RecurringTask) },
  LoadFailed: { error: Schema.String },
  LoginRequired: {},
  ClickedRow: { id: Schema.Number },
  OpenedTask: { id: Schema.Number, scroll: Schema.Number },
  ClickedBack: {},
  CompletedRestoreScroll: {},
  ClickedPause: { id: Schema.Number, paused: Schema.Boolean },
  PausedChanged: { task: RecurringTask },
  PauseFailed: { id: Schema.Number },
  ClickedRun: { id: Schema.Number },
  RunStarted: { task: RecurringTask },
  RunRefused: { id: Schema.Number, reason: Schema.String },
  CompletedShowNotice: {},
  ClickedDelete: { id: Schema.Number, text: Schema.String },
  ClickedConfirmDelete: {},
  TaskDeleted: { id: Schema.Number },
  DeleteFailed: {},
  DismissedDelete: {},
  PressedEscape: {},
  CompletedFocusDialog: {},
});

export type Message = typeof Message.Type;

export const OutMessage = defineMessageUnion({
  LoginRequired: {},
});

export type OutMessage = typeof OutMessage.Type;
