import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { HarnessChoices, RoleName } from "../schemas";
import { Choice, Role } from "./schemas";

export const Message = defineMessageUnion({
  RolesLoaded: { roles: Schema.Array(Role) },
  RolesFailed: { error: Schema.String },
  HarnessesLoaded: { harnesses: Schema.Array(HarnessChoices) },
  HarnessesFailed: { error: Schema.String },
  LoginRequired: {},
  OpenedRole: { role: RoleName },
  ClosedSheet: {},
  ToggledChoice: { choice: Choice },
  ClosedChoice: {},
  PressedEscape: {},
  PickedHarness: { value: Schema.String },
  PickedModel: { value: Schema.String },
  PickedEffort: { value: Schema.String },
  ChangedMax: { text: Schema.String },
  ChangedContext: { text: Schema.String },
  ClickedSave: {},
  ClickedReset: {},
  RoleChanged: { role: Role },
  SaveFailed: { error: Schema.String },
  SaveLoginRequired: {},
  ClickedBack: {},
  CompletedRequestLogin: {},
  CompletedGoBack: {},
  CompletedFocusSheet: {},
});

export type Message = typeof Message.Type;
