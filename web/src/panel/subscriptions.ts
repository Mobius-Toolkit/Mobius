import { Option, Schema } from "effect";
import * as Subscription from "foldkit/subscription";
import { storeValues } from "@/live/stores";
import { currentWorkstreams, subscribeWorkstreams } from "@/lib/workstreams";
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
import { subscriptions as recurringSubscriptions } from "./recurring/subscriptions";
import { subscriptions as tasksSubscriptions } from "./tasks/subscriptions";

const panel = Subscription.make<Model, Message>()((entry) => ({
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
