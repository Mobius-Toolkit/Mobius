import { Option } from "effect";
import { Command } from "foldkit";
import type { Command as Cmd } from "foldkit/command";
import * as Update from "foldkit/update";
import { toString as urlToString, type Url } from "foldkit/url";
import { update as updateAgents } from "@/agents/update";
import { FetchPage, ListQueued } from "@/chat/commands";
import { initialModel as initialChatModel } from "@/chat/model";
import { update as updateChat } from "@/chat/update";
import { FetchCheckup, FetchTools } from "@/checkup/commands";
import { initialModel as initialCheckupModel } from "@/checkup/model";
import { update as updateCheckup } from "@/checkup/update";
import { initialModel as initialPanelModel } from "@/panel/model";
import { commandsFor as panelCommandsFor, update as updatePanel } from "@/panel/update";
import { FetchDevices } from "@/settings/devices/commands";
import { initialModel as initialDevicesModel } from "@/settings/devices/model";
import { update as updateDevices } from "@/settings/devices/update";
import { initialModel as initialGitHubModel } from "@/settings/github/model";
import { update as updateGitHub } from "@/settings/github/update";
import { FetchServers } from "@/settings/mcp-servers/commands";
import { initialModel as initialMcpServersModel } from "@/settings/mcp-servers/model";
import { update as updateMcpServers } from "@/settings/mcp-servers/update";
import { FetchFiles, FetchVersions } from "@/settings/memory/commands";
import { initialModel as initialMemoryModel } from "@/settings/memory/model";
import { update as updateMemory } from "@/settings/memory/update";
import { FetchVersion, FetchVoice as FetchOverviewVoice } from "@/settings/overview/commands";
import { initialModel as initialOverviewModel } from "@/settings/overview/model";
import { update as updateOverview } from "@/settings/overview/update";
import { FetchHarnesses, FetchRoles } from "@/settings/roles/commands";
import { initialModel as initialRolesModel } from "@/settings/roles/model";
import { update as updateRoles } from "@/settings/roles/update";
import { update as updateSwitches } from "@/settings/switches/update";
import { FetchVoice } from "@/settings/voice/commands";
import { initialModel as initialVoiceModel } from "@/settings/voice/model";
import { update as updateVoice } from "@/settings/voice/update";
import { FetchBoard } from "@/tasks/commands";
import { initialModel as initialTasksModel } from "@/tasks/model";
import { update as updateTasks } from "@/tasks/update";
import { initialModel as initialUsageModel } from "@/usage/model";
import { load as usageLoad, update as updateUsage } from "@/usage/update";
import { initialModel as initialWorkstreamsModel } from "@/workstreams/model";
import { update as updateWorkstreams } from "@/workstreams/update";
import {
  CancelUpgrade,
  CheckUpgrade,
  CloseWorkstream,
  FetchCIStops,
  FetchPauses,
  FetchSwitches,
  GoBack,
  LoadApp,
  LoadExternal,
  LoadReleaseChanges,
  Navigate,
  ReplaceUrl,
  ResumePause,
  SelectAutopilot,
  StartUpgrade,
  SubmitLogin,
  ToggleSwitch,
} from "./commands";
import { Message } from "./message";
import { initialModel, Model, savedOrganization, saveOrganization } from "./model";
import { parseRoute, type AppRoute } from "./route";

type UpdateReturn = Update.Return<Model, Message>;

const foldChat = Update.foldChild({
  update: updateChat,
  read: (model: Model) => model.chat,
  write: (model, chat) => ({ ...model, chat: Option.some(chat) }),
  toParentMessage: (message) => Message.GotChatMessage({ message }),
});

const foldPanel = Update.foldChild({
  update: updatePanel,
  read: (model: Model) => model.panel,
  write: (model, panel) => ({ ...model, panel: Option.some(panel) }),
  toParentMessage: (message) => Message.GotPanelMessage({ message }),
});

const foldInbox = Update.foldChild({
  update: updateChat,
  read: (model: Model) => model.inboxProgram,
  write: (model, inboxProgram) => ({ ...model, inboxProgram: Option.some(inboxProgram) }),
  toParentMessage: (message) => Message.GotInboxMessage({ message }),
});

const foldWorkstreams = Update.foldChild({
  update: updateWorkstreams,
  read: (model: Model) => model.workstreamsProgram,
  write: (model, workstreamsProgram) => ({
    ...model,
    workstreamsProgram: Option.some(workstreamsProgram),
  }),
  toParentMessage: (message) => Message.GotWorkstreamsMessage({ message }),
});

const foldTasks = Update.foldChild({
  update: updateTasks,
  read: (model: Model) => model.tasksProgram,
  write: (model, tasksProgram) => ({ ...model, tasksProgram: Option.some(tasksProgram) }),
  toParentMessage: (message) => Message.GotTasksMessage({ message }),
});

const foldAgents = Update.foldChild({
  update: updateAgents,
  read: (model: Model) => model.agentsProgram,
  write: (model, agentsProgram) => ({ ...model, agentsProgram: Option.some(agentsProgram) }),
  toParentMessage: (message) => Message.GotAgentsMessage({ message }),
});

const foldCheckup = Update.foldChild({
  update: updateCheckup,
  read: (model: Model) => model.checkupProgram,
  write: (model, checkupProgram) => ({ ...model, checkupProgram: Option.some(checkupProgram) }),
  toParentMessage: (message) => Message.GotCheckupMessage({ message }),
});

const foldUsage = Update.foldChild({
  update: updateUsage,
  read: (model: Model) => model.usageProgram,
  write: (model, usageProgram) => ({ ...model, usageProgram: Option.some(usageProgram) }),
  toParentMessage: (message) => Message.GotUsageMessage({ message }),
});

const foldSettings = Update.foldChild({
  update: updateOverview,
  read: (model: Model) => model.settingsProgram,
  write: (model, settingsProgram) => ({ ...model, settingsProgram: Option.some(settingsProgram) }),
  toParentMessage: (message) => Message.GotSettingsMessage({ message }),
});

const foldRoles = Update.foldChild({
  update: updateRoles,
  read: (model: Model) => model.rolesProgram,
  write: (model, rolesProgram) => ({ ...model, rolesProgram: Option.some(rolesProgram) }),
  toParentMessage: (message) => Message.GotRolesMessage({ message }),
});

const foldMemory = Update.foldChild({
  update: updateMemory,
  read: (model: Model) => model.memoryProgram,
  write: (model, memoryProgram) => ({ ...model, memoryProgram: Option.some(memoryProgram) }),
  toParentMessage: (message) => Message.GotMemoryMessage({ message }),
});

const foldMcpServers = Update.foldChild({
  update: updateMcpServers,
  read: (model: Model) => model.mcpServersProgram,
  write: (model, mcpServersProgram) => ({
    ...model,
    mcpServersProgram: Option.some(mcpServersProgram),
  }),
  toParentMessage: (message) => Message.GotMcpServersMessage({ message }),
});

const foldSwitches = Update.foldChild({
  update: updateSwitches,
  read: (model: Model) => model.switchesProgram,
  write: (model, switchesProgram) => ({ ...model, switchesProgram: Option.some(switchesProgram) }),
  toParentMessage: (message) => Message.GotSwitchesMessage({ message }),
});

const foldVoice = Update.foldChild({
  update: updateVoice,
  read: (model: Model) => model.voiceProgram,
  write: (model, voiceProgram) => ({ ...model, voiceProgram: Option.some(voiceProgram) }),
  toParentMessage: (message) => Message.GotVoiceMessage({ message }),
});

const foldDevices = Update.foldChild({
  update: updateDevices,
  read: (model: Model) => model.devicesProgram,
  write: (model, devicesProgram) => ({ ...model, devicesProgram: Option.some(devicesProgram) }),
  toParentMessage: (message) => Message.GotDevicesMessage({ message }),
});

const foldGitHub = Update.foldChild({
  update: updateGitHub,
  read: (model: Model) => model.githubProgram,
  write: (model, githubProgram) => ({ ...model, githubProgram: Option.some(githubProgram) }),
  toParentMessage: (message) => Message.GotGitHubMessage({ message }),
});

function initScreenForRoute(
  model: Model,
  route: AppRoute,
): { model: Model; commands: Cmd<Message>[] } {
  const commands: Cmd<Message>[] = [];

  switch (route._tag) {
    case "CoordinatorChat": {
      const organization = model.organization;
      const chatModel = initialChatModel({
        organization,
        repository: "",
        workstream: 0,
        agent: "Coordinator",
      });
      const trayItems = model.inbox.filter((item) => item.organization === organization);
      model = {
        ...model,
        chat: Option.some({ ...chatModel, trayItems }),
        panel: Option.none(),
      };
      commands.push(
        Command.mapMessage(
          FetchPage({
            organization,
            repository: "",
            workstream: 0,
            isInitial: true,
            known: null,
            sent: null,
          }),
          (message) => Message.GotChatMessage({ message }),
        ),
        Command.mapMessage(ListQueued({ organization, repository: "", workstream: 0 }), (message) =>
          Message.GotChatMessage({ message }),
        ),
      );
      break;
    }

    case "WorkstreamChat": {
      const { owner, name, number } = route;
      const repository = `${owner}/${name}`;
      const chatModel = initialChatModel({
        organization: owner,
        repository,
        workstream: number,
        agent: "Lead",
      });
      const panelModel = initialPanelModel({
        owner,
        name,
        number,
        instance: "side",
        tab: "tasks",
      });
      const inWorkstream = model.inbox
        .filter((item) => item.workstreamRepository === repository && item.workstream === number)
        .toSorted((a, b) => a.id - b.id);
      const workstream = model.workstreams.find(
        (other) => other.repository === repository && other.number === number,
      );

      model = {
        ...model,
        chat: Option.some({
          ...chatModel,
          brief: workstream ?? null,
          trayItems: inWorkstream,
        }),
        panel: Option.some(panelModel),
      };
      commands.push(
        Command.mapMessage(
          FetchPage({
            organization: owner,
            repository,
            workstream: number,
            isInitial: true,
            known: null,
            sent: null,
          }),
          (message) => Message.GotChatMessage({ message }),
        ),
        Command.mapMessage(
          ListQueued({ organization: owner, repository, workstream: number }),
          (message) => Message.GotChatMessage({ message }),
        ),
        ...Command.mapMessages(panelCommandsFor(panelModel), (message) =>
          Message.GotPanelMessage({ message }),
        ),
      );
      break;
    }

    case "Inbox": {
      const inboxModel = initialChatModel({
        organization: model.organization,
        repository: "",
        workstream: 0,
        agent: "Coordinator",
      });
      const trayItems = model.inbox.filter((item) => item.organization === model.organization);
      model = {
        ...model,
        inboxProgram: Option.some({ ...inboxModel, trayItems }),
      };
      break;
    }

    case "Workstreams": {
      model = {
        ...model,
        workstreamsProgram: Option.some({
          ...initialWorkstreamsModel({ organization: model.organization }),
          workstreams:
            model.workstreams.length > 0 ? Option.some(model.workstreams) : Option.none(),
          needsHuman: model.needsHuman,
          working: model.workstreamsWorking,
          unread: model.unread,
        }),
      };
      break;
    }

    case "Tasks": {
      const tasksModel = initialTasksModel({ organization: model.organization });
      model = {
        ...model,
        tasksProgram: Option.some(tasksModel),
      };
      commands.push(
        Command.mapMessage(FetchBoard(), (message) => Message.GotTasksMessage({ message })),
      );
      break;
    }

    case "Agents": {
      model = {
        ...model,
        agentsProgram: Option.some({
          agents: Option.none(),
          transcript: Option.none(),
          request: 0,
          error: "",
        }),
      };
      break;
    }

    case "Usage": {
      const usageModel = initialUsageModel({
        search: route.search,
        now: Date.now(),
        timeZone: new Intl.DateTimeFormat().resolvedOptions().timeZone,
      });
      model = {
        ...model,
        usageProgram: Option.some(usageModel),
      };
      commands.push(
        Command.mapMessage(usageLoad(usageModel), (message) =>
          Message.GotUsageMessage({ message }),
        ),
      );
      break;
    }

    case "Settings": {
      model = {
        ...model,
        settingsProgram: Option.some(initialOverviewModel),
      };
      commands.push(
        Command.mapMessage(FetchVersion(), (message) => Message.GotSettingsMessage({ message })),
        Command.mapMessage(FetchOverviewVoice(), (message) =>
          Message.GotSettingsMessage({ message }),
        ),
      );
      break;
    }

    case "Checkup":
    case "CheckupTools": {
      const checkupModel = initialCheckupModel({ page: "tools", organization: model.organization });
      model = {
        ...model,
        checkupProgram: Option.some(checkupModel),
      };
      commands.push(
        Command.mapMessage(FetchTools(), (message) => Message.GotCheckupMessage({ message })),
      );
      break;
    }

    case "CheckupPermissions":
    case "CheckupLabels":
    case "CheckupWorkstreams": {
      const page =
        route._tag === "CheckupPermissions"
          ? ("permissions" as const)
          : route._tag === "CheckupLabels"
            ? ("labels" as const)
            : ("workstreams" as const);
      const checkupModel = initialCheckupModel({ page, organization: route.organization });
      model = {
        ...model,
        checkupProgram: Option.some(checkupModel),
      };
      commands.push(
        Command.mapMessage(FetchCheckup({ organization: route.organization }), (message) =>
          Message.GotCheckupMessage({ message }),
        ),
      );
      break;
    }

    case "Roles": {
      model = {
        ...model,
        rolesProgram: Option.some(initialRolesModel),
      };
      commands.push(
        Command.mapMessage(FetchRoles(), (message) => Message.GotRolesMessage({ message })),
        Command.mapMessage(FetchHarnesses(), (message) => Message.GotRolesMessage({ message })),
      );
      break;
    }

    case "MemoryRepositories": {
      model = {
        ...model,
        memoryProgram: Option.some(
          initialMemoryModel({ page: "repositories", owner: "", name: "", file: "" }),
        ),
      };
      break;
    }

    case "Memory": {
      model = {
        ...model,
        memoryProgram: Option.some(
          initialMemoryModel({ page: "files", owner: route.owner, name: route.name, file: "" }),
        ),
      };
      commands.push(
        Command.mapMessage(FetchFiles({ owner: route.owner, name: route.name }), (message) =>
          Message.GotMemoryMessage({ message }),
        ),
      );
      break;
    }

    case "MemoryFile": {
      model = {
        ...model,
        memoryProgram: Option.some(
          initialMemoryModel({
            page: "file",
            owner: route.owner,
            name: route.name,
            file: route.file,
          }),
        ),
      };
      commands.push(
        Command.mapMessage(
          FetchVersions({ owner: route.owner, name: route.name, file: route.file }),
          (message) => Message.GotMemoryMessage({ message }),
        ),
      );
      break;
    }

    case "McpServers": {
      const callback = {
        oauth: route.search.oauth ?? ("" as const),
        server: route.search.server ?? 0,
        reason: route.search.reason ?? "",
      };
      model = {
        ...model,
        mcpServersProgram: Option.some(
          initialMcpServersModel({ organization: model.organization, callback }),
        ),
      };
      commands.push(
        Command.mapMessage(FetchServers({ organization: model.organization }), (message) =>
          Message.GotMcpServersMessage({ message }),
        ),
      );
      break;
    }

    case "SwitchSettings": {
      model = {
        ...model,
        switchesProgram: Option.some({
          switches: model.switches,
          errors: {
            start: model.switchErrors.start ?? "",
            merge: model.switchErrors.merge ?? "",
            repairs: model.switchErrors.repairs ?? "",
          },
        }),
      };
      break;
    }

    case "Voice": {
      model = {
        ...model,
        voiceProgram: Option.some(initialVoiceModel),
      };
      commands.push(
        Command.mapMessage(FetchVoice(), (message) => Message.GotVoiceMessage({ message })),
      );
      break;
    }

    case "Devices": {
      model = {
        ...model,
        devicesProgram: Option.some(initialDevicesModel),
      };
      commands.push(
        Command.mapMessage(FetchDevices(), (message) => Message.GotDevicesMessage({ message })),
      );
      break;
    }

    case "GitHub": {
      const apps = model.apps._tag === "Some" ? model.apps.value : [];
      const setup = !(apps.length > 0);
      model = {
        ...model,
        githubProgram: Option.some({
          ...initialGitHubModel({ setup }),
          apps,
        }),
      };
      break;
    }

    default:
      break;
  }

  return { model, commands };
}

export function init(url: Url): UpdateReturn {
  const route = parseRoute(url);
  if (route._tag === "WorkstreamChat") {
    saveOrganization(route.owner);
  }
  const model = initialModel(route);
  const commands: Cmd<Message>[] = [
    LoadApp(),
    CheckUpgrade(),
    FetchPauses(),
    FetchCIStops(),
    FetchSwitches(),
  ];

  if (route._tag === "NotFound") {
    commands.push(ReplaceUrl({ path: "/chat" }));
  } else {
    const screenInit = initScreenForRoute(model, route);
    commands.push(...screenInit.commands);
    return { model: screenInit.model, commands };
  }

  return { model, commands };
}

export function update(model: Model, message: Message): UpdateReturn {
  switch (message._tag) {
    case "UrlRequested": {
      if (message.request._tag === "Internal") {
        return {
          model,
          commands: [Navigate({ path: urlToString(message.request.url) })],
        };
      }
      return {
        model,
        commands: [LoadExternal({ href: message.request.href })],
      };
    }

    case "UrlChanged": {
      const nextRoute = parseRoute(message.url);
      if (nextRoute._tag === "NotFound") {
        return {
          model: { ...model, route: nextRoute },
          commands: [ReplaceUrl({ path: "/chat" })],
        };
      }

      let org = model.organization;
      if (nextRoute._tag === "WorkstreamChat") {
        org = nextRoute.owner;
        saveOrganization(org);
      }

      const nextDepth = model.historyDepth + 1;
      const screenInit = initScreenForRoute(
        { ...model, route: nextRoute, historyDepth: nextDepth, organization: org },
        nextRoute,
      );

      return {
        model: screenInit.model,
        commands: screenInit.commands,
      };
    }

    case "NavigateRequested":
      return { model, commands: [Navigate({ path: message.path })] };

    case "BackRequested":
      return {
        model,
        commands: [model.historyDepth > 0 ? GoBack() : ReplaceUrl({ path: message.parent })],
      };

    case "CompletedNavigate":
    case "CompletedReplaceUrl":
    case "CompletedGoBack":
    case "CompletedLoadExternal":
    case "CompletedFocusSheet":
      return { model, commands: [] };

    case "ShowLogin":
      return { model: { ...model, loginShown: true }, commands: [] };

    case "HideLogin":
      return { model: { ...model, loginShown: false }, commands: [] };

    case "PasswordInputChanged":
      return { model: { ...model, passwordInput: message.password }, commands: [] };

    case "SubmitLoginRequested":
      return {
        model: { ...model, loginBusy: true, loginError: "" },
        commands: [SubmitLogin({ password: model.passwordInput })],
      };

    case "LoginSucceeded":
      return {
        model: { ...model, loginShown: false, loginBusy: false, passwordInput: "" },
        commands: [LoadApp(), CheckUpgrade(), FetchPauses(), FetchCIStops(), FetchSwitches()],
      };

    case "LoginFailed":
      return {
        model: { ...model, loginBusy: false, loginError: message.error },
        commands: [],
      };

    case "LoadAppRequested":
      return { model, commands: [LoadApp()] };

    case "AppsAndOrganizationsLoaded": {
      const { apps, organizations } = message;
      let org = model.organization;
      if (model.route._tag === "WorkstreamChat" && organizations.includes(model.route.owner)) {
        saveOrganization(model.route.owner);
        org = model.route.owner;
      } else {
        const saved = savedOrganization();
        org = organizations.includes(org)
          ? org
          : organizations.includes(saved)
            ? saved
            : (organizations[0] ?? "");
      }

      let githubProgram = model.githubProgram;
      if (Option.isSome(githubProgram)) {
        const setup = !(apps.length > 0);
        githubProgram = Option.some({
          ...githubProgram.value,
          apps,
          setup,
        });
      }

      let workstreamsProgram = model.workstreamsProgram;
      if (Option.isSome(workstreamsProgram)) {
        workstreamsProgram = Option.some({
          ...workstreamsProgram.value,
          organization: org,
        });
      }

      let tasksProgram = model.tasksProgram;
      if (Option.isSome(tasksProgram)) {
        tasksProgram = Option.some({
          ...tasksProgram.value,
          organization: org,
        });
      }

      const updatedModel: Model = {
        ...model,
        apps: Option.some(apps),
        organizations,
        organization: org,
        githubProgram,
        workstreamsProgram,
        tasksProgram,
        appError: Option.none(),
      };

      return { model: updatedModel, commands: [] };
    }

    case "LoadAppFailed":
      return {
        model: { ...model, appError: Option.some(message.error) },
        commands: [],
      };

    case "SelectOrganization": {
      const org = message.organization;
      saveOrganization(org);
      const commands: Cmd<Message>[] = [];
      if (model.route._tag === "WorkstreamChat" && model.route.owner !== org) {
        commands.push(Navigate({ path: "/workstreams" }));
      }
      let workstreamsProgram = model.workstreamsProgram;
      if (Option.isSome(workstreamsProgram)) {
        workstreamsProgram = Option.some({
          ...workstreamsProgram.value,
          organization: org,
        });
      }
      let tasksProgram = model.tasksProgram;
      if (Option.isSome(tasksProgram)) {
        tasksProgram = Option.some({
          ...tasksProgram.value,
          organization: org,
        });
      }
      return {
        model: { ...model, organization: org, workstreamsProgram, tasksProgram },
        commands,
      };
    }

    case "ConnectionChanged":
      return { model: { ...model, link: message.link }, commands: [] };

    case "OnlineChanged":
      return { model: { ...model, online: message.online }, commands: [] };

    case "LiveOpened":
    case "LiveConnected":
      return {
        model: { ...model, link: "connected" },
        commands: [CheckUpgrade(), FetchPauses(), FetchCIStops(), FetchSwitches()],
      };

    case "LiveConnecting":
      return { model: { ...model, link: "connecting" }, commands: [] };

    case "LiveLoginRequired":
      return { model: { ...model, loginShown: true }, commands: [] };

    case "RepositoriesChanged":
      return { model, commands: [LoadApp()] };

    case "UnreadStoreChanged": {
      let workstreamsProgram = model.workstreamsProgram;
      if (Option.isSome(workstreamsProgram)) {
        workstreamsProgram = Option.some({
          ...workstreamsProgram.value,
          unread: message.unread,
        });
      }
      return { model: { ...model, unread: message.unread, workstreamsProgram }, commands: [] };
    }

    case "WorkstreamsStoreChanged": {
      const { workstreams, needsHuman, working } = message;
      const commands: Cmd<Message>[] = [];
      if (model.route._tag === "WorkstreamChat") {
        const key = `${model.route.owner}/${model.route.name}`;
        const num = model.route.number;
        const exists = workstreams.some((w) => w.repository === key && w.number === num);
        if (workstreams.length > 0 && !exists) {
          commands.push(ReplaceUrl({ path: "/workstreams" }));
        }
      }
      let workstreamsProgram = model.workstreamsProgram;
      if (Option.isSome(workstreamsProgram)) {
        workstreamsProgram = Option.some({
          ...workstreamsProgram.value,
          workstreams: Option.some(workstreams),
          needsHuman,
          working,
        });
      }
      let chat = model.chat;
      if (Option.isSome(chat) && model.route._tag === "WorkstreamChat") {
        const repo = `${model.route.owner}/${model.route.name}`;
        const num = model.route.number;
        const workstream = workstreams.find(
          (other) => other.repository === repo && other.number === num,
        );
        if (workstream) {
          chat = Option.some({ ...chat.value, brief: workstream });
        }
      }
      return {
        model: {
          ...model,
          workstreams,
          needsHuman,
          workstreamsWorking: working,
          workstreamsProgram,
          chat,
        },
        commands,
      };
    }

    case "InboxStoreChanged": {
      const { items, answered } = message;
      let chat = model.chat;
      if (Option.isSome(chat)) {
        const activeOrg = chat.value.organization;
        const activeRepo = chat.value.repository;
        const activeWorkstream = chat.value.workstream;
        const trayItems = activeRepo
          ? items.filter(
              (it) => it.workstreamRepository === activeRepo && it.workstream === activeWorkstream,
            )
          : items.filter((it) => it.organization === activeOrg);
        chat = Option.some({ ...chat.value, trayItems });
      }

      let inboxProgram = model.inboxProgram;
      if (Option.isSome(inboxProgram)) {
        const trayItems = items.filter((it) => it.organization === model.organization);
        inboxProgram = Option.some({ ...inboxProgram.value, trayItems });
      }

      return {
        model: { ...model, inbox: items, inboxAnswered: answered, chat, inboxProgram },
        commands: [],
      };
    }

    case "NoticeStoreChanged":
      return { model: { ...model, notice: message.notice }, commands: [] };

    case "NewBuildStoreChanged":
      return { model: { ...model, newBuild: message.newBuild }, commands: [] };

    case "CIStopsChanged":
      return { model: { ...model, ciStops: message.stops }, commands: [] };

    case "PausesChanged":
      return { model: { ...model, pauses: message.pauses }, commands: [] };

    case "ResumePauseRequested":
      return {
        model: { ...model, pauseBusy: Option.some(message.harness) },
        commands: [ResumePause({ harness: message.harness })],
      };

    case "ResumePauseSucceeded":
      return {
        model: { ...model, pauseBusy: Option.none(), pauseError: Option.none() },
        commands: [],
      };

    case "ResumePauseFailed":
      return {
        model: {
          ...model,
          pauseBusy: Option.none(),
          pauseError: Option.some(message.error),
        },
        commands: [],
      };

    case "SwitchesChanged": {
      let switchesProgram = model.switchesProgram;
      if (Option.isSome(switchesProgram)) {
        switchesProgram = Option.some({
          ...switchesProgram.value,
          switches: Option.some(message.switches),
        });
      }
      return {
        model: {
          ...model,
          switches: Option.some(message.switches),
          switchesProgram,
        },
        commands: [],
      };
    }

    case "ToggleSwitchRequested": {
      const current = Option.getOrUndefined(model.switches);
      if (current) {
        const optimistic = { ...current, [message.kind]: message.on };
        let switchesProgram = model.switchesProgram;
        if (Option.isSome(switchesProgram)) {
          switchesProgram = Option.some({
            ...switchesProgram.value,
            switches: Option.some(optimistic),
          });
        }
        return {
          model: {
            ...model,
            switches: Option.some(optimistic),
            switchBusy: Option.some(message.kind),
            switchesProgram,
          },
          commands: [ToggleSwitch({ kind: message.kind, on: message.on })],
        };
      }
      return { model, commands: [] };
    }

    case "ToggleSwitchSucceeded": {
      const errors = { ...model.switchErrors };
      delete errors[message.kind];
      let switchesProgram = model.switchesProgram;
      if (Option.isSome(switchesProgram)) {
        switchesProgram = Option.some({
          ...switchesProgram.value,
          errors: {
            start: errors.start ?? "",
            merge: errors.merge ?? "",
            repairs: errors.repairs ?? "",
          },
        });
      }
      return {
        model: {
          ...model,
          switchBusy: Option.none(),
          switchErrors: errors,
          switchesProgram,
        },
        commands: [],
      };
    }

    case "ToggleSwitchFailed": {
      const current = Option.getOrUndefined(model.switches);
      const reverted = current
        ? {
            ...current,
            [message.kind]: !current[message.kind as "start" | "merge" | "repairs"],
          }
        : current;
      const nextErrors = {
        ...model.switchErrors,
        [message.kind]: message.error,
      };
      let switchesProgram = model.switchesProgram;
      if (Option.isSome(switchesProgram)) {
        switchesProgram = Option.some({
          ...switchesProgram.value,
          switches: Option.fromNullishOr(reverted),
          errors: {
            start: nextErrors.start ?? "",
            merge: nextErrors.merge ?? "",
            repairs: nextErrors.repairs ?? "",
          },
        });
      }
      return {
        model: {
          ...model,
          switches: Option.fromNullishOr(reverted),
          switchBusy: Option.none(),
          switchErrors: nextErrors,
          switchesProgram,
        },
        commands: [],
      };
    }

    case "OpenHeldSheet":
      return {
        model: { ...model, heldSheet: Option.some({ title: message.title, held: message.held }) },
        commands: [],
      };

    case "CloseHeldSheet":
      return { model: { ...model, heldSheet: Option.none() }, commands: [] };

    case "DrainChanged":
      return {
        model: {
          ...model,
          upgrade: {
            ...model.upgrade,
            drain: Option.some(message.drain),
            cancelling: !message.drain.on ? false : model.upgrade.cancelling,
          },
        },
        commands: [],
      };

    case "UpgradeFailureChanged":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, failure: message.failure },
        },
        commands: [],
      };

    case "CheckUpgradeRequested":
      return { model, commands: [CheckUpgrade()] };

    case "ReleaseVersionChecked":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, version: message.version },
        },
        commands: [],
      };

    case "UpgradeInfoLoaded": {
      const { version, drain, failure } = message;
      return {
        model: {
          ...model,
          upgrade: {
            ...model.upgrade,
            version: version || model.upgrade.version,
            drain: drain ? Option.some(drain) : model.upgrade.drain,
            failure: failure || model.upgrade.failure,
            cancelling: drain && !drain.on ? false : model.upgrade.cancelling,
          },
        },
        commands: [],
      };
    }

    case "ShowUpgradeChanges":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, changesShown: true },
        },
        commands: [LoadReleaseChanges(), CheckUpgrade()],
      };

    case "HideUpgradeChanges":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, changesShown: false },
        },
        commands: [],
      };

    case "ReleaseChangesLoaded":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, changes: Option.some(message.changes) },
        },
        commands: [],
      };

    case "ReleaseChangesFailed":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, changesError: message.error },
        },
        commands: [],
      };

    case "StartUpgradeRequested":
      return {
        model: {
          ...model,
          upgrade: {
            ...model.upgrade,
            changesShown: false,
            upgrading: true,
            failure: "",
          },
        },
        commands: [StartUpgrade()],
      };

    case "StartUpgradeSucceeded":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, upgrading: false },
        },
        commands: [],
      };

    case "StartUpgradeFailed":
      return {
        model: {
          ...model,
          upgrade: {
            ...model.upgrade,
            upgrading: false,
            failure: message.error,
          },
        },
        commands: [],
      };

    case "CancelUpgradeRequested":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, cancelling: true },
        },
        commands: [CancelUpgrade()],
      };

    case "CancelUpgradeSucceeded":
      return {
        model: {
          ...model,
          upgrade: { ...model.upgrade, cancelling: false },
        },
        commands: [],
      };

    case "CancelUpgradeFailed":
      return {
        model: {
          ...model,
          upgrade: {
            ...model.upgrade,
            cancelling: false,
            failure: message.error,
          },
        },
        commands: [],
      };

    case "SelectAutopilotRequested": {
      if (model.route._tag === "WorkstreamChat") {
        const { owner, name, number } = model.route;
        const workstream = model.workstreams.find(
          (w) => w.repository === `${owner}/${name}` && w.number === number,
        );
        const from = workstream?.autopilot ?? "off";
        return {
          model: { ...model, autopilotBusy: true, autopilotError: "" },
          commands: [SelectAutopilot({ owner, name, number, from, to: message.level })],
        };
      }
      return { model, commands: [] };
    }

    case "SelectAutopilotSucceeded":
      return {
        model: {
          ...model,
          autopilotBusy: false,
          chosenAutopilot: Option.some({ from: message.from, to: message.to }),
        },
        commands: [],
      };

    case "SelectAutopilotFailed":
      return {
        model: { ...model, autopilotBusy: false, autopilotError: message.error },
        commands: [],
      };

    case "CloseWorkstreamRequested": {
      if (model.route._tag === "WorkstreamChat") {
        const { owner, name, number } = model.route;
        return {
          model: { ...model, closeBusy: true, closeError: "" },
          commands: [CloseWorkstream({ owner, name, number })],
        };
      }
      return { model, commands: [] };
    }

    case "CloseWorkstreamSucceeded":
      return {
        model: { ...model, closeBusy: false },
        commands: [],
      };

    case "CloseWorkstreamFailed":
      return {
        model: { ...model, closeBusy: false, closeError: message.error },
        commands: [],
      };

    case "SetMobileTasksOpen":
      return { model: { ...model, mobileTasksOpen: message.open }, commands: [] };

    case "ToggleMenu": {
      const isCurrentlyOpen = Option.isSome(model.openMenu) && model.openMenu.value === message.id;
      return {
        model: {
          ...model,
          openMenu: isCurrentlyOpen ? Option.none() : Option.some(message.id),
        },
        commands: [],
      };
    }

    case "CloseMenu":
      return { model: { ...model, openMenu: Option.none() }, commands: [] };

    // Submodel event forwarders
    case "ForwardWorkstreamsEvent": {
      let m = model;
      const cmds: Cmd<Message>[] = [];
      if (Option.isSome(m.tasksProgram)) {
        const res = foldTasks(m, { _tag: "BoardChanged" });
        m = res.model;
        if (res.commands) cmds.push(...res.commands);
      }
      if (Option.isSome(m.panel)) {
        const res = foldPanel(m, { _tag: "WorkstreamsChanged" });
        m = res.model;
        if (res.commands) cmds.push(...res.commands);
      }
      return { model: m, commands: cmds };
    }

    case "ForwardAgentEvent": {
      let m = model;
      const cmds: Cmd<Message>[] = [];
      if (Option.isSome(m.agentsProgram)) {
        const res = foldAgents(m, { _tag: "AgentChanged" });
        m = res.model;
        if (res.commands) cmds.push(...res.commands);
      }
      if (Option.isSome(m.panel) && message.agent) {
        const res = foldPanel(m, { _tag: "AgentChanged", agent: message.agent });
        m = res.model;
        if (res.commands) cmds.push(...res.commands);
      }
      return { model: m, commands: cmds };
    }

    case "ForwardRecurringEvent": {
      if (Option.isSome(model.panel)) {
        return foldPanel(model, {
          _tag: "RecurringChanged",
          repository: message.repository,
          number: message.number,
        });
      }
      return { model, commands: [] };
    }

    case "ForwardInboxEvent": {
      if (Option.isSome(model.tasksProgram)) {
        return foldTasks(model, { _tag: "BoardChanged" });
      }
      return { model, commands: [] };
    }

    case "ForwardLiveMessage": {
      if (Option.isSome(model.chat)) {
        return foldChat(model, { _tag: "ReceivedLiveMessage", message: message.message });
      }
      return { model, commands: [] };
    }

    case "ForwardLiveChatState": {
      if (Option.isSome(model.chat)) {
        return foldChat(model, { _tag: "ReceivedLiveChatState", state: message.state });
      }
      return { model, commands: [] };
    }

    case "ForwardLiveAction": {
      if (Option.isSome(model.chat)) {
        return foldChat(model, { _tag: "ReceivedLiveAction", action: message.action });
      }
      return { model, commands: [] };
    }

    case "ForwardLiveQuestion": {
      if (Option.isSome(model.chat)) {
        return foldChat(model, { _tag: "ReceivedLiveQuestion", question: message.question });
      }
      return { model, commands: [] };
    }

    // Submodel message handlers
    case "GotChatMessage":
      return foldChat(model, message.message);

    case "GotPanelMessage":
      return foldPanel(model, message.message);

    case "GotInboxMessage":
      return foldInbox(model, message.message);

    case "GotWorkstreamsMessage":
      return foldWorkstreams(model, message.message);

    case "GotTasksMessage":
      return foldTasks(model, message.message);

    case "GotAgentsMessage":
      return foldAgents(model, message.message);

    case "GotCheckupMessage":
      return foldCheckup(model, message.message);

    case "GotUsageMessage":
      return foldUsage(model, message.message);

    case "GotSettingsMessage":
      return foldSettings(model, message.message);

    case "GotRolesMessage":
      return foldRoles(model, message.message);

    case "GotMemoryMessage":
      return foldMemory(model, message.message);

    case "GotMcpServersMessage":
      return foldMcpServers(model, message.message);

    case "GotSwitchesMessage":
      return foldSwitches(model, message.message);

    case "GotVoiceMessage":
      return foldVoice(model, message.message);

    case "GotDevicesMessage":
      return foldDevices(model, message.message);

    case "GotGitHubMessage":
      return foldGitHub(model, message.message);

    default:
      return { model, commands: [] };
  }
}
