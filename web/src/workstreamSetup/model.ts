import { Option, Schema } from "effect";
import { Checkup, Picker, WorkstreamKind } from "./schemas";

export const Model = Schema.Struct({
  organization: Schema.String,
  checkup: Schema.Option(Checkup),
  checkupError: Schema.String,
  picker: Schema.Option(Picker),
  creating: Schema.Option(WorkstreamKind),
  createError: Schema.String,
});

export type Model = Schema.Schema.Type<typeof Model>;

export function initialModel(organization: string): Model {
  return {
    organization,
    checkup: Option.none(),
    checkupError: "",
    picker: Option.none(),
    creating: Option.none(),
    createError: "",
  };
}
