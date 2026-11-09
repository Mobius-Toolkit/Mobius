import { ListActiveAgentsResponse } from "@/api/schemas.gen";

export const ActiveAgents = ListActiveAgentsResponse.fields.data;
export const ActiveAgent = ActiveAgents.fields.groups.value.fields.agents.value;

export type ActiveAgents = typeof ActiveAgents.Type;
export type ActiveAgent = typeof ActiveAgent.Type;
