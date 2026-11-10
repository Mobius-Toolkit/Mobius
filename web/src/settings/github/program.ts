import { Effect } from "effect";
import * as Port from "foldkit/port";
import * as Runtime from "foldkit/runtime";
import * as Subscription from "foldkit/subscription";
import { Message } from "./message";
import { Flags, initialModel, Model } from "./model";
import { ports } from "./ports";
import { update } from "./update";
import { view } from "./view";

const subscriptions = Subscription.make<Model, Message>()(() => ({
  appsPort: Port.subscriptionEntry(ports.inbound.apps, (apps) => Message.ReceivedApps({ apps })),
}));

export function createGitHubProgram(container: HTMLElement, flags: Flags) {
  return Runtime.makeElement({
    Model,
    Flags,
    flags: Effect.succeed(flags),
    init: () => ({ model: initialModel(flags), commands: [] }),
    update,
    view,
    subscriptions,
    container,
    ports,
  });
}
