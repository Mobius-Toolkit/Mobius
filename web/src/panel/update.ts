import { Option } from "effect";
import { Command } from "foldkit";
import * as Update from "foldkit/update";
import { OutMessage as AgentsOutMessage } from "./agents/message";
import * as AgentsModel from "./agents/model";
import {
  refresh as refreshAgents,
  reload as reloadAgents,
  update as updateAgents,
} from "./agents/update";
import { RequestLogin, ReportTab, ReportTaskAttached } from "./commands";
import { OutMessage as DetailsOutMessage } from "./details/message";
import * as DetailsModel from "./details/model";
import {
  refresh as refreshDetails,
  reload as reloadDetails,
  update as updateDetails,
} from "./details/update";
import { Message, toGotAgentsMessage, toGotDetailsMessage, toGotTasksMessage } from "./message";
import type { Model } from "./model";
import type { Tab } from "./schemas";
import { OutMessage as TasksOutMessage } from "./tasks/message";
import * as TasksModel from "./tasks/model";
import {
  refresh as refreshTasks,
  reload as reloadTasks,
  update as updateTasks,
} from "./tasks/update";

type UpdateReturn = Update.Return<Model, Message>;

const requestLogin: Update.Step<Model, Message> = (model) => ({
  model,
  commands: [RequestLogin()],
});

const foldTasks = Update.foldChild({
  update: updateTasks,
  read: (model: Model) => Option.some(model.tasks),
  write: (model, tasks) => ({ ...model, tasks }),
  toParentMessage: toGotTasksMessage,
  foldOutMessage: (outMessage) =>
    TasksOutMessage.match<Update.Step<Model, Message>>(outMessage, {
      LoginRequired: () => requestLogin,
      TaskAttached: () => (model) => ({ model, commands: [ReportTaskAttached()] }),
    }),
});

const foldAgents = Update.foldChild({
  update: updateAgents,
  read: (model: Model) => Option.some(model.agents),
  write: (model, agents) => ({ ...model, agents }),
  toParentMessage: toGotAgentsMessage,
  foldOutMessage: (outMessage) =>
    AgentsOutMessage.match<Update.Step<Model, Message>>(outMessage, {
      LoginRequired: () => requestLogin,
    }),
});

const foldDetails = Update.foldChild({
  update: updateDetails,
  read: (model: Model) => Option.some(model.details),
  write: (model, details) => ({ ...model, details }),
  toParentMessage: toGotDetailsMessage,
  foldOutMessage: (outMessage) =>
    DetailsOutMessage.match<Update.Step<Model, Message>>(outMessage, {
      LoginRequired: () => requestLogin,
    }),
});

const refreshTasksStep = Update.foldChildStep({
  update: refreshTasks,
  read: (model: Model) => Option.some(model.tasks),
  write: (model, tasks) => ({ ...model, tasks }),
  toParentMessage: toGotTasksMessage,
});

const refreshAgentsStep = Update.foldChildStep({
  update: refreshAgents,
  read: (model: Model) => Option.some(model.agents),
  write: (model, agents) => ({ ...model, agents }),
  toParentMessage: toGotAgentsMessage,
});

const refreshDetailsStep = Update.foldChildStep({
  update: refreshDetails,
  read: (model: Model) => Option.some(model.details),
  write: (model, details) => ({ ...model, details }),
  toParentMessage: toGotDetailsMessage,
});

// The Tasks tab, the Agents tab and the Details tab read their data each time they open.
export function commandsFor(model: Model) {
  switch (model.tab) {
    case "tasks":
      return Command.mapMessages(reloadTasks(model.tasks), toGotTasksMessage);
    case "agents":
      return Command.mapMessages([reloadAgents(model.agents)], toGotAgentsMessage);
    case "details":
      return Command.mapMessages(reloadDetails(model.details), toGotDetailsMessage);
    case "brief":
      return [];
  }
}

function enter(model: Model, tab: Tab): UpdateReturn {
  if (tab === model.tab) {
    return { model };
  }
  const workstream = {
    owner: model.owner,
    name: model.name,
    number: model.number,
    instance: model.instance,
  };
  const next: Model = {
    ...model,
    tab,
    tasks: tab === "tasks" ? TasksModel.initialModel(workstream) : model.tasks,
    agents: tab === "agents" ? AgentsModel.initialModel(workstream) : model.agents,
    details: tab === "details" ? DetailsModel.initialModel(workstream) : model.details,
  };
  return { model: next, commands: commandsFor(next) };
}

// A change of the Workstreams or of the agents reads the tabs that show them again.
function reloadTab(model: Model, only: readonly Tab[]): UpdateReturn {
  if (!only.includes(model.tab)) {
    return { model };
  }
  switch (model.tab) {
    case "tasks":
      return refreshTasksStep(model);
    case "agents":
      return refreshAgentsStep(model);
    case "details":
      return refreshDetailsStep(model);
    case "brief":
      return { model };
  }
}

export function update(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    ConnectionOpened: () => reloadTab(model, ["tasks", "agents", "details"]),
    LoginRequired: () => requestLogin(model),
    WorkstreamsChanged: () => reloadTab(model, ["tasks", "details"]),
    AgentChanged: ({ agent }) =>
      agent.workstreamRepository === `${model.owner}/${model.name}` &&
      agent.workstream === model.number
        ? reloadTab(model, ["agents"])
        : { model },
    ReceivedBrief: ({ brief }) => ({ model: { ...model, brief } }),
    SelectedTab: ({ tab }) => {
      const entered = enter(model, tab);
      return { ...entered, commands: [...(entered.commands ?? []), ReportTab({ tab })] };
    },
    ReceivedTab: ({ tab }) => enter(model, tab),
    GotTasksMessage: ({ message: child }) => foldTasks(model, child),
    GotAgentsMessage: ({ message: child }) => foldAgents(model, child),
    GotDetailsMessage: ({ message: child }) => foldDetails(model, child),
    CompletedRequestLogin: () => ({ model }),
    CompletedReportTab: () => ({ model }),
    CompletedReportTaskAttached: () => ({ model }),
  });
}
