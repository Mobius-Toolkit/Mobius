import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { Flags } from "./flags";
import { initialModel, Model } from "./model";
import { ports } from "./ports";
import { subscriptions } from "./subscriptions";
import { commandsFor, update } from "./update";
import { view } from "./view";

export function createPanelProgram(container: HTMLElement, flags: Flags) {
  return Runtime.makeElement({
    Model,
    Flags,
    flags: Effect.succeed(flags),
    init: (f) => {
      const model = initialModel(f);
      return { model, commands: commandsFor(model) };
    },
    update,
    view,
    subscriptions,
    container,
    ports,
  });
}
