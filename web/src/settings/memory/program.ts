import { Effect, Option, Schema, Stream } from "effect";
import * as Port from "foldkit/port";
import * as Runtime from "foldkit/runtime";
import * as Subscription from "foldkit/subscription";
import { escapeKey } from "@/ui/escape";
import { FetchFiles, FetchVersions } from "./commands";
import { Message } from "./message";
import { Flags, initialModel, Model } from "./model";
import { ports } from "./ports";
import { update } from "./update";
import { view } from "./view";

const subscriptions = Subscription.make<Model, Message>()((entry) => ({
  appsPort: Port.subscriptionEntry(ports.inbound.apps, (apps) => Message.ReceivedApps({ apps })),
  organizationsPort: Port.subscriptionEntry(ports.inbound.organizations, (organizations) =>
    Message.ReceivedOrganizations({ organizations }),
  ),
  escape: entry(
    { editing: Schema.Boolean },
    {
      modelToDependencies: (model) => ({ editing: Option.isSome(model.editing) }),
      dependenciesToStream: ({ editing }) =>
        editing ? escapeKey(Message.PressedEscape()) : Stream.empty,
    },
  ),
}));

function initialCommands(flags: Flags) {
  switch (flags.page) {
    case "repositories":
      return [];
    case "files":
      return [FetchFiles({ owner: flags.owner, name: flags.name })];
    case "file":
      return [FetchVersions({ owner: flags.owner, name: flags.name, file: flags.file })];
  }
}

export function createMemoryProgram(container: HTMLElement, flags: Flags) {
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
