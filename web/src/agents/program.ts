import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { FetchAgents } from "./commands";
import { Flags } from "./flags";
import { initialModel, Model } from "./model";
import { ports } from "./ports";
import { subscriptions } from "./subscriptions";
import { update } from "./update";
import { view } from "./view";

export function createAgentsProgram(container: HTMLElement, flags: Flags) {
  return Runtime.makeElement({
    Model,
    Flags,
    flags: Effect.succeed(flags),
    init: () => ({
      model: initialModel,
      commands: [FetchAgents({ request: initialModel.request })],
    }),
    update,
    view,
    subscriptions,
    container,
    ports,
  });
}
