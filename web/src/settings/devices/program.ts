import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import * as Subscription from "foldkit/subscription";
import { FetchDevices } from "./commands";
import type { Message } from "./message";
import { Flags, initialModel, Model } from "./model";
import { ports } from "./ports";
import { update } from "./update";
import { view } from "./view";

const subscriptions = Subscription.make<Model, Message>()(() => ({}));

export function createDevicesProgram(container: HTMLElement, flags: Flags) {
  return Runtime.makeElement({
    Model,
    Flags,
    flags: Effect.succeed(flags),
    init: () => ({ model: initialModel, commands: [FetchDevices()] }),
    update,
    view,
    subscriptions,
    container,
    ports,
  });
}
