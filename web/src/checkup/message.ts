import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { Checkup, Pause, ToolCheck, WorkstreamKind } from "./schemas";

export const Message = defineMessageUnion({
  ReceivedOrganizations: { organizations: Schema.Array(Schema.String) },
  ReceivedPauses: { pauses: Schema.Array(Pause) },
  LoginRequired: {},
  ToolsLoaded: { tools: Schema.Array(ToolCheck) },
  ToolsFailed: { error: Schema.String },
  CheckupLoaded: { organization: Schema.String, checkup: Checkup },
  CheckupFailed: { organization: Schema.String, error: Schema.String },
  OpenedTool: { name: Schema.String },
  ClosedTool: {},
  ClickedUpgrade: { tool: ToolCheck },
  ClickedAutoUpgrade: { tool: ToolCheck, on: Schema.Boolean },
  ToolsUpgraded: { tools: Schema.Array(ToolCheck) },
  UpgradeFailed: { error: Schema.String },
  ClickedFix: {},
  LabelsFixed: {},
  LabelFixFailed: { error: Schema.String },
  ClickedCreate: { kind: WorkstreamKind },
  WorkstreamRequested: { organization: Schema.String },
  CreateFailed: { error: Schema.String },
  NavigateTo: { path: Schema.String },
  ClickedBack: {},
  CompletedRequestLogin: {},
  CompletedNavigate: {},
  CompletedGoBack: {},
  CompletedOpenChat: {},
  CompletedReportFixButton: {},
  CompletedFocusDialog: {},
});

export type Message = typeof Message.Type;
