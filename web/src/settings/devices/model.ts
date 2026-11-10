import { Option, Schema } from "effect";
import { Devices } from "../schemas";

export const Flags = Schema.Struct({});

export const Model = Schema.Struct({
  devices: Schema.Option(Devices),
  error: Schema.String,
  loggingOut: Schema.Array(Schema.Number),
});

export type Flags = Schema.Schema.Type<typeof Flags>;
export type Model = Schema.Schema.Type<typeof Model>;

export const initialModel: Model = { devices: Option.none(), error: "", loggingOut: [] };
