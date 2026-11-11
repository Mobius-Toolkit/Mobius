import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import { back } from "foldkit/navigation";
import { SwitchKind } from "../schemas";
import { Message } from "./message";

export const RequestSwitch = Command.define("RequestSwitch", {
  args: { kind: SwitchKind, on: Schema.Boolean },
  messages: [Message.CompletedRequestSwitch],
  execute: ({ kind, on }) =>
    Effect.sync(() => {
      window.dispatchEvent(new CustomEvent("mobius:switch", { detail: { kind, on } }));
    }).pipe(Effect.as(Message.CompletedRequestSwitch())),
});

export const GoBack = Command.define("GoBack", {
  messages: [Message.CompletedGoBack],
  execute: back().pipe(Effect.as(Message.CompletedGoBack())),
});
