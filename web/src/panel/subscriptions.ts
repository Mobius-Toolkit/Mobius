import { Option, Schema } from "effect";
import * as Port from "foldkit/port";
import * as Subscription from "foldkit/subscription";
import { liveEvents, liveListener } from "@/live/events";
import { storeValues } from "@/live/stores";
import { currentWorkstreams, subscribeWorkstreams } from "@/lib/workstreams";
import { Agent } from "@/transcript/schemas";
import { subscriptions as agentsSubscriptions } from "./agents/subscriptions";
import { subscriptions as detailsSubscriptions } from "./details/subscriptions";
import {
  Message,
  toGotAgentsMessage,
  toGotDetailsMessage,
  toGotRecurringMessage,
  toGotTasksMessage,
} from "./message";
import type { Model } from "./model";
import { ports } from "./ports";
import { subscriptions as recurringSubscriptions } from "./recurring/subscriptions";
import { subscriptions as tasksSubscriptions } from "./tasks/subscriptions";

// The generated type of the data of the event workstreams is an object with no known fields, and the panel does not use
// the data.
const WorkstreamsEventData = Schema.Record(Schema.String, Schema.Json);

const RecurringEventData = Schema.Struct({ repository: Schema.String, number: Schema.Number });

const panel = Subscription.make<Model, Message>()((entry) => ({
  tabPort: Port.subscriptionEntry(ports.inbound.tab, (tab) => Message.ReceivedTab({ tab })),
  liveEvents: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        liveEvents<Message>({
          opened: Message.ConnectionOpened(),
          loginRequired: Message.LoginRequired(),
          listeners: [
            liveListener("workstreams", WorkstreamsEventData, () => Message.WorkstreamsChanged()),
            liveListener("agent", Agent, (agent) => Message.AgentChanged({ agent })),
            liveListener("recurring_tasks", RecurringEventData, ({ repository, number }) =>
              Message.RecurringChanged({ repository, number }),
            ),
          ],
        }),
    },
  ),
  brief: entry(
    { owner: Schema.String, name: Schema.String, number: Schema.Number },
    {
      modelToDependencies: ({ owner, name, number }) => ({ owner, name, number }),
      dependenciesToStream: ({ owner, name, number }) =>
        storeValues(subscribeWorkstreams, currentWorkstreams, (state) =>
          Message.ReceivedBrief({
            brief:
              state.list?.find(
                (workstream) =>
                  workstream.repository === `${owner}/${name}` && workstream.number === number,
              )?.brief ?? "",
          }),
        ),
    },
  ),
}));

const tasks = Subscription.lift(tasksSubscriptions)<Model, Message>({
  read: (model) => Option.some(model.tasks),
  toParentMessage: toGotTasksMessage,
  when: (model) => model.tab === "tasks",
});

const agents = Subscription.lift(agentsSubscriptions)<Model, Message>({
  read: (model) => Option.some(model.agents),
  toParentMessage: toGotAgentsMessage,
  when: (model) => model.tab === "agents",
});

const details = Subscription.lift(detailsSubscriptions)<Model, Message>({
  read: (model) => Option.some(model.details),
  toParentMessage: toGotDetailsMessage,
  when: (model) => model.tab === "details",
});

const recurring = Subscription.lift(recurringSubscriptions)<Model, Message>({
  read: (model) => Option.some(model.recurring),
  toParentMessage: toGotRecurringMessage,
  when: (model) => model.tab === "recurring",
});

export const subscriptions = Subscription.aggregate(panel, tasks, agents, details, recurring);
