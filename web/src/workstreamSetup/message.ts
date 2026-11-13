import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { Checkup, WorkstreamKind } from "./schemas";

export const Message = defineMessageUnion({
  CheckupLoaded: { checkup: Checkup },
  CheckupFailed: { error: Schema.String },
  LoginRequired: {},
  OpenedPicker: {},
  ClosedPicker: {},
  SelectedRepository: { repository: Schema.String },
  ClickedSave: {},
  Saved: {},
  SaveFailed: { error: Schema.String },
  ClickedCreate: { kind: WorkstreamKind },
  WorkstreamRequested: { organization: Schema.String },
  CreateFailed: { error: Schema.String },
  NavigateTo: { path: Schema.String },
  CompletedRequestLogin: {},
  CompletedNavigate: {},
  CompletedFocusDialog: {},
});

export type Message = typeof Message.Type;
