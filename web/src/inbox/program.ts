import { Effect } from "effect";
import * as Runtime from "foldkit/runtime";
import { initialModel, Model } from "@/chat/model";
import { itemSubscriptions } from "@/chat/subscriptions";
import { update } from "@/chat/update";
import { Flags } from "./flags";
import { ports } from "./ports";
import { view } from "./view";

// The Inbox shows the rows and the answer panel of the chat program, with no chat.
export function createInboxProgram(container: HTMLElement, flags: Flags) {
  return Runtime.makeElement({
    Model,
    Flags,
    flags: Effect.succeed(flags),
    init: ({ organization }) => ({
      model: initialModel({ organization, repository: "", workstream: 0, agent: "Coordinator" }),
      commands: [],
    }),
    update,
    view,
    subscriptions: itemSubscriptions,
    container,
    ports,
  });
}
