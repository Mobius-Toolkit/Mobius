import { Schema } from "effect";
import { GetCheckupResponse, GetCheckupToolsResponse, ListPausesResponse } from "@/api/schemas.gen";

export const Checkup = GetCheckupResponse.fields.data;
export const ToolCheck = GetCheckupToolsResponse.fields.data.value;
export const Pause = ListPausesResponse.fields.data.value;

export const WorkstreamKind = Schema.Literals(["maintenance", "skills"]);

export const FixButton = Schema.Struct({ text: Schema.String, pending: Schema.Boolean });

export type Checkup = typeof Checkup.Type;
export type ToolCheck = typeof ToolCheck.Type;
export type Pause = typeof Pause.Type;
export type WorkstreamKind = typeof WorkstreamKind.Type;
export type FixButton = typeof FixButton.Type;
