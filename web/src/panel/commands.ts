import { Effect } from "effect";
import { Command } from "foldkit";
import * as Port from "foldkit/port";
import { Message } from "./message";
import { ports } from "./ports";
import { Tab } from "./schemas";

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Port.emit(ports.outbound.loginRequested, undefined).pipe(
    Effect.as(Message.CompletedRequestLogin()),
  ),
});

export const ReportTaskAttached = Command.define("ReportTaskAttached", {
  messages: [Message.CompletedReportTaskAttached],
  execute: Port.emit(ports.outbound.taskAttached, undefined).pipe(
    Effect.as(Message.CompletedReportTaskAttached()),
  ),
});

export const ReportTab = Command.define("ReportTab", {
  args: { tab: Tab },
  messages: [Message.CompletedReportTab],
  execute: ({ tab }) =>
    Port.emit(ports.outbound.tabChanged, tab).pipe(Effect.as(Message.CompletedReportTab())),
});
