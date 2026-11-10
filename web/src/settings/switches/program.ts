import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import * as Port from "foldkit/port";
import * as Subscription from "foldkit/subscription";
import { Message } from "./message";
import { Flags, initialModel, Model } from "./model";
import { ports } from "./ports";
import { update } from "./update";
import { view } from "./view";

const subscriptions = Subscription.make<Model, Message>()(() => ({
  switchesPort: Port.subscriptionEntry(ports.inbound.switches, (switches) =>
    Message.ReceivedSwitches({ switches }),
  ),
  errorsPort: Port.subscriptionEntry(ports.inbound.errors, (errors) =>
    Message.ReceivedErrors({ errors }),
  ),
}));

export function createSwitchesProgram(container: HTMLElement, flags: Flags) {
  return Runtime.makeElement({
    Model,
    Flags,
    flags: Effect.succeed(flags),
    init: () => ({ model: initialModel, commands: [] }),
    update,
    view,
    subscriptions,
    container,
    ports,
  });
}
