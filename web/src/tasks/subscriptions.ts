import { Effect, Queue, Schema, Stream } from "effect";
import * as Subscription from "foldkit/subscription";
import { InboxItem } from "@/chat/schemas";
import { liveEvents, liveListener } from "@/live/events";
import { Message } from "./message";
import type { Model } from "./model";

const minute = 60_000;

// The generated type of the data of the event workstreams is an object with no known fields, and the board does not use
// the data.
const WorkstreamsEventData = Schema.Record(Schema.String, Schema.Json);

const clock = Stream.callback<Message>((queue) => {
  const tick = () => Queue.offerUnsafe(queue, Message.Ticked({ now: Date.now() }));
  return Effect.gen(function* () {
    tick();
    const timer = setInterval(tick, minute);
    yield* Effect.addFinalizer(() =>
      Effect.sync(() => {
        clearInterval(timer);
      }),
    );
  });
});

export const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  clock: entry({}, { modelToDependencies: () => ({}), dependenciesToStream: () => clock }),
  liveEvents: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () =>
        liveEvents<Message>({
          opened: Message.ConnectionOpened(),
          loginRequired: Message.LoginRequired(),
          listeners: [
            liveListener("workstreams", WorkstreamsEventData, () => Message.BoardChanged()),
            liveListener("inbox", InboxItem, () => Message.BoardChanged()),
          ],
        }),
    },
  ),
}));
