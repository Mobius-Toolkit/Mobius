import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import * as Port from "foldkit/port";
import { SwitchKind } from "../schemas";
import { Message } from "./message";
import { ports } from "./ports";

export const RequestSwitch = Command.define("RequestSwitch", {
  args: { kind: SwitchKind, on: Schema.Boolean },
  messages: [Message.CompletedRequestSwitch],
  execute: ({ kind, on }) =>
    Port.emit(ports.outbound.switchRequested, { kind, on }).pipe(
      Effect.as(Message.CompletedRequestSwitch()),
    ),
});

export const GoBack = Command.define("GoBack", {
  messages: [Message.CompletedGoBack],
  execute: Port.emit(ports.outbound.backRequested, undefined).pipe(
    Effect.as(Message.CompletedGoBack()),
  ),
});
