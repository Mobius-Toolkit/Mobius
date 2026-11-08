import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import * as Port from "foldkit/port";
import { listActiveAgents } from "@/api/api.gen";
import { request as send } from "@/lib/effects";
import { Message } from "./message";
import { ports } from "./ports";

export const FetchAgents = Command.define("FetchAgents", {
  args: { request: Schema.Number },
  messages: [Message.AgentsLoaded, Message.LoginRequired, Message.LoadFailed],
  execute: ({ request }) =>
    send(() => listActiveAgents()).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.LoadFailed({ request, error: res.data.error });
        }
        return Message.AgentsLoaded({ request, agents: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.LoadFailed({ request, error: error.message })),
      ),
    ),
});

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Port.emit(ports.outbound.loginRequested, undefined).pipe(
    Effect.as(Message.CompletedRequestLogin()),
  ),
});

export const ReportTitle = Command.define("ReportTitle", {
  args: { title: Schema.String },
  messages: [Message.CompletedReportTitle],
  execute: ({ title }) =>
    Port.emit(ports.outbound.titleChanged, title).pipe(Effect.as(Message.CompletedReportTitle())),
});
