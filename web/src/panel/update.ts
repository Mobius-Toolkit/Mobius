import { Option } from "effect";
import { Command } from "foldkit";
import type { Command as Cmd } from "foldkit/command";
import * as Update from "foldkit/update";
import { chatDraftKey } from "@/lib/draft";
import * as Selection from "@/ui/selection";
import { OutMessage as AgentsOutMessage } from "./agents/message";
import * as AgentsModel from "./agents/model";
import {
  refresh as refreshAgents,
  reload as reloadAgents,
  update as updateAgents,
} from "./agents/update";
import { AddSelection, Navigate, RequestLogin, ReportTab } from "./commands";
import { OutMessage as DetailsOutMessage } from "./details/message";
import * as DetailsModel from "./details/model";
import {
  openSetting as openDetailsSetting,
  refresh as refreshDetails,
  reload as reloadDetails,
  update as updateDetails,
} from "./details/update";
import {
  Message,
  toGotAgentsMessage,
  toGotDetailsMessage,
  toGotRecurringMessage,
  toGotSelectionMessage,
  toGotTasksMessage,
} from "./message";
import type { Model } from "./model";
import { OutMessage as RecurringOutMessage } from "./recurring/message";
import * as RecurringModel from "./recurring/model";
import {
  refresh as refreshRecurring,
  reload as reloadRecurring,
  update as updateRecurring,
} from "./recurring/update";
import type { Tab } from "./schemas";
import { OutMessage as TasksOutMessage } from "./tasks/message";
import * as TasksModel from "./tasks/model";
import {
  open as openTask,
  openSetting as openTaskSetting,
  refresh as refreshTasks,
  reload as reloadTasks,
  update as updateTasks,
} from "./tasks/update";

type UpdateReturn = Update.Return<Model, Message>;

const requestLogin: Update.Step<Model, Message> = (model) => ({
  model,
  commands: [RequestLogin()],
});

const foldSelection = Update.foldChild({
  update: Selection.update,
  read: (model: Model) => Option.some(model.selection),
  write: (model, selection) => ({ ...model, selection }),
  toParentMessage: toGotSelectionMessage,
  foldOutMessage: (outMessage) =>
    Selection.OutMessage.match<Update.Step<Model, Message>>(outMessage, {
      AddRequested:
        ({ text, context, part }) =>
        (model) => ({
          model,
          commands: [
            AddSelection({
              key: chatDraftKey(model.owner, `${model.owner}/${model.name}`, model.number),
              selection: {
                source: "brief",
                text,
                context,
                reference: `#${model.number}`,
                link: `https://github.com/${model.owner}/${model.name}/issues/${model.number}`,
                part,
                messageId: 0,
                messageAuthor: "",
              },
            }),
          ],
        }),
    }),
});

const foldTasks = Update.foldChild({
  update: updateTasks,
  read: (model: Model) => Option.some(model.tasks),
  write: (model, tasks) => ({ ...model, tasks }),
  toParentMessage: toGotTasksMessage,
  foldOutMessage: (outMessage) =>
    TasksOutMessage.match<Update.Step<Model, Message>>(outMessage, {
      LoginRequired: () => requestLogin,
      TaskAttached: () => (model) => ({ model }),
    }),
});

const foldOpenTask = Update.foldChild({
  update: openTask,
  read: (model: Model) => Option.some(model.tasks),
  write: (model, tasks) => ({ ...model, tasks }),
  toParentMessage: toGotTasksMessage,
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
      NavigationRequested:
        ({ path }) =>
        (model) => ({ model, commands: [Navigate({ path })] }),
    }),
});

const foldRecurring = Update.foldChild({
  update: updateRecurring,
  read: (model: Model) => Option.some(model.recurring),
  write: (model, recurring) => ({ ...model, recurring }),
  toParentMessage: toGotRecurringMessage,
  foldOutMessage: (outMessage) =>
    RecurringOutMessage.match<Update.Step<Model, Message>>(outMessage, {
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

const refreshRecurringStep = Update.foldChildStep({
  update: refreshRecurring,
  read: (model: Model) => Option.some(model.recurring),
  write: (model, recurring) => ({ ...model, recurring }),
  toParentMessage: toGotRecurringMessage,
});

// The Tasks tab, the Agents tab, the Details tab and the Recurring tab read their data each time they open.
export function commandsFor(model: Model): ReadonlyArray<Cmd<Message>> {
  switch (model.tab) {
    case "tasks":
      return Command.mapMessages(reloadTasks(model.tasks), toGotTasksMessage);
    case "agents":
      return Command.mapMessages([reloadAgents(model.agents)], toGotAgentsMessage);
    case "details":
      return Command.mapMessages(reloadDetails(model.details), toGotDetailsMessage);
    case "recurring":
      return Command.mapMessages(reloadRecurring(model.recurring), toGotRecurringMessage);
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
    opening: Option.none(),
    selection: null,
    tasks: tab === "tasks" ? TasksModel.initialModel(workstream) : model.tasks,
    agents: tab === "agents" ? AgentsModel.initialModel(workstream) : model.agents,
    details: tab === "details" ? DetailsModel.initialModel(workstream) : model.details,
    recurring: tab === "recurring" ? RecurringModel.initialModel(workstream) : model.recurring,
  };
  return { model: next, commands: commandsFor(next) };
}

function select(model: Model, tab: Tab): UpdateReturn {
  const entered = enter(model, tab);
  return { ...entered, commands: [...(entered.commands ?? []), ReportTab({ tab })] };
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
    case "recurring":
      return refreshRecurringStep(model);
    case "brief":
      return { model };
  }
}

// Opens the requested setting sheet once its data is there. A task that is not in the list drops the request.
function openRequested(result: UpdateReturn): UpdateReturn {
  const { model } = result;
  if (Option.isNone(model.opening)) {
    return result;
  }
  const { setting, task } = model.opening.value;
  const cleared = { ...model, opening: Option.none() };
  const opened = Option.match(task, {
    onNone: () =>
      Option.isSome(model.details.details) && Option.isSome(model.details.roles)
        ? Update.foldChildStep({
            update: (details: DetailsModel.Model) => openDetailsSetting(details, setting),
            read: (parent: Model) => Option.some(parent.details),
            write: (parent, details) => ({ ...parent, details }),
            toParentMessage: toGotDetailsMessage,
          })(cleared)
        : undefined,
    onSome: (target) => {
      if (Option.isNone(model.tasks.lines) || Option.isNone(model.tasks.fallback)) {
        return undefined;
      }
      const line = model.tasks.lines.value.find(
        (other) => other.repository === target.repository && other.number === target.number,
      );
      return line
        ? Update.foldChildStep({
            update: (tasks: TasksModel.Model) => openTaskSetting(tasks, line),
            read: (parent: Model) => Option.some(parent.tasks),
            write: (parent, tasks) => ({ ...parent, tasks }),
            toParentMessage: toGotTasksMessage,
          })(cleared)
        : { model: cleared };
    },
  });
  if (opened === undefined) {
    return result;
  }
  return {
    model: opened.model,
    commands: [...(result.commands ?? []), ...(opened.commands ?? [])],
  };
}

export function update(model: Model, message: Message): UpdateReturn {
  return openRequested(handle(model, message));
}

function handle(model: Model, message: Message): UpdateReturn {
  return Message.match<UpdateReturn>(message, {
    ConnectionOpened: () => reloadTab(model, ["tasks", "agents", "details", "recurring"]),
    LoginRequired: () => requestLogin(model),
    WorkstreamsChanged: () => reloadTab(model, ["tasks", "details"]),
    AgentChanged: ({ agent }) =>
      agent.workstreamRepository === `${model.owner}/${model.name}` &&
      agent.workstream === model.number
        ? reloadTab(model, ["agents"])
        : { model },
    RecurringChanged: ({ repository, number }) =>
      repository === `${model.owner}/${model.name}` && number === model.number
        ? reloadTab(model, ["recurring"])
        : { model },
    ReceivedBrief: ({ brief }) => ({ model: { ...model, brief } }),
    GotSelectionMessage: ({ message: child }) => foldSelection(model, child),
    AddedSelection: () => ({ model }),
    SelectedTab: ({ tab }) => select(model, tab),
    ReceivedTab: ({ tab }) => enter(model, tab),
    RequestedTask: ({ repository, number }) =>
      Update.combine(model, [
        (current) => select(current, "tasks"),
        foldOpenTask({ repository, number }),
      ]),
    GotTasksMessage: ({ message: child }) => foldTasks(model, child),
    GotAgentsMessage: ({ message: child }) => foldAgents(model, child),
    GotDetailsMessage: ({ message: child }) => foldDetails(model, child),
    GotRecurringMessage: ({ message: child }) => foldRecurring(model, child),
    CompletedRequestLogin: () => ({ model }),
    CompletedNavigate: () => ({ model }),
    CompletedReportTab: () => ({ model }),
  });
}
