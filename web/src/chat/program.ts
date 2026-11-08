import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { FetchPage, ListQueued, LoadDraft } from "./commands";
import { Flags } from "./flags";
import { initialModel, Model } from "./model";
import { ports } from "./ports";
import { subscriptions } from "./subscriptions";
import { update } from "./update";
import { view } from "./view";

export function createChatProgram(container: HTMLElement, flags: Flags) {
  return Runtime.makeElement({
    Model,
    Flags,
    flags: Effect.succeed(flags),
    init: (f) => ({
      model: initialModel(f),
      commands: [
        FetchPage({
          organization: flags.organization,
          repository: flags.repository,
          workstream: flags.workstream,
          isInitial: true,
          known: null,
          sent: null,
        }),
        ListQueued({
          organization: flags.organization,
          repository: flags.repository,
          workstream: flags.workstream,
        }),
        LoadDraft({
          organization: flags.organization,
          repository: flags.repository,
          workstream: flags.workstream,
        }),
      ],
    }),
    update,
    view,
    subscriptions,
    container,
    ports,
  });
}
