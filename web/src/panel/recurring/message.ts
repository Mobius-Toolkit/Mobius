import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { RecurringTask } from "./model";

export const Message = defineMessageUnion({
  TasksLoaded: { tasks: Schema.Array(RecurringTask) },
  LoadFailed: { error: Schema.String },
  LoginRequired: {},
  ClickedRow: { id: Schema.Number },
  ClickedPause: { id: Schema.Number, paused: Schema.Boolean },
  PausedChanged: { task: RecurringTask },
  PauseFailed: { id: Schema.Number },
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
