import { Effect, Queue, Stream } from "effect";
import * as Subscription from "foldkit/subscription";
import { Message } from "./message";
import type { Model } from "./model";

const minute = 60_000;

export const tasksClock = Stream.callback<Message>((queue) => {
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
  clock: entry({}, { modelToDependencies: () => ({}), dependenciesToStream: () => tasksClock }),
}));
