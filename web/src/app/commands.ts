import { Effect, Schema } from "effect";
import { Command } from "foldkit";
import * as Navigation from "foldkit/navigation";
import { goBackOr, pushUrl, replaceUrl, settleScroll } from "@/lib/navigation";
import {
  cancelDrain,
  completeWorkstream,
  getDrain,
  getRelease,
  getSwitches,
  getUpgrade,
  listCIStops,
  listGitHubApps,
  listOrganizations,
  listPauses,
  listReleaseChanges,
  login,
  resume,
  setAutopilot,
  setSwitch,
  startUpgrade,
} from "@/api/api.gen";
import { levelName } from "@/lib/autopilot";
import { reloadOnNewBuild } from "@/lib/build";
import { request } from "@/lib/effects";
import { showNotice } from "@/lib/notice";
import { loadWorkstreams } from "@/lib/workstreams";
import { SwitchKind } from "@/settings/schemas";
import { Message } from "./message";
import { WorkstreamAutopilot } from "./model";

export const Navigate = Command.define("Navigate", {
  args: { path: Schema.String },
  messages: [Message.CompletedNavigate],
  execute: ({ path }) => pushUrl(path).pipe(Effect.as(Message.CompletedNavigate())),
});

export const SettleScroll = Command.define("SettleScroll", {
  messages: [Message.CompletedSettleScroll],
  execute: settleScroll.pipe(Effect.as(Message.CompletedSettleScroll())),
});

export const ReplaceUrl = Command.define("ReplaceUrl", {
  args: { path: Schema.String },
  messages: [Message.CompletedReplaceUrl],
  execute: ({ path }) => replaceUrl(path).pipe(Effect.as(Message.CompletedReplaceUrl())),
});

export const GoBack = Command.define("GoBack", {
  args: { parent: Schema.String },
  messages: [Message.CompletedGoBack],
  execute: ({ parent }) => goBackOr(parent).pipe(Effect.as(Message.CompletedGoBack())),
});

export const LoadExternal = Command.define("LoadExternal", {
  args: { href: Schema.String },
  messages: [Message.CompletedLoadExternal],
  execute: ({ href }) => Navigation.load(href).pipe(Effect.as(Message.CompletedLoadExternal())),
});

export const LoadApp = Command.define("LoadApp", {
  messages: [Message.AppsAndOrganizationsLoaded, Message.ShowLogin, Message.LoadAppFailed],
  execute: Effect.gen(function* () {
    const appsRes = yield* request(() => listGitHubApps());
    if (appsRes.status === 401) {
      return Message.ShowLogin();
    }
    if (appsRes.status !== 200) {
      return Message.LoadAppFailed({ error: appsRes.data.error });
    }

    const orgsRes = yield* request(() => listOrganizations());
    if (orgsRes.status !== 200) {
      return Message.LoadAppFailed({ error: orgsRes.data.error });
    }

    return Message.AppsAndOrganizationsLoaded({
      apps: appsRes.data.data,
      organizations: orgsRes.data.data,
    });
  }).pipe(
    Effect.catchTag("RequestFailed", (error) =>
      Effect.succeed(Message.LoadAppFailed({ error: error.message })),
    ),
  ),
});

export const SubmitLogin = Command.define("SubmitLogin", {
  args: { password: Schema.String },
  messages: [Message.LoginSucceeded, Message.LoginFailed],
  execute: ({ password }) =>
    request(() => login({ password })).pipe(
      Effect.map((res) => {
        if (res.status === 204) {
          return Message.LoginSucceeded();
        }
        return Message.LoginFailed({ error: res.data.error });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.LoginFailed({ error: error.message })),
      ),
    ),
});

export const FetchPauses = Command.define("FetchPauses", {
  messages: [Message.PausesChanged],
  execute: request(() => listPauses()).pipe(
    Effect.map((res) => {
      if (res.status === 200) {
        return Message.PausesChanged({ pauses: res.data.data });
      }
      return Message.PausesChanged({ pauses: [] });
    }),
    Effect.catchTag("RequestFailed", () => Effect.succeed(Message.PausesChanged({ pauses: [] }))),
  ),
});

export const FetchCIStops = Command.define("FetchCIStops", {
  messages: [Message.CIStopsChanged],
  execute: request(() => listCIStops()).pipe(
    Effect.map((res) => {
      if (res.status === 200) {
        return Message.CIStopsChanged({ stops: res.data.data });
      }
      return Message.CIStopsChanged({ stops: [] });
    }),
    Effect.catchTag("RequestFailed", () => Effect.succeed(Message.CIStopsChanged({ stops: [] }))),
  ),
});

export const FetchSwitches = Command.define("FetchSwitches", {
  messages: [Message.SwitchesChanged],
  execute: request(() => getSwitches()).pipe(
    Effect.map((res) => {
      if (res.status === 200) {
        return Message.SwitchesChanged({ switches: res.data.data });
      }
      return Message.SwitchesChanged({
        switches: { start: true, merge: true, repairs: true, held: [] },
      });
    }),
    Effect.catchTag("RequestFailed", () =>
      Effect.succeed(
        Message.SwitchesChanged({
          switches: { start: true, merge: true, repairs: true, held: [] },
        }),
      ),
    ),
  ),
});

export const ResumePause = Command.define("ResumePause", {
  args: { harness: Schema.String },
  messages: [Message.ResumePauseSucceeded, Message.ResumePauseFailed, Message.ShowLogin],
  execute: ({ harness }) =>
    request(() => resume(harness)).pipe(
      Effect.map((res) => {
        if (res.status === 204) {
          return Message.ResumePauseSucceeded({ harness });
        }
        if (res.status === 401) {
          return Message.ShowLogin();
        }
        return Message.ResumePauseFailed({ harness, error: res.data.error });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.ResumePauseFailed({ harness, error: error.message })),
      ),
    ),
});

export const ToggleSwitch = Command.define("ToggleSwitch", {
  args: { kind: SwitchKind, on: Schema.Boolean },
  messages: [Message.ToggleSwitchSucceeded, Message.ToggleSwitchFailed, Message.ShowLogin],
  execute: ({ kind, on }) =>
    request(() => setSwitch(kind, { on })).pipe(
      Effect.map((res) => {
        if (res.status === 204) {
          showNotice(
            `${kind === "start" ? "Start" : kind === "merge" ? "Merge" : "Repairs"} is ${on ? "on" : "off"}`,
          );
          return Message.ToggleSwitchSucceeded({ kind, on });
        }
        if (res.status === 401) {
          return Message.ShowLogin();
        }
        return Message.ToggleSwitchFailed({ kind, error: res.data.error });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.ToggleSwitchFailed({ kind, error: error.message })),
      ),
    ),
});

export const SelectAutopilot = Command.define("SelectAutopilot", {
  args: {
    owner: Schema.String,
    name: Schema.String,
    number: Schema.Number,
    from: WorkstreamAutopilot,
    to: WorkstreamAutopilot,
  },
  messages: [Message.SelectAutopilotSucceeded, Message.SelectAutopilotFailed],
  execute: ({ owner, name, number, from, to }) =>
    request(() => setAutopilot(owner, name, number, { level: to })).pipe(
      Effect.map((res) => {
        if (res.status === 204) {
          showNotice(`Autopilot of #${number}: ${levelName(to)}`);
          return Message.SelectAutopilotSucceeded({
            from,
            to,
            number,
          });
        }
        return Message.SelectAutopilotFailed({ error: res.data.error });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.SelectAutopilotFailed({ error: error.message })),
      ),
      Effect.tap(() =>
        Effect.sync(() => {
          void loadWorkstreams();
        }),
      ),
    ),
});

export const CloseWorkstream = Command.define("CloseWorkstream", {
  args: { owner: Schema.String, name: Schema.String, number: Schema.Number },
  messages: [Message.CloseWorkstreamSucceeded, Message.CloseWorkstreamFailed],
  execute: ({ owner, name, number }) =>
    request(() => completeWorkstream(owner, name, number)).pipe(
      Effect.map((res) => {
        if (res.status === 204) {
          showNotice(`Closed the Workstream #${number}`);
          return Message.CloseWorkstreamSucceeded({ number });
        }
        return Message.CloseWorkstreamFailed({ error: res.data.error });
      }),
      Effect.catchTag("RequestFailed", (error) =>
        Effect.succeed(Message.CloseWorkstreamFailed({ error: error.message })),
      ),
    ),
});

export const CheckUpgrade = Command.define("CheckUpgrade", {
  messages: [Message.UpgradeInfoLoaded],
  execute: Effect.gen(function* () {
    const releaseRes = yield* request(() => getRelease());
    const version = releaseRes.status === 200 ? releaseRes.data.data.version : null;

    const drainRes = yield* request(() => getDrain());
    const drain = drainRes.status === 200 ? drainRes.data.data : null;

    const upgradeRes = yield* request(() => getUpgrade());
    const failure = upgradeRes.status === 200 ? upgradeRes.data.data.failure : null;

    return Message.UpgradeInfoLoaded({ version, drain, failure });
  }).pipe(
    Effect.catchTag("RequestFailed", () =>
      Effect.succeed(Message.UpgradeInfoLoaded({ version: null, drain: null, failure: null })),
    ),
  ),
});

export const LoadReleaseChanges = Command.define("LoadReleaseChanges", {
  messages: [Message.ReleaseChangesLoaded, Message.ReleaseChangesFailed, Message.ShowLogin],
  execute: request(() => listReleaseChanges()).pipe(
    Effect.map((res) => {
      if (res.status === 200) {
        return Message.ReleaseChangesLoaded({ changes: res.data.data });
      }
      if (res.status === 401) {
        return Message.ShowLogin();
      }
      return Message.ReleaseChangesFailed({ error: res.data.error });
    }),
    Effect.catchTag("RequestFailed", (error) =>
      Effect.succeed(Message.ReleaseChangesFailed({ error: error.message })),
    ),
  ),
});

export const StartUpgrade = Command.define("StartUpgrade", {
  messages: [Message.StartUpgradeSucceeded, Message.StartUpgradeFailed],
  execute: request(() => startUpgrade()).pipe(
    Effect.map((res) => {
      if (res.status === 200 && res.data.data.end === "drained") {
        void reloadOnNewBuild();
        return Message.StartUpgradeSucceeded();
      }
      if (res.status !== 200) {
        return Message.StartUpgradeFailed({ error: res.data.error });
      }
      return Message.StartUpgradeSucceeded();
    }),
    Effect.catchTag("RequestFailed", (error) =>
      Effect.succeed(Message.StartUpgradeFailed({ error: error.message })),
    ),
  ),
});

export const CancelUpgrade = Command.define("CancelUpgrade", {
  messages: [Message.CancelUpgradeSucceeded, Message.CancelUpgradeFailed],
  execute: request(() => cancelDrain()).pipe(
    Effect.map((res) => {
      if (res.status === 204) {
        return Message.CancelUpgradeSucceeded();
      }
      return Message.CancelUpgradeFailed({ error: res.data.error });
    }),
    Effect.catchTag("RequestFailed", (error) =>
      Effect.succeed(Message.CancelUpgradeFailed({ error: error.message })),
    ),
  ),
});
