import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import { pushUrl } from "foldkit/navigation";
import { Message } from "./message";
import { Tab } from "./schemas";

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Effect.sync(() => {
    window.dispatchEvent(new CustomEvent("mobius:login"));
  }).pipe(Effect.as(Message.CompletedRequestLogin())),
});

export const Navigate = Command.define("Navigate", {
  args: { path: Schema.String },
  messages: [Message.CompletedNavigate],
  execute: ({ path }) => pushUrl(path).pipe(Effect.as(Message.CompletedNavigate())),
});

export const ReportTaskAttached = Command.define("ReportTaskAttached", {
  messages: [Message.CompletedReportTaskAttached],
  execute: Effect.succeed(Message.CompletedReportTaskAttached()),
});

export const ReportTab = Command.define("ReportTab", {
  args: { tab: Tab },
  messages: [Message.CompletedReportTab],
  execute: () => Effect.succeed(Message.CompletedReportTab()),
});
