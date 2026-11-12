import { Effect, Queue, Stream } from "effect";
import * as Subscription from "foldkit/subscription";
import { subscriptions as transcriptSubscriptions } from "@/transcript/subscriptions";
import { Message, toGotTranscriptMessage } from "./message";
import type { Model } from "./model";

const minute = 60_000;

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

const transcript = Subscription.lift(transcriptSubscriptions)<Model, Message>({
  read: (model) => model.transcript,
  toParentMessage: toGotTranscriptMessage,
});

const own = Subscription.make<Model, Message>()((entry) => ({
  agentsClock: entry({}, { modelToDependencies: () => ({}), dependenciesToStream: () => clock }),
}));

export const subscriptions = { transcriptStream: transcript.stream, agentsClock: own.agentsClock };
