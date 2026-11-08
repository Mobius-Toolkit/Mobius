import { Option, Schema } from "effect";
import * as Transcript from "@/transcript/model";
import { Agent } from "@/transcript/schemas";

export const Model = Schema.Struct({
  owner: Schema.String,
  name: Schema.String,
  number: Schema.Number,
  instance: Schema.String,
  agents: Schema.Array(Agent),
  error: Schema.String,
  showStopped: Schema.Boolean,
  transcript: Schema.Option(Transcript.Model),
});

export type Model = Schema.Schema.Type<typeof Model>;

export function initialModel(options: {
  owner: string;
  name: string;
  number: number;
  instance: string;
}): Model {
  return {
    ...options,
    agents: [],
    error: "",
    showStopped: false,
    transcript: Option.none(),
  };
}

export const shownAgents = (agents: readonly Agent[], showStopped: boolean) =>
  agents.filter((agent) => showStopped || agent.endedAt === null).toSorted((a, b) => b.id - a.id);
