import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { SwitchErrors, SwitchKind, SwitchStates } from "../schemas";

export const Message = defineMessageUnion({
  ReceivedSwitches: { switches: Schema.NullOr(SwitchStates) },
  ReceivedErrors: { errors: SwitchErrors },
  ClickedSwitch: { kind: SwitchKind, on: Schema.Boolean },
  ClickedBack: {},
  CompletedRequestSwitch: {},
  CompletedGoBack: {},
});

export type Message = typeof Message.Type;
