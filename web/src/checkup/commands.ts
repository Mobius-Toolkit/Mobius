import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import { pushUrl } from "@/lib/navigation";
import {
  fixLabels,
  getCheckup,
  getCheckupTools,
  setToolAutoUpgrade,
  upgradeCheckupTool,
} from "@/api/api.gen";
import { answer, request } from "@/lib/effects";
import { showNotice } from "@/lib/notice";
import { Message } from "./message";
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
  messages: [Message.ToolsUpgraded, Message.UpgradeLoginRequired, Message.UpgradeFailed],
  execute: ({ tool }) =>
    request(() => upgradeCheckupTool({ package: tool.package })).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.UpgradeLoginRequired();
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
  messages: [Message.ToolsUpgraded, Message.UpgradeLoginRequired, Message.UpgradeFailed],
  execute: ({ tool, on }) =>
    request(() => setToolAutoUpgrade({ package: tool.package, on })).pipe(
      Effect.map((res) => {
        if (res.status === 401) {
          return Message.UpgradeLoginRequired();
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
  messages: [Message.LabelsFixed, Message.FixLoginRequired, Message.LabelFixFailed],
  execute: ({ organization }) =>
    answer(() => fixLabels({ organization }), {
      done: Message.LabelsFixed,
      login: Message.FixLoginRequired,
      failed: (error) => Message.LabelFixFailed({ error }),
    }),
});

export const Navigate = Command.define("Navigate", {
  args: { path: Schema.String },
  messages: [Message.CompletedNavigate],
  execute: ({ path }) => pushUrl(path).pipe(Effect.as(Message.CompletedNavigate())),
});

export const RequestLogin = Command.define("RequestLogin", {
  messages: [Message.CompletedRequestLogin],
  execute: Effect.sync(() => {
    window.dispatchEvent(new CustomEvent("mobius:login"));
  }).pipe(Effect.as(Message.CompletedRequestLogin())),
});

export const ReportFixButton = Command.define("ReportFixButton", {
  args: { button: Schema.NullOr(FixButton) },
  messages: [Message.CompletedReportFixButton],
  execute: () => Effect.succeed(Message.CompletedReportFixButton()),
});
