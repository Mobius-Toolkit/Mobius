import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import * as Transcript from "@/transcript/message";
import { Agent } from "@/transcript/schemas";

export const Message = defineMessageUnion({
  AgentsLoaded: { agents: Schema.Array(Agent) },
  LoadFailed: { error: Schema.String },
  LoginRequired: {},
  ToggledStopped: {},
  OpenedAgent: { agent: Agent },
  GotTranscriptMessage: { message: Transcript.Message },
});

export type Message = typeof Message.Type;

export const OutMessage = defineMessageUnion({
  LoginRequired: {},
});

export type OutMessage = typeof OutMessage.Type;

export const toGotTranscriptMessage = (message: Transcript.Message) =>
  Message.GotTranscriptMessage({ message });
