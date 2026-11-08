import * as Subscription from "foldkit/subscription";
import { escapeKey } from "@/ui/escape";
import { Message } from "./message";
import type { Model } from "./model";

export const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  escape: entry(
    {},
    {
      modelToDependencies: () => ({}),
      dependenciesToStream: () => escapeKey(Message.PressedEscape()),
    },
  ),
}));
