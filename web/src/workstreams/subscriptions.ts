import { Option } from "effect";
import * as Subscription from "foldkit/subscription";
import { currentUnread, subscribeUnread } from "@/lib/unread";
import { currentWorkstreams, subscribeWorkstreams } from "@/lib/workstreams";
import { storeValues } from "@/live/stores";
import { Message } from "./message";
import type { Model } from "./model";

export const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  wsNeedsHuman: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        storeValues(subscribeWorkstreams, currentWorkstreams, (state) =>
          Message.ReceivedNeedsHuman({ needsHuman: state.needsHuman }),
        ),
    },
  ),
  wsWorkstreams: entry(
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
  wsUnread: entry(
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
