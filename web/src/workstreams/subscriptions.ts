import { Option } from "effect";
import * as Port from "foldkit/port";
import * as Subscription from "foldkit/subscription";
import { currentUnread, subscribeUnread } from "@/lib/unread";
import { currentWorkstreams, subscribeWorkstreams } from "@/lib/workstreams";
import { storeValues } from "@/live/stores";
import { Message } from "./message";
import type { Model } from "./model";
import { ports } from "./ports";

export const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  needsHumanPort: Port.subscriptionEntry(ports.inbound.needsHuman, (needsHuman) =>
    Message.ReceivedNeedsHuman({ needsHuman }),
  ),
  workstreams: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeWorkstreams, currentWorkstreams, (state) =>
          Message.ReceivedWorkstreams({
            workstreams: Option.fromNullishOr(state.list),
            working: [...state.working],
            error: state.error ?? "",
          }),
        ),
    },
  ),
  unread: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeUnread, currentUnread, (unread) =>
          Message.ReceivedUnread({ unread: unread ?? [] }),
        ),
    },
  ),
}));
