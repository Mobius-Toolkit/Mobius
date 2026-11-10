import { Option } from "effect";
import type { Return as UpdateReturn } from "foldkit/update";
import { GoBack, RequestLogin, SaveLanguage } from "./commands";
import { Message } from "./message";
import type { Model } from "./model";

export function update(model: Model, message: Message): UpdateReturn<Model, Message> {
  return Message.match<UpdateReturn<Model, Message>>(message, {
    // An answer that comes after a save keeps the saved language.
    VoiceLoaded: ({ voice }) => ({
      model: { ...model, voice: Option.orElse(model.voice, () => Option.some(voice)) },
    }),
    LoadFailed: ({ error }) => ({
      model: { ...model, error: `Mobius could not load the language: ${error}.` },
    }),
    LoginRequired: () => ({ model, commands: [RequestLogin()] }),
    SearchChanged: ({ text }) => ({ model: { ...model, search: text } }),
    PickedLanguage: ({ code }) =>
      Option.exists(model.voice, (voice) => voice.language === code)
        ? { model }
        : { model, commands: [SaveLanguage({ code })] },
    LanguageSaved: ({ voice }) => ({ model: { ...model, voice: Option.some(voice), error: "" } }),
    SaveFailed: ({ error }) => ({
      model: { ...model, error: `Mobius could not save the language: ${error}.` },
    }),
    ClickedBack: () => ({ model, commands: [GoBack()] }),
    CompletedRequestLogin: () => ({ model }),
    CompletedGoBack: () => ({ model }),
  });
}
