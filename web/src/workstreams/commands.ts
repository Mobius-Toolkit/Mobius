import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import { pushUrl } from "@/lib/navigation";
import { Message } from "./message";

export const Navigate = Command.define("Navigate", {
  args: { path: Schema.String },
  messages: [Message.CompletedNavigate],
  execute: ({ path }) => pushUrl(path).pipe(Effect.as(Message.CompletedNavigate())),
});
