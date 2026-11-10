import { Option, Schema } from "effect";
import { SwitchErrors, SwitchStates, type SwitchKind } from "../schemas";

export const Flags = Schema.Struct({});

export const Model = Schema.Struct({
  switches: Schema.Option(SwitchStates),
  errors: SwitchErrors,
});

export type Flags = Schema.Schema.Type<typeof Flags>;
export type Model = Schema.Schema.Type<typeof Model>;

export const initialModel: Model = {
  switches: Option.none(),
  errors: { start: "", merge: "", repairs: "" },
};

// A switch is on until the shell gives its value.
export const isOn = (model: Model, kind: SwitchKind) =>
  Option.match(model.switches, { onNone: () => true, onSome: (switches) => switches[kind] });

// The line of a switch that is off shows its error, so the row shows it only for a switch that is on.
export const shownError = (model: Model, kind: SwitchKind) =>
  isOn(model, kind) ? model.errors[kind] : "";
