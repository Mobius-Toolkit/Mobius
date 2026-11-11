import { Schema } from "effect";
import { defineMessageUnion } from "foldkit/message";
import { UrlRequest } from "foldkit/navigation";
import { Url } from "foldkit/url";
import {
  Answered,
  CIStop,
  Drain,
  GitHubApp,
  HeldWork,
  InboxItem,
  NeedsHuman,
  Pause,
  Switches,
  Unread,
  Workstream,
  WorkstreamAutopilot,
} from "./model";
import { Message as ChatMessage } from "@/chat/message";
import {
  ChatMessage as LiveChatMessage,
  ChatState as LiveChatState,
  InboxItem as LiveInboxItem,
  PendingAction,
} from "@/chat/schemas";
import { Agent } from "@/transcript/schemas";
import { SwitchKind } from "@/settings/schemas";
import { Message as PanelMessage } from "@/panel/message";
import { Message as WorkstreamsMessage } from "@/workstreams/message";
import { Message as TasksMessage } from "@/tasks/message";
import { Message as AgentsMessage } from "@/agents/message";
import { Message as CheckupMessage } from "@/checkup/message";
import { Message as UsageMessage } from "@/usage/message";
import { Message as SettingsMessage } from "@/settings/overview/message";
import { Message as RolesMessage } from "@/settings/roles/message";
import { Message as MemoryMessage } from "@/settings/memory/message";
import { Message as McpServersMessage } from "@/settings/mcp-servers/message";
import { Message as SwitchesMessage } from "@/settings/switches/message";
import { Message as VoiceMessage } from "@/settings/voice/message";
import { Message as DevicesMessage } from "@/settings/devices/message";
import { Message as GitHubMessage } from "@/settings/github/message";

export const Message = defineMessageUnion({
  // Routing
  UrlRequested: { request: UrlRequest },
  UrlChanged: { url: Url },
  NavigateRequested: { path: Schema.String },
  BackRequested: { parent: Schema.String },
  CompletedNavigate: {},
  CompletedReplaceUrl: {},
  CompletedGoBack: {},
  CompletedLoadExternal: {},
  CompletedFocusSheet: {},

  // Authentication
  ShowLogin: {},
  HideLogin: {},
  PasswordInputChanged: { password: Schema.String },
  SubmitLoginRequested: {},
  LoginSucceeded: {},
  LoginFailed: { error: Schema.String },

  // Initial & App loading
  LoadAppRequested: {},
  AppsAndOrganizationsLoaded: {
    apps: Schema.Array(GitHubApp),
    organizations: Schema.Array(Schema.String),
  },
  LoadAppFailed: { error: Schema.String },
  SelectOrganization: { organization: Schema.String },

  // Connection & Live events
  ConnectionChanged: { link: Schema.Literals(["connected", "connecting", "offline"]) },
  OnlineChanged: { online: Schema.Boolean },
  WideChanged: { wide: Schema.Boolean },
  LiveOpened: {},
  LiveConnecting: {},
  LiveConnected: {},
  LiveLoginRequired: {},
  RepositoriesChanged: {},

  // Store changes
  UnreadStoreChanged: { unread: Schema.Array(Unread) },
  WorkstreamsStoreChanged: {
    workstreams: Schema.Array(Workstream),
    needsHuman: Schema.Array(NeedsHuman),
    working: Schema.Array(Schema.String),
  },
  InboxStoreChanged: {
    items: Schema.Array(InboxItem),
    answered: Schema.Array(Answered),
  },
  NoticeStoreChanged: { notice: Schema.String },
  NewBuildStoreChanged: { newBuild: Schema.Boolean },

  // Alerts
  CIStopsChanged: { stops: Schema.Array(CIStop) },
  PausesChanged: { pauses: Schema.Array(Pause) },
  ResumePauseRequested: { harness: Schema.String },
  ResumePauseSucceeded: { harness: Schema.String },
  ResumePauseFailed: { harness: Schema.String, error: Schema.String },

  SwitchesChanged: { switches: Switches },
  ToggleSwitchRequested: { kind: SwitchKind, on: Schema.Boolean },
  ToggleSwitchSucceeded: { kind: SwitchKind, on: Schema.Boolean },
  ToggleSwitchFailed: { kind: SwitchKind, error: Schema.String },
  OpenHeldSheet: { title: Schema.String, held: Schema.Array(HeldWork) },
  CloseHeldSheet: {},

  // Upgrade
  DrainChanged: { drain: Drain },
  UpgradeFailureChanged: { failure: Schema.String },
  CheckUpgradeRequested: {},
  ReleaseVersionChecked: { version: Schema.String },
  UpgradeInfoLoaded: {
    version: Schema.String,
    drain: Schema.NullOr(Drain),
    failure: Schema.String,
  },
  ShowUpgradeChanges: {},
  HideUpgradeChanges: {},
  ReleaseChangesLoaded: { changes: Schema.Array(Schema.String) },
  ReleaseChangesFailed: { error: Schema.String },
  StartUpgradeRequested: {},
  StartUpgradeSucceeded: {},
  StartUpgradeFailed: { error: Schema.String },
  CancelUpgradeRequested: {},
  CancelUpgradeSucceeded: {},
  CancelUpgradeFailed: { error: Schema.String },

  // Menu & UI actions
  ToggleMenu: { id: Schema.String },
  CloseMenu: {},
  PressedEscape: {},

  // Autopilot & Workstream actions
  SelectAutopilotRequested: { level: WorkstreamAutopilot },
  SelectAutopilotSucceeded: {
    from: WorkstreamAutopilot,
    to: WorkstreamAutopilot,
    number: Schema.Number,
  },
  SelectAutopilotFailed: { error: Schema.String },
  CloseWorkstreamRequested: {},
  CloseWorkstreamSucceeded: { number: Schema.Number },
  CloseWorkstreamFailed: { error: Schema.String },
  SetMobileTasksOpen: { open: Schema.Boolean },

  // Forwarded live events to active screens
  ForwardWorkstreamsEvent: {},
  ForwardAgentEvent: { agent: Agent },
  ForwardRecurringEvent: { repository: Schema.String, number: Schema.Number },
  ForwardInboxEvent: {},
  ForwardLiveMessage: { message: LiveChatMessage },
  ForwardLiveChatState: { state: LiveChatState },
  ForwardLiveAction: { action: PendingAction },
  ForwardLiveQuestion: { question: LiveInboxItem },

  // Submodel messages
  GotChatMessage: { message: ChatMessage },
  GotPanelMessage: { message: PanelMessage },
  GotInboxMessage: { message: ChatMessage },
  GotWorkstreamsMessage: { message: WorkstreamsMessage },
  GotTasksMessage: { message: TasksMessage },
  GotAgentsMessage: { message: AgentsMessage },
  GotCheckupMessage: { message: CheckupMessage },
  GotUsageMessage: { message: UsageMessage },
  GotSettingsMessage: { message: SettingsMessage },
  GotRolesMessage: { message: RolesMessage },
  GotMemoryMessage: { message: MemoryMessage },
  GotMcpServersMessage: { message: McpServersMessage },
  GotSwitchesMessage: { message: SwitchesMessage },
  GotVoiceMessage: { message: VoiceMessage },
  GotDevicesMessage: { message: DevicesMessage },
  GotGitHubMessage: { message: GitHubMessage },
});

export type Message = typeof Message.Type;
