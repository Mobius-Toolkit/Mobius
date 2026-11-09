import { GetTranscriptResponse, ListAgentsResponse } from "@/api/schemas.gen";

export const Line = GetTranscriptResponse.fields.data.fields.lines.value;
export const Agent = ListAgentsResponse.fields.data.value;

export type Line = typeof Line.Type;
export type Agent = typeof Agent.Type;
