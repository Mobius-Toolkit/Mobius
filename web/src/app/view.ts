import { Option } from "effect";
import type { Document, Html, HtmlBuilder } from "foldkit/html";
import { defineView } from "foldkit/submodel";
import { view as agentsView } from "@/agents/view";
import { Message as AgentsMessage } from "@/agents/message";
import type { Model as AgentsModel } from "@/agents/model";
import { view as chatView } from "@/chat/view";
import { view as inboxView } from "@/inbox/view";
import { Message as ChatMessage } from "@/chat/message";
import type { Model as ChatModel, TaskRef } from "@/chat/model";
import { briefView, sheetView as panelSheetView, view as panelView } from "@/panel/view";
import { Message as PanelMessage } from "@/panel/message";
import type { Model as PanelModel } from "@/panel/model";
import { view as checkupView } from "@/checkup/view";
import { Message as CheckupMessage } from "@/checkup/message";
import type { Model as CheckupModel } from "@/checkup/model";
import { view as devicesView } from "@/settings/devices/view";
import { Message as DevicesMessage } from "@/settings/devices/message";
import type { Model as DevicesModel } from "@/settings/devices/model";
import { view as githubView } from "@/settings/github/view";
import { Message as GitHubMessage } from "@/settings/github/message";
import type { Model as GitHubModel } from "@/settings/github/model";
import { view as mcpServersView } from "@/settings/mcp-servers/view";
import { Message as McpServersMessage } from "@/settings/mcp-servers/message";
import type { Model as McpServersModel } from "@/settings/mcp-servers/model";
import { view as memoryView } from "@/settings/memory/view";
import { Message as MemoryMessage } from "@/settings/memory/message";
import type { Model as MemoryModel } from "@/settings/memory/model";
import { view as settingsOverviewView } from "@/settings/overview/view";
import { Message as SettingsMessage } from "@/settings/overview/message";
import type { Model as SettingsModel } from "@/settings/overview/model";
import { view as rolesView } from "@/settings/roles/view";
import { Message as RolesMessage } from "@/settings/roles/message";
import type { Model as RolesModel } from "@/settings/roles/model";
import { view as switchesView } from "@/settings/switches/view";
import { Message as SwitchesMessage } from "@/settings/switches/message";
import type { Model as SwitchesModel } from "@/settings/switches/model";
import { view as voiceView } from "@/settings/voice/view";
import { Message as VoiceMessage } from "@/settings/voice/message";
import type { Model as VoiceModel } from "@/settings/voice/model";
import { view as tasksView } from "@/tasks/view";
import { Message as TasksMessage } from "@/tasks/message";
import type { Model as TasksModel } from "@/tasks/model";
import { view as usageView } from "@/usage/view";
import { Message as UsageMessage } from "@/usage/message";
import type { Model as UsageModel } from "@/usage/model";
import { view as workstreamsView } from "@/workstreams/view";
import { Message as WorkstreamsMessage } from "@/workstreams/message";
import type { Model as WorkstreamsModel } from "@/workstreams/model";
import { renderBadge } from "@/ui/badge";
import { renderButton } from "@/ui/button";
import { fileTextIcon } from "@/ui/icons";
import { renderPageHeader } from "@/ui/page";
import { defineInPlaceMount, renderSheet } from "@/ui/sheet";
import { renderAutopilotMenu, shownAutopilot } from "./autopilot";
import { renderFrame } from "./frame";
import { renderLogin } from "./login";
import { Message } from "./message";
import type { Model } from "./model";
import { AppRoute } from "./route";

const openTasks = (panel: Option.Option<PanelModel>): TaskRef[] =>
  Option.match(
    Option.flatMap(panel, (value) => value.tasks.lines),
    {
      onNone: () => [],
      onSome: (lines) =>
        lines
          .filter((line) => line.state !== "closed")
          .map(({ repository, number }) => ({ repository, number })),
    },
  );

const inPlaceMount = defineInPlaceMount(Message.CompletedFocusSheet);

// Branded submodel views
const chatSubmodelView = defineView<ChatModel, ChatMessage, { tasks: readonly TaskRef[] }>(
  (m: ChatModel, { tasks }, h: HtmlBuilder<ChatMessage>) => chatView(m, h, tasks),
);
const panelSubmodelView = defineView<PanelModel, PanelMessage>(
  (m: PanelModel, h: HtmlBuilder<PanelMessage>) => panelView(m, h),
);
const panelSheetSubmodelView = defineView<PanelModel, PanelMessage>(
  (m: PanelModel, h: HtmlBuilder<PanelMessage>) => panelSheetView(m, h),
);
const briefSubmodelView = defineView<PanelModel, PanelMessage>(
  (m: PanelModel, h: HtmlBuilder<PanelMessage>) => briefView(m, h),
);
const workstreamsSubmodelView = defineView<WorkstreamsModel, WorkstreamsMessage>(
  (m: WorkstreamsModel, h: HtmlBuilder<WorkstreamsMessage>) => workstreamsView(m, h),
);
const tasksSubmodelView = defineView<TasksModel, TasksMessage>(
  (m: TasksModel, h: HtmlBuilder<TasksMessage>) => tasksView(m, h),
);
const inboxSubmodelView = defineView<ChatModel, ChatMessage>(
  (m: ChatModel, h: HtmlBuilder<ChatMessage>) => inboxView(m, h),
);
const agentsSubmodelView = defineView<AgentsModel, AgentsMessage>(
  (m: AgentsModel, h: HtmlBuilder<AgentsMessage>) => agentsView(m, h),
);
const usageSubmodelView = defineView<UsageModel, UsageMessage>(
  (m: UsageModel, h: HtmlBuilder<UsageMessage>) => usageView(m, h),
);
const settingsOverviewSubmodelView = defineView<SettingsModel, SettingsMessage>(
  (m: SettingsModel, h: HtmlBuilder<SettingsMessage>) => settingsOverviewView(m, h),
);
const rolesSubmodelView = defineView<RolesModel, RolesMessage>(
  (m: RolesModel, h: HtmlBuilder<RolesMessage>) => rolesView(m, h),
);
const memorySubmodelView = defineView<MemoryModel, MemoryMessage>(
  (m: MemoryModel, h: HtmlBuilder<MemoryMessage>) => memoryView(m, h),
);
const switchesSubmodelView = defineView<SwitchesModel, SwitchesMessage>(
  (m: SwitchesModel, h: HtmlBuilder<SwitchesMessage>) => switchesView(m, h),
);
const voiceSubmodelView = defineView<VoiceModel, VoiceMessage>(
  (m: VoiceModel, h: HtmlBuilder<VoiceMessage>) => voiceView(m, h),
);
const devicesSubmodelView = defineView<DevicesModel, DevicesMessage>(
  (m: DevicesModel, h: HtmlBuilder<DevicesMessage>) => devicesView(m, h),
);
const githubSubmodelView = defineView<GitHubModel, GitHubMessage>(
  (m: GitHubModel, h: HtmlBuilder<GitHubMessage>) => githubView(m, h),
);
const mcpServersSubmodelView = defineView<McpServersModel, McpServersMessage>(
  (m: McpServersModel, h: HtmlBuilder<McpServersMessage>) => mcpServersView(m, h),
);
const checkupSubmodelView = defineView<CheckupModel, CheckupMessage>(
  (m: CheckupModel, h: HtmlBuilder<CheckupMessage>) => checkupView(m, h),
);

function renderPage(h: HtmlBuilder<Message>, model: Model): Html {
  const currentOrg = model.organization;
  const route = model.route;

  const renderChat = () => {
    if (!model.organizations.includes(currentOrg)) {
      return h.div(
        [
          h.Class(
            "grid grow content-start gap-6 p-4 md:mx-auto md:my-6 md:w-[min(48rem,calc(100%-3rem))] md:grow-0 md:self-start md:rounded-xl md:border md:bg-card md:p-6",
          ),
        ],
        [
          renderPageHeader(h, "Chat"),
          h.p(
            [h.Class("text-muted-foreground")],
            [
              "Mobius reads the repositories from GitHub. The Coordinator chat opens after this step.",
            ],
          ),
        ],
      );
    }
    if (Option.isNone(model.chat)) {
      return h.empty;
    }
    return h.section(
      [h.Class("group/chat flex min-h-0 min-w-0 grow flex-col")],
      [
        h.header(
          [h.Class("hidden min-h-14 items-center gap-2 border-b px-4 py-2 md:flex")],
          [h.h2([h.Class("text-base font-semibold")], ["Chat"])],
        ),
        h.keyed("div")(
          `chat-${currentOrg}`,
          [h.Id("chat-app-wrapper"), h.Class("flex min-h-0 min-w-0 grow flex-col")],
          [
            h.submodel({
              slotId: `chat-${currentOrg}`,
              model: model.chat.value,
              view: chatSubmodelView,
              viewInputs: { tasks: [] },
              toParentMessage: (m) => Message.GotChatMessage({ message: m }),
            }),
          ],
        ),
      ],
    );
  };

  const renderCheckup = () => {
    if (Option.isNone(model.checkupProgram)) return h.empty;
    return h.submodel({
      slotId: "settings-checkup",
      model: model.checkupProgram.value,
      view: checkupSubmodelView,
      toParentMessage: (m) => Message.GotCheckupMessage({ message: m }),
    });
  };

  const renderMemory = () => {
    if (Option.isNone(model.memoryProgram)) return h.empty;
    return h.submodel({
      slotId: "settings-memory",
      model: model.memoryProgram.value,
      view: memorySubmodelView,
      toParentMessage: (m) => Message.GotMemoryMessage({ message: m }),
    });
  };

  return AppRoute.match(route, {
    CoordinatorChat: renderChat,
    NotFound: renderChat,
    WorkstreamChat: (wsRoute) => {
      const activeWorkstream = model.workstreams.find(
        (w) => w.repository === `${wsRoute.owner}/${wsRoute.name}` && w.number === wsRoute.number,
      );

      const level = shownAutopilot(model, `${wsRoute.owner}/${wsRoute.name}`, wsRoute.number);

      if (Option.isNone(model.chat) || Option.isNone(model.panel)) {
        return h.empty;
      }

      return h.div(
        [h.Class("flex min-h-0 min-w-0 grow")],
        [
          h.section(
            [h.Class("group/chat flex min-h-0 min-w-0 grow flex-col")],
            [
              h.header(
                [h.Class("hidden min-h-14 items-center gap-2 border-b px-4 py-2 md:flex")],
                [
                  h.h2(
                    [h.Class("min-w-0 truncate text-base font-semibold")],
                    [activeWorkstream?.title ?? ""],
                  ),
                  h.span([h.Class("text-sm text-muted-foreground")], ["#", String(wsRoute.number)]),
                  h.div(
                    [h.Class("ml-auto flex shrink-0 items-center gap-2")],
                    [
                      h.button(
                        [
                          h.Type("button"),
                          h.AriaLabel("Brief"),
                          h.Title("Brief"),
                          h.Class(
                            "group/button inline-flex h-7 shrink-0 items-center justify-center gap-1 rounded-[min(var(--radius-md),12px)] border border-border bg-background px-2.5 text-[0.8rem] font-medium whitespace-nowrap transition-all outline-none select-none hover:bg-muted hover:text-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 active:translate-y-px [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-3.5",
                          ),
                          h.OnClick(
                            Message.GotPanelMessage({
                              message: PanelMessage.SelectedTab({ tab: "brief" }),
                            }),
                          ),
                        ],
                        [
                          fileTextIcon(h, "size-3.5"),
                          h.span([h.Class("hidden lg:inline")], ["Brief"]),
                        ],
                      ),
                      renderAutopilotMenu(
                        h,
                        level,
                        !activeWorkstream || model.autopilotBusy,
                        Option.isSome(model.openMenu) && model.openMenu.value === "autopilot",
                      ),
                    ],
                  ),
                ],
              ),
              model.autopilotError
                ? h.p(
                    [h.Class("border-b px-4 py-2 text-sm text-destructive")],
                    [model.autopilotError],
                  )
                : h.empty,
              activeWorkstream?.allTasksClosed && !activeWorkstream.evergreen
                ? h.div(
                    [h.Class("flex flex-wrap items-center gap-3 border-b px-4 py-2")],
                    [
                      h.span([], ["All tasks are closed."]),
                      renderButton(h, {
                        label: "Close Workstream",
                        size: "sm",
                        pending: model.closeBusy,
                        onClick: Message.CloseWorkstreamRequested(),
                      }),
                      model.closeError
                        ? h.span([h.Class("text-sm text-destructive")], [model.closeError])
                        : h.empty,
                    ],
                  )
                : h.empty,
              h.keyed("div")(
                `chat-${wsRoute.owner}-${wsRoute.name}-${wsRoute.number}`,
                [h.Id("chat-app-wrapper"), h.Class("flex min-h-0 min-w-0 grow flex-col")],
                [
                  h.submodel({
                    slotId: `chat-${wsRoute.owner}-${wsRoute.name}-${wsRoute.number}`,
                    model: model.chat.value,
                    view: chatSubmodelView,
                    viewInputs: { tasks: openTasks(model.panel) },
                    toParentMessage: (m) => Message.GotChatMessage({ message: m }),
                  }),
                ],
              ),
            ],
          ),
          h.aside(
            [h.Class("hidden w-80 shrink-0 flex-col border-l pt-2 md:flex")],
            [
              model.wide
                ? h.submodel({
                    slotId: `panel-side-${wsRoute.owner}-${wsRoute.name}-${wsRoute.number}`,
                    model: model.panel.value,
                    view: panelSubmodelView,
                    toParentMessage: (m) => Message.GotPanelMessage({ message: m }),
                  })
                : h.empty,
            ],
          ),
          !model.wide && model.mobileTasksOpen
            ? renderSheet(h, {
                mount: h.OnMount(inPlaceMount()),
                titleId: "tasks-sheet-title",
                title: "Tasks",
                hideTitle: true,
                close: Message.SetMobileTasksOpen({ open: false }),
                className: "pt-2 data-[side=bottom]:h-[80svh]",
                children: [
                  h.submodel({
                    slotId: `panel-sheet-${wsRoute.owner}-${wsRoute.name}-${wsRoute.number}`,
                    model: model.panel.value,
                    view: panelSheetSubmodelView,
                    toParentMessage: (m) => Message.GotPanelMessage({ message: m }),
                  }),
                ],
              })
            : h.empty,
          !model.wide && model.mobileBriefOpen
            ? renderSheet(h, {
                mount: h.OnMount(inPlaceMount()),
                titleId: "brief-sheet-title",
                title: "Brief",
                hideTitle: true,
                close: Message.SetMobileBriefOpen({ open: false }),
                className: "pt-2 data-[side=bottom]:h-[80svh]",
                children: [
                  h.submodel({
                    slotId: `brief-sheet-${wsRoute.owner}-${wsRoute.name}-${wsRoute.number}`,
                    model: model.panel.value,
                    view: briefSubmodelView,
                    toParentMessage: (m) => Message.GotPanelMessage({ message: m }),
                  }),
                ],
              })
            : h.empty,
        ],
      );
    },
    Workstreams: () => {
      if (Option.isNone(model.workstreamsProgram)) return h.empty;
      return h.submodel({
        slotId: "workstreams",
        model: model.workstreamsProgram.value,
        view: workstreamsSubmodelView,
        toParentMessage: (m) => Message.GotWorkstreamsMessage({ message: m }),
      });
    },
    Tasks: () => {
      if (Option.isNone(model.tasksProgram)) return h.empty;
      return h.submodel({
        slotId: "tasks",
        model: model.tasksProgram.value,
        view: tasksSubmodelView,
        toParentMessage: (m) => Message.GotTasksMessage({ message: m }),
      });
    },
    Inbox: () => {
      if (Option.isNone(model.inboxProgram)) return h.empty;
      return h.div(
        [h.Class("contents")],
        [
          renderPageHeader(h, "Inbox"),
          h.submodel({
            slotId: "inbox",
            model: model.inboxProgram.value,
            view: inboxSubmodelView,
            toParentMessage: (m) => Message.GotInboxMessage({ message: m }),
          }),
        ],
      );
    },
    Agents: () => {
      if (Option.isNone(model.agentsProgram)) return h.empty;
      return h.submodel({
        slotId: "agents",
        model: model.agentsProgram.value,
        view: agentsSubmodelView,
        toParentMessage: (m) => Message.GotAgentsMessage({ message: m }),
      });
    },
    Usage: () => {
      if (Option.isNone(model.usageProgram)) return h.empty;
      return h.submodel({
        slotId: "usage",
        model: model.usageProgram.value,
        view: usageSubmodelView,
        toParentMessage: (m) => Message.GotUsageMessage({ message: m }),
      });
    },
    Settings: () => {
      if (Option.isNone(model.settingsProgram)) return h.empty;
      return h.submodel({
        slotId: "settings-overview",
        model: model.settingsProgram.value,
        view: settingsOverviewSubmodelView,
        toParentMessage: (m) => Message.GotSettingsMessage({ message: m }),
      });
    },
    Roles: () => {
      if (Option.isNone(model.rolesProgram)) return h.empty;
      return h.submodel({
        slotId: "settings-roles",
        model: model.rolesProgram.value,
        view: rolesSubmodelView,
        toParentMessage: (m) => Message.GotRolesMessage({ message: m }),
      });
    },
    Memory: renderMemory,
    MemoryRepositories: renderMemory,
    MemoryFile: renderMemory,
    SwitchSettings: () => {
      if (Option.isNone(model.switchesProgram)) return h.empty;
      return h.submodel({
        slotId: "settings-switches",
        model: model.switchesProgram.value,
        view: switchesSubmodelView,
        toParentMessage: (m) => Message.GotSwitchesMessage({ message: m }),
      });
    },
    Voice: () => {
      if (Option.isNone(model.voiceProgram)) return h.empty;
      return h.submodel({
        slotId: "settings-voice",
        model: model.voiceProgram.value,
        view: voiceSubmodelView,
        toParentMessage: (m) => Message.GotVoiceMessage({ message: m }),
      });
    },
    Devices: () => {
      if (Option.isNone(model.devicesProgram)) return h.empty;
      return h.submodel({
        slotId: "settings-devices",
        model: model.devicesProgram.value,
        view: devicesSubmodelView,
        toParentMessage: (m) => Message.GotDevicesMessage({ message: m }),
      });
    },
    GitHub: () => {
      if (Option.isNone(model.githubProgram)) return h.empty;
      return h.submodel({
        slotId: "settings-github",
        model: model.githubProgram.value,
        view: githubSubmodelView,
        toParentMessage: (m) => Message.GotGitHubMessage({ message: m }),
      });
    },
    McpServers: () => {
      if (Option.isNone(model.mcpServersProgram)) return h.empty;
      return h.submodel({
        slotId: "settings-mcp-servers",
        model: model.mcpServersProgram.value,
        view: mcpServersSubmodelView,
        toParentMessage: (m) => Message.GotMcpServersMessage({ message: m }),
      });
    },
    Checkup: renderCheckup,
    CheckupTools: renderCheckup,
    CheckupPermissions: renderCheckup,
    CheckupLabels: renderCheckup,
    CheckupWorkstreams: renderCheckup,
  });
}

export function view(model: Model, h: HtmlBuilder<Message>): Document {
  if (model.loginShown) {
    return {
      title: "Mobius",
      body: renderLogin(h, model),
    };
  }

  if (Option.isSome(model.appError)) {
    return {
      title: "Mobius",
      body: h.main([h.Class("p-6")], [renderBadge(h, "destructive", model.appError.value)]),
    };
  }

  if (Option.isNone(model.apps)) {
    return {
      title: "Mobius",
      body: h.empty,
    };
  }

  if (model.apps.value.length === 0) {
    if (Option.isNone(model.githubProgram)) {
      return {
        title: "Mobius",
        body: h.empty,
      };
    }
    return {
      title: "Mobius",
      body: h.main(
        [h.Class("flex min-h-svh items-center justify-center p-6")],
        [
          h.div(
            [h.Id("github-app-wrapper"), h.Class("w-full max-w-lg")],
            [
              h.submodel({
                slotId: "settings-github-setup",
                model: model.githubProgram.value,
                view: githubSubmodelView,
                toParentMessage: (m) => Message.GotGitHubMessage({ message: m }),
              }),
            ],
          ),
        ],
      ),
    };
  }

  return {
    title: "Mobius",
    body: renderFrame(h, model, renderPage(h, model)),
  };
}
