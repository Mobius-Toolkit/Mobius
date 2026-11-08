import * as Port from "foldkit/port";
import * as Subscription from "foldkit/subscription";
import { liveEvents, liveListener } from "@/live/events";
import { Agent } from "@/transcript/schemas";
import { subscriptions as transcriptSubscriptions } from "@/transcript/subscriptions";
import { Message, toGotTranscriptMessage } from "./message";
import type { Model } from "./model";
import { ports } from "./ports";

const page = Subscription.make<Model, Message>()((entry) => ({
  backPort: Port.subscriptionEntry(ports.inbound.backRequested, () => Message.BackRequested()),
  liveEvents: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        liveEvents<Message>({
          opened: Message.ConnectionOpened(),
          loginRequired: Message.LoginRequired(),
          listeners: [liveListener("agent", Agent, () => Message.AgentChanged())],
        }),
    },
  ),
}));

const transcript = Subscription.lift(transcriptSubscriptions)<Model, Message>({
  read: (model) => model.transcript,
  toParentMessage: toGotTranscriptMessage,
});

export const subscriptions = Subscription.aggregate(page, transcript);
