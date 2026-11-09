import { Option, Schema } from "effect";
import * as Transcript from "@/transcript/model";
import { ActiveAgents } from "./schemas";

export const Model = Schema.Struct({
  agents: Schema.Option(ActiveAgents),
  error: Schema.String,
  // The list requests can finish in another order than they start. Only the answer to the newest request counts.
  request: Schema.Number,
  transcript: Schema.Option(Transcript.Model),
});

export type Model = Schema.Schema.Type<typeof Model>;

export const initialModel: Model = {
  agents: Option.none(),
  error: "",
  request: 1,
  transcript: Option.none(),
};

export const titleOf = (transcript: Transcript.Model) =>
  `${transcript.agent.name} ${transcript.agent.title}`;
