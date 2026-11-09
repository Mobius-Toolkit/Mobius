import { Option, Schema } from "effect";
import { switchKinds } from "@/lib/switches";
import { SwitchErrors, SwitchStates, VoiceLanguage } from "../schemas";

export const Flags = Schema.Struct({});

export const Model = Schema.Struct({
  switches: Schema.Option(SwitchStates),
  errors: SwitchErrors,
  voice: Schema.Option(VoiceLanguage),
  server: Schema.String,
});

export type Flags = Schema.Schema.Type<typeof Flags>;
export type Model = Schema.Schema.Type<typeof Model>;

export const initialModel: Model = {
  switches: Option.none(),
  errors: { start: "", merge: "", repairs: "" },
  voice: Option.none(),
  server: "",
};

// The text is empty until the shell gives the switches.
export function switchesText(model: Model) {
  return Option.match(model.switches, {
    onNone: () => "",
    onSome: (switches) => {
      const off = switchKinds
        .filter(({ kind }) => !switches[kind])
        .map(({ title }) => `${title} off`);
      return off.length === 0 ? "All on" : off.join(", ");
    },
  });
}

export const switchesFailed = (model: Model) =>
  Object.values(model.errors).some((error) => error !== "");

export function languageName(model: Model) {
  return Option.match(model.voice, {
    onNone: () => "",
    onSome: (voice) => voice.languages.find((item) => item.code === voice.language)?.name ?? "",
  });
}
