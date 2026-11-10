import { Effect, Option, Schema, Stream } from "effect";
import * as Runtime from "foldkit/runtime";
import * as Subscription from "foldkit/subscription";
import { escapeKey } from "@/ui/escape";
import { FetchServers } from "./commands";
import { Message } from "./message";
import { Flags, initialModel, Model } from "./model";
import { ports } from "./ports";
import { update } from "./update";
import { view } from "./view";

const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  escape: entry(
    { open: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ open: Option.isSome(model.sheet) }),
      dependenciesToStream: ({ open }) =>
        open ? escapeKey(Message.PressedEscape()) : Stream.empty,
    },
  ),
}));

export function createMcpServersProgram(container: HTMLElement, flags: Flags) {
  return Runtime.makeElement({
    Model,
    Flags,
    flags: Effect.succeed(flags),
    init: (f) => ({
      model: initialModel(f),
      commands: [FetchServers({ organization: f.organization })],
    }),
    update,
    view,
    subscriptions,
    container,
    ports,
  });
}
