import { Option, Schema, Stream } from "effect";
import * as Subscription from "foldkit/subscription";
import { escapeKey } from "@/ui/escape";
import { Message } from "./message";
import type { Model } from "./model";

export const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  memoryEscape: entry(
    { editing: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ editing: Option.isSome(model.editing) }),
      dependenciesToStream: ({ editing }) =>
        editing ? escapeKey(Message.PressedEscape()) : Stream.empty,
    },
  ),
}));
