import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import * as Transcript from "@/transcript/message";
import { Agent } from "@/transcript/schemas";
import { ActiveAgents } from "./schemas";

export const Message = defineMessageUnion({
  ConnectionOpened: {},
  AgentChanged: {},
  LoginRequired: {},
  AgentsLoaded: { request: Schema.Number, agents: ActiveAgents },
  LoadFailed: { request: Schema.Number, error: Schema.String },
  OpenedAgent: { agent: Agent },
  ClickedBack: {},
  BackRequested: {},
  GotTranscriptMessage: { message: Transcript.Message },
  CompletedRequestLogin: {},
  CompletedReportTitle: {},
});

export type Message = typeof Message.Type;

export const toGotTranscriptMessage = (message: Transcript.Message) =>
  Message.GotTranscriptMessage({ message });
