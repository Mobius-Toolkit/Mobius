import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { Agent } from "@/transcript/schemas";
import * as Agents from "./agents/message";
import * as Details from "./details/message";
import { Tab } from "./schemas";
import * as Tasks from "./tasks/message";

export const Message = defineMessageUnion({
  ConnectionOpened: {},
  LoginRequired: {},
  WorkstreamsChanged: {},
  AgentChanged: { agent: Agent },
  ReceivedBrief: { brief: Schema.String },
  SelectedTab: { tab: Tab },
  ReceivedTab: { tab: Tab },
  GotTasksMessage: { message: Tasks.Message },
  GotAgentsMessage: { message: Agents.Message },
  GotDetailsMessage: { message: Details.Message },
  CompletedRequestLogin: {},
  CompletedNavigate: {},
  CompletedReportTab: {},
  CompletedReportTaskAttached: {},
});

export type Message = typeof Message.Type;

export const toGotTasksMessage = (message: Tasks.Message) => Message.GotTasksMessage({ message });
export const toGotAgentsMessage = (message: Agents.Message) =>
  Message.GotAgentsMessage({ message });
export const toGotDetailsMessage = (message: Details.Message) =>
  Message.GotDetailsMessage({ message });
