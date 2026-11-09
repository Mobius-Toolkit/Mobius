import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { FetchCheckup, FetchTools } from "./commands";
import { Flags } from "./flags";
import { initialModel, Model } from "./model";
import { ports } from "./ports";
import { subscriptions } from "./subscriptions";
import { update } from "./update";
import { view } from "./view";

function initialCommands(flags: Flags) {
  switch (flags.page) {
    case "overview":
    case "tools":
      return [FetchTools()];
    case "permissions":
    case "labels":
    case "workstreams":
      return [FetchCheckup({ organization: flags.organization })];
  }
}

export function createCheckupProgram(container: HTMLElement, flags: Flags) {
  return Runtime.makeElement({
    Model,
    Flags,
    flags: Effect.succeed(flags),
    init: () => ({ model: initialModel(flags), commands: initialCommands(flags) }),
    update,
    view,
    subscriptions,
    container,
    ports,
  });
}
