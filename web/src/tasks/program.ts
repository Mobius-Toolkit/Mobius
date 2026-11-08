import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { FetchBoard } from "./commands";
import { Flags } from "./flags";
import { initialModel, Model } from "./model";
import { ports } from "./ports";
import { subscriptions } from "./subscriptions";
import { update } from "./update";
import { view } from "./view";

export function createTasksProgram(container: HTMLElement, flags: Flags) {
  return Runtime.makeElement({
    Model,
    Flags,
    flags: Effect.succeed(flags),
    init: (f) => ({ model: initialModel(f), commands: [FetchBoard()] }),
    update,
    view,
    subscriptions,
    container,
    ports,
  });
}
