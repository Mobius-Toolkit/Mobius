import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { Choice, HarnessChoices, IssueSetting } from "./schemas";

export const Message = defineMessageUnion({
  HarnessesLoaded: { harnesses: Schema.Array(HarnessChoices) },
  HarnessesFailed: { error: Schema.String },
  LoginRequired: {},
  PickedHarness: { value: Schema.String },
  PickedModel: { value: Schema.String },
  PickedEffort: { value: Schema.String },
  ToggledChoice: { choice: Choice },
  ClosedChoice: {},
  PressedEscape: {},
  ClickedSave: {},
  ClickedUseDefault: {},
  Saved: { setting: IssueSetting },
  Cleared: {},
  Failed: { error: Schema.String },
  ClickedClose: {},
  CompletedFocusSheet: {},
});

export type Message = typeof Message.Type;

export const OutMessage = defineMessageUnion({
  Changed: { setting: Schema.Option(IssueSetting) },
  Closed: {},
  LoginRequired: {},
});

export type OutMessage = typeof OutMessage.Type;
