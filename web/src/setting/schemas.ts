import { Schema } from "effect";
import {
  GetWorkstreamDetailsResponse,
  ListHarnessesResponse,
  ListRolesResponse,
} from "@/api/schemas.gen";

export const HarnessChoices = ListHarnessesResponse.fields.data.value;
export const NextSetting = GetWorkstreamDetailsResponse.fields.data.fields.lead;
export const Role = ListRolesResponse.fields.data.value;

export const IssueSetting = Schema.Struct({
  harness: Schema.String,
  model: Schema.String,
  effort: Schema.String,
});

const Workstream = { owner: Schema.String, name: Schema.String, number: Schema.Number };

// The place that a setting belongs to: the Implementer of a task, the Lead of a Workstream, or the Implementer of a
// Workstream.
export const Target = Schema.Union([
  Schema.TaggedStruct("Task", Workstream),
  Schema.TaggedStruct("Lead", Workstream),
  Schema.TaggedStruct("WorkstreamImplementer", Workstream),
]);

export const Choice = Schema.Literals(["harness", "model", "effort"]);

export type HarnessChoices = typeof HarnessChoices.Type;
export type NextSetting = typeof NextSetting.Type;
export type Role = typeof Role.Type;
export type IssueSetting = typeof IssueSetting.Type;
export type Target = typeof Target.Type;
export type Choice = typeof Choice.Type;
