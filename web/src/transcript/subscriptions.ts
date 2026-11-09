import { Schema } from "effect";
import * as Subscription from "foldkit/subscription";
import { Message } from "./message";
import type { Model } from "./model";
import { transcriptStream } from "./stream";

export const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  stream: entry(
    { id: Schema.Number },
    {
      modelToDependencies: (model) => ({ id: model.agent.id }),
      dependenciesToStream: ({ id }) => transcriptStream(id),
    },
  ),
}));
