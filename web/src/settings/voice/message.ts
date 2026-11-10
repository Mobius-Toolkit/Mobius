import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { VoiceLanguage } from "../schemas";

export const Message = defineMessageUnion({
  VoiceLoaded: { voice: VoiceLanguage },
  LoadFailed: { error: Schema.String },
  LoginRequired: {},
  SearchChanged: { text: Schema.String },
  PickedLanguage: { code: Schema.String },
  LanguageSaved: { voice: VoiceLanguage },
  SaveFailed: { error: Schema.String },
  ClickedBack: {},
  CompletedRequestLogin: {},
  CompletedGoBack: {},
});

export type Message = typeof Message.Type;
