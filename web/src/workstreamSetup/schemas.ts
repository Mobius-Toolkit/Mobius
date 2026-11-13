import { Schema } from "effect";
import { GetCheckupResponse } from "@/api/schemas.gen";

export const Checkup = GetCheckupResponse.fields.data;

export const WorkstreamKind = Schema.Literals(["maintenance", "skills"]);

export const Part = Schema.Literals(["all", "maintenance", "skills"]);

export const Picker = Schema.Struct({
  selected: Schema.String,
  error: Schema.String,
  saving: Schema.Boolean,
});

export type Checkup = typeof Checkup.Type;
export type WorkstreamKind = typeof WorkstreamKind.Type;
export type Part = typeof Part.Type;
export type Picker = typeof Picker.Type;
