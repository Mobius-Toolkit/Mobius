import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import * as Port from "foldkit/port";
import {
  fixLabels,
  getCheckup,
  getCheckupTools,
  setToolAutoUpgrade,
  upgradeCheckupTool,
} from "@/api/api.gen";
import { answer, request, store } from "@/lib/effects";
import { showNotice } from "@/lib/notice";
import { addCoordinatorMessage } from "@/lib/outbox";
import { Message } from "./message";
import { ports } from "./ports";
import { FixButton, ToolCheck } from "./schemas";

export const FetchTools = Command.define("FetchTools", {
  messages: [Message.ToolsLoaded, Message.LoginRequired, Message.ToolsFailed],
  execute: request(() => getCheckupTools()).pipe(
    Effect.map((res) => {
      if (res.status === 401) {
        return Message.LoginRequired();
      }
      if (res.status !== 200) {
        return Message.ToolsFailed({ error: res.data.error });
      }
      return Message.ToolsLoaded({ tools: res.data.data });
    }),
    Effect.catchTag("RequestFailed", (error) =>
      Effect.succeed(Message.ToolsFailed({ error: error.message })),
    ),
  ),
});

export const FetchCheckup = Command.define("FetchCheckup", {
  args: { organization: Schema.String },
  messages: [Message.CheckupLoaded, Message.LoginRequired, Message.CheckupFailed],
  execute: ({ organization }) =>
    request(() => getCheckup({ organization })).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.CheckupFailed({ organization, error: res.data.error });
        }
        return Message.CheckupLoaded({ organization, checkup: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.CheckupFailed({ organization, error: error.message })),
      ),
    ),
});

export const UpgradeTool = Command.define("UpgradeTool", {
  args: { tool: ToolCheck },
  messages: [Message.ToolsUpgraded, Message.LoginRequired, Message.UpgradeFailed],
  execute: ({ tool }) =>
    request(() => upgradeCheckupTool({ package: tool.package })).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.UpgradeFailed({ error: res.data.error });
        }
        showNotice(`Upgraded ${tool.name} to ${tool.newest}`);
        return Message.ToolsUpgraded({ tools: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.UpgradeFailed({ error: error.message })),
      ),
    ),
});

export const SetAutoUpgrade = Command.define("SetAutoUpgrade", {
  args: { tool: ToolCheck, on: Schema.Boolean },
  messages: [Message.ToolsUpgraded, Message.LoginRequired, Message.UpgradeFailed],
  execute: ({ tool, on }) =>
    request(() => setToolAutoUpgrade({ package: tool.package, on })).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.LoginRequired();
        }
        if (res.status !== 200) {
          return Message.UpgradeFailed({ error: res.data.error });
        }
        showNotice(`Auto-upgrade of ${tool.name} ${on ? "on" : "off"}`);
        return Message.ToolsUpgraded({ tools: res.data.data });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.UpgradeFailed({ error: error.message })),
      ),
    ),
});

export const FixLabels = Command.define("FixLabels", {
  args: { organization: Schema.String },
  messages: [Message.LabelsFixed, Message.LoginRequired, Message.LabelFixFailed],
  execute: ({ organization }) =>
    answer(() => fixLabels({ organization }), {
      done: Message.LabelsFixed,
      login: Message.LoginRequired,
      failed: (error) => Message.LabelFixFailed({ error }),
    }),
});

export const CreateWorkstream = Command.define("CreateWorkstream", {
  args: { organization: Schema.String, request: Schema.String },
  messages: [Message.WorkstreamRequested, Message.CreateFailed],
  execute: ({ organization, request: text }) =>
    store(() => addCoordinatorMessage(organization, text)).pipe(
      Effect.as(Message.WorkstreamRequested({ organization })),
      Effect.catchTag("StoreFailed", (error) =>
        Effect.succeed(Message.CreateFailed({ error: error.message })),
      ),
    ),
});

export const OpenChat = Command.define("OpenChat", {
  args: { organization: Schema.String },
  messages: [Message.CompletedOpenChat],
  execute: ({ organization }) =>
    Port.emit(ports.outbound.chatRequested, organization).pipe(
      Effect.as(Message.CompletedOpenChat()),
    ),
});

export const Navigate = Command.define("Navigate", {
  args: { path: Schema.String },
  messages: [Message.CompletedNavigate],
  execute: ({ path }) =>
    Port.emit(ports.outbound.navigationRequested, path).pipe(
      Effect.as(Message.CompletedNavigate()),
    ),
});

export const GoBack = Command.define("GoBack", {
  messages: [Message.CompletedGoBack],
  execute: Port.emit(ports.outbound.backRequested, undefined).pipe(
    Effect.as(Message.CompletedGoBack()),
  ),
});

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Port.emit(ports.outbound.loginRequested, undefined).pipe(
    Effect.as(Message.CompletedRequestLogin()),
  ),
});

export const ReportFixButton = Command.define("ReportFixButton", {
  args: { button: Schema.NullOr(FixButton) },
  messages: [Message.CompletedReportFixButton],
  execute: ({ button }) =>
    Port.emit(ports.outbound.fixButtonChanged, button).pipe(
      Effect.as(Message.CompletedReportFixButton()),
    ),
});
