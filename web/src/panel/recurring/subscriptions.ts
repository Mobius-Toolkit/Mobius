import { Option, Schema, Stream } from "effect";
import * as Subscription from "foldkit/subscription";
import { escapeKey } from "@/ui/escape";
import { Message } from "./message";
import type { Model } from "./model";

export const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  recurringEscape: entry(
    { open: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ open: Option.isSome(model.deletion) }),
      dependenciesToStream: ({ open }) =>
        open ? escapeKey(Message.PressedEscape()) : Stream.empty,
    },
  ),
}));
