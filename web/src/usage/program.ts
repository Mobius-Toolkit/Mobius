import { Effect, Option, Schema, Stream } from "effect";
import * as Runtime from "foldkit/runtime";
import * as Subscription from "foldkit/subscription";
import { escapeKey } from "@/ui/escape";
import { Message } from "./message";
import { Flags, initialModel, Model } from "./model";
import { ports } from "./ports";
import { load, update } from "./update";
import { view } from "./view";

const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  escape: entry(
    { open: Schema.Boolean },
    {
      modelToDependencies: (model) => ({
        open: Option.isSome(model.openMenu) || model.groupSheetOpen,
      }),
      dependenciesToStream: ({ open }) =>
        open ? escapeKey(Message.PressedEscape()) : Stream.empty,
    },
  ),
}));

export function createUsageProgram(container: HTMLElement, flags: Flags) {
  const model = initialModel(flags);
  return Runtime.makeElement({
    Model,
    Flags,
    flags: Effect.succeed(flags),
    init: () => ({ model, commands: [load(model)] }),
    update,
    view,
    subscriptions,
    container,
    ports,
  });
}
