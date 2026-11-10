import { Option, Schema } from "effect";
import { VoiceLanguage } from "../schemas";

export const Flags = Schema.Struct({});

export const Model = Schema.Struct({
  voice: Schema.Option(VoiceLanguage),
  error: Schema.String,
  search: Schema.String,
});

export type Flags = Schema.Schema.Type<typeof Flags>;
export type Model = Schema.Schema.Type<typeof Model>;

export const initialModel: Model = { voice: Option.none(), error: "", search: "" };

const query = (model: Model) => model.search.trim().toLowerCase();

export const searching = (model: Model) => query(model) !== "";

export function matches(model: Model, voice: VoiceLanguage) {
  const text = query(model);
  return voice.languages.filter(
    (item) => item.name.toLowerCase().includes(text) || item.code.toLowerCase().includes(text),
  );
}

// The language of the Owner, English and the automatic detection, in this order, with no repeat.
export function suggested(voice: VoiceLanguage) {
  return [...new Set([voice.language, "en", "auto"])].flatMap((code) =>
    voice.languages.filter((item) => item.code === code),
  );
}

export const allLanguages = (voice: VoiceLanguage) =>
  voice.languages.filter((item) => item.code !== "auto");
