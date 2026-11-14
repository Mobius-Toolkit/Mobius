import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { Agent } from "@/transcript/schemas";
import * as Selection from "@/ui/selection";
import * as Agents from "./agents/message";
import * as Details from "./details/message";
import * as Recurring from "./recurring/message";
import { Tab } from "./schemas";
import * as Tasks from "./tasks/message";

export const Message = defineMessageUnion({
  ConnectionOpened: {},
  LoginRequired: {},
  WorkstreamsChanged: {},
  RecurringChanged: { repository: Schema.String, number: Schema.Number },
  AgentChanged: { agent: Agent },
  ReceivedBrief: { brief: Schema.String },
  GotSelectionMessage: { message: Selection.Message },
  AddedSelection: {},
  SelectedTab: { tab: Tab },
  ReceivedTab: { tab: Tab },
  GotTasksMessage: { message: Tasks.Message },
  GotAgentsMessage: { message: Agents.Message },
  GotDetailsMessage: { message: Details.Message },
  GotRecurringMessage: { message: Recurring.Message },
  CompletedRequestLogin: {},
  CompletedNavigate: {},
  CompletedReportTab: {},
});

export type Message = typeof Message.Type;

export const toGotSelectionMessage = (message: Selection.Message) =>
  Message.GotSelectionMessage({ message });
export const toGotTasksMessage = (message: Tasks.Message) => Message.GotTasksMessage({ message });
export const toGotAgentsMessage = (message: Agents.Message) =>
  Message.GotAgentsMessage({ message });
export const toGotDetailsMessage = (message: Details.Message) =>
  Message.GotDetailsMessage({ message });
export const toGotRecurringMessage = (message: Recurring.Message) =>
  Message.GotRecurringMessage({ message });
