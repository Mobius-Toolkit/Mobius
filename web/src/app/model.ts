import { Option, Schema } from "effect";
import {
  GetDrainResponse,
  GetUpgradeResponse,
  GetSwitchesResponse,
  ListCIStopsResponse,
  ListGitHubAppsResponse,
  ListInboxResponse,
  ListNeedsHumanResponse,
  ListPausesResponse,
  ListUnreadResponse,
  ListWorkstreamsResponse,
} from "@/api/schemas.gen";
import { AppRoute } from "./route";
import { Model as ChatModel } from "@/chat/model";
import { Model as PanelModel } from "@/panel/model";
import { Model as WorkstreamsModel } from "@/workstreams/model";
import { Model as TasksModel } from "@/tasks/model";
import { Model as AgentsModel } from "@/agents/model";
import { Model as CheckupModel } from "@/checkup/model";
import { Model as UsageModel } from "@/usage/model";
import { Model as SettingsModel } from "@/settings/overview/model";
import { Model as RolesModel } from "@/settings/roles/model";
import { Model as MemoryModel } from "@/settings/memory/model";
import { Model as McpServersModel } from "@/settings/mcp-servers/model";
import { Model as SwitchesModel } from "@/settings/switches/model";
import { Model as VoiceModel } from "@/settings/voice/model";
import { Model as DevicesModel } from "@/settings/devices/model";
import { Model as GitHubModel } from "@/settings/github/model";

export const GitHubApp = ListGitHubAppsResponse.fields.data.value;
export type GitHubApp = typeof GitHubApp.Type;

export const CIStop = ListCIStopsResponse.fields.data.value;
export type CIStop = typeof CIStop.Type;

export const Pause = ListPausesResponse.fields.data.value;
export type Pause = typeof Pause.Type;

export const Switches = GetSwitchesResponse.fields.data;
export type Switches = typeof Switches.Type;

export const HeldWork = Switches.fields.held.value;
export type HeldWork = typeof HeldWork.Type;

export const Drain = GetDrainResponse.fields.data;
export type Drain = typeof Drain.Type;

export const Upgrade = GetUpgradeResponse.fields.data;
export type Upgrade = typeof Upgrade.Type;

export const Unread = ListUnreadResponse.fields.data.value;
export type Unread = typeof Unread.Type;

export const WorkstreamAutopilot = Schema.Literals(["off", "start", "merge", "start_and_merge"]);
export type WorkstreamAutopilot = typeof WorkstreamAutopilot.Type;

export const Workstream = ListWorkstreamsResponse.fields.data.value;
export type Workstream = typeof Workstream.Type;

export const InboxItem = ListInboxResponse.fields.data.value;
export type InboxItem = typeof InboxItem.Type;

export const NeedsHuman = ListNeedsHumanResponse.fields.data.value;
export type NeedsHuman = typeof NeedsHuman.Type;

export const Answered = Schema.Struct({
  id: Schema.Number,
  repository: Schema.String,
  issue: Schema.Number,
  time: Schema.Number,
  item: Schema.Boolean,
  mark: Schema.Boolean,
});
export type Answered = typeof Answered.Type;

export const UpgradeState = Schema.Struct({
  version: Schema.String,
  drain: Schema.Option(Drain),
  step: Upgrade.fields.step,
  tries: Schema.Number,
  failure: Schema.String,
  nextTry: Schema.Option(Schema.Number),
  now: Schema.Number,
  upgrading: Schema.Boolean,
  awaitingStart: Schema.Boolean,
  cancelling: Schema.Boolean,
  changesShown: Schema.Boolean,
  changes: Schema.Option(Schema.Array(Schema.String)),
  changesError: Schema.String,
});
export type UpgradeState = typeof UpgradeState.Type;

export const ChosenAutopilot = Schema.Struct({
  from: WorkstreamAutopilot,
  to: WorkstreamAutopilot,
});
export type ChosenAutopilot = typeof ChosenAutopilot.Type;

export const HeldSheet = Schema.Struct({
  title: Schema.String,
  held: Schema.Array(HeldWork),
});
export type HeldSheet = typeof HeldSheet.Type;

// A page shows no badge while it starts, until the start limit ends.
export const Connection = Schema.Literals(["starting", "connected", "connecting", "offline"]);
export type Connection = typeof Connection.Type;

export const Model = Schema.Struct({
  route: AppRoute,

  // Login
  loginShown: Schema.Boolean,
  passwordInput: Schema.String,
  loginBusy: Schema.Boolean,
  loginError: Schema.String,

  // Apps & Organizations
  apps: Schema.Option(Schema.Array(GitHubApp)),
  organizations: Schema.Array(Schema.String),
  organization: Schema.String,
  appError: Schema.Option(Schema.String),

  // Connection
  link: Connection,
  online: Schema.Boolean,

  // Plain stores
  unread: Schema.Array(Unread),
  workstreams: Schema.Array(Workstream),
  workstreamsWorking: Schema.Array(Schema.String),
  needsHuman: Schema.Array(NeedsHuman),
  inbox: Schema.Array(InboxItem),
  inboxAnswered: Schema.Array(Answered),
  notice: Schema.String,
  newBuild: Schema.Boolean,

  // Global Alerts
  ciStops: Schema.Array(CIStop),
  pauses: Schema.Array(Pause),
  pauseBusy: Schema.Option(Schema.String),
  pauseError: Schema.Option(Schema.String),

  switches: Schema.Option(Switches),
  switchErrors: Schema.Record(Schema.String, Schema.String),
  // The value of each switch on the server at the time of its failure. The first other value clears the error.
  switchFailures: Schema.Record(Schema.String, Schema.Boolean),
  switchBusy: Schema.Option(Schema.String),
  heldSheet: Schema.Option(HeldSheet),

  upgrade: UpgradeState,

  // Screen UI state
  openMenu: Schema.Option(Schema.String),
  mobileTasksOpen: Schema.Boolean,
  wide: Schema.Boolean,
  autopilotBusy: Schema.Boolean,
  autopilotError: Schema.String,
  closeBusy: Schema.Boolean,
  closeError: Schema.String,
  chosenAutopilot: Schema.Option(ChosenAutopilot),

  // Submodels
  chat: Schema.Option(ChatModel),
  panel: Schema.Option(PanelModel),
  inboxProgram: Schema.Option(ChatModel),
  workstreamsProgram: Schema.Option(WorkstreamsModel),
  tasksProgram: Schema.Option(TasksModel),
  agentsProgram: Schema.Option(AgentsModel),
  checkupProgram: Schema.Option(CheckupModel),
  usageProgram: Schema.Option(UsageModel),
  settingsProgram: Schema.Option(SettingsModel),
  rolesProgram: Schema.Option(RolesModel),
  memoryProgram: Schema.Option(MemoryModel),
  mcpServersProgram: Schema.Option(McpServersModel),
  switchesProgram: Schema.Option(SwitchesModel),
  voiceProgram: Schema.Option(VoiceModel),
  devicesProgram: Schema.Option(DevicesModel),
  githubProgram: Schema.Option(GitHubModel),
});

export type Model = typeof Model.Type;

export function savedOrganization(): string {
  try {
    return localStorage.getItem("organization") ?? "";
  } catch {
    return "";
  }
}

export function saveOrganization(organization: string): void {
  try {
    localStorage.setItem("organization", organization);
  } catch {
    // The page works with no saved organization.
  }
}

export const initialUpgradeState: UpgradeState = {
  version: "",
  drain: Option.none(),
  step: "idle",
  tries: 0,
  failure: "",
  nextTry: Option.none(),
  now: 0,
  upgrading: false,
  awaitingStart: false,
  cancelling: false,
  changesShown: false,
  changes: Option.none(),
  changesError: "",
};

export const wideQuery = "(min-width: 768px)";

export const initialModel = (route: AppRoute): Model => {
  let organization = savedOrganization();
  if (route._tag === "WorkstreamChat") {
    organization = route.owner;
  }
  return {
    route,
    loginShown: false,
    passwordInput: "",
    loginBusy: false,
    loginError: "",
    apps: Option.none(),
    organizations: [],
    organization,
    appError: Option.none(),
    link: "starting",
    online: typeof navigator !== "undefined" ? navigator.onLine : true,
    unread: [],
    workstreams: [],
    workstreamsWorking: [],
    needsHuman: [],
    inbox: [],
    inboxAnswered: [],
    notice: "",
    newBuild: false,
    ciStops: [],
    pauses: [],
    pauseBusy: Option.none(),
    pauseError: Option.none(),
    switches: Option.none(),
    switchErrors: {},
    switchFailures: {},
    switchBusy: Option.none(),
    heldSheet: Option.none(),
    upgrade: initialUpgradeState,
    openMenu: Option.none(),
    mobileTasksOpen: false,
    wide: typeof window !== "undefined" ? window.matchMedia(wideQuery).matches : true,
    autopilotBusy: false,
    autopilotError: "",
    closeBusy: false,
    closeError: "",
    chosenAutopilot: Option.none(),
    chat: Option.none(),
    panel: Option.none(),
    inboxProgram: Option.none(),
    workstreamsProgram: Option.none(),
    tasksProgram: Option.none(),
    agentsProgram: Option.none(),
    checkupProgram: Option.none(),
    usageProgram: Option.none(),
    settingsProgram: Option.none(),
    rolesProgram: Option.none(),
    memoryProgram: Option.none(),
    mcpServersProgram: Option.none(),
    switchesProgram: Option.none(),
    voiceProgram: Option.none(),
    devicesProgram: Option.none(),
    githubProgram: Option.none(),
  };
};
