import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { SwitchErrors, SwitchStates, VoiceLanguage } from "../schemas";

export const Message = defineMessageUnion({
  ReceivedSwitches: { switches: Schema.NullOr(SwitchStates) },
  ReceivedErrors: { errors: SwitchErrors },
  VersionLoaded: { release: Schema.String },
  VoiceLoaded: { voice: VoiceLanguage },
  LoginRequired: {},
  ReadFailed: {},
  NavigateTo: { path: Schema.String },
  CompletedRequestLogin: {},
  CompletedNavigate: {},
});

export type Message = typeof Message.Type;
